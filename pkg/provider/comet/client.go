package comet

import (
    "bufio"
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "strconv"
    "strings"
    "time"

    "unitool/pkg/provider"
    "unitool/internal/metrics"
    "math/rand"
)

type Client struct {
    base   string
    key    string
    httpc  *http.Client
}

func New(base, key string, timeout time.Duration) *Client {
    if timeout <= 0 { timeout = 25 * time.Second }
    return &Client{
        base:  trimRightSlash(base),
        key:   key,
        httpc: &http.Client{ Timeout: timeout },
    }
}

func trimRightSlash(s string) string {
    if s == "" { return s }
    for len(s) > 0 && s[len(s)-1] == '/' { s = s[:len(s)-1] }
    return s
}

func (c *Client) Generate(ctx context.Context, req provider.ModelRequest) (provider.ModelResponse, error) {
    kind, _ := req.Params["kind"].(string)
    if kind == "" { kind = "text" }
    model := req.Model
    if model == "" { model = "gpt-4o" }

    switch kind {
    case "text", "search":
        return c.generateChat(ctx, model, req.History, req.Input)
    case "image":
        return c.generateImage(ctx, model, req.Input)
    case "video":
        return c.generateVideo(ctx, model, req.Input)
    default:
        return provider.ModelResponse{}, fmt.Errorf("unsupported kind: %s", kind)
    }
}

func (c *Client) GenerateStream(ctx context.Context, req provider.ModelRequest, onDelta func(string) error) (provider.ModelResponse, error) {
    kind, _ := req.Params["kind"].(string)
    if kind == "" { kind = "text" }
    if kind != "text" && kind != "search" {
        // Fallback to non-stream generation for non-text kinds
        return c.Generate(ctx, req)
    }
    model := req.Model
    if model == "" { model = "gpt-4o" }

    // Build messages
    msgs := make([]oaMsg, 0, len(req.History)+1)
    for _, m := range req.History {
        if m.Role == "" || m.Content == "" { continue }
        msgs = append(msgs, oaMsg{Role: m.Role, Content: m.Content})
    }
    msgs = append(msgs, oaMsg{Role: "user", Content: req.Input})
    payload := chatReq{ Model: model, Messages: msgs, Stream: true }

    // Attempt to establish stream with retries
    path := "/v1/chat/completions"
    attempts := 3
    var lastErr error
    var resp *http.Response
    var reqBody []byte
    var err error
    reqBody, err = json.Marshal(payload)
    if err != nil { return provider.ModelResponse{}, err }
    retries := 0
    for attempt := 0; attempt < attempts; attempt++ {
        // Respect context deadline; if cancelled, stop
        if ctx.Err() != nil { return provider.ModelResponse{}, ctx.Err() }
        httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(reqBody))
        if err != nil { lastErr = err; break }
        httpReq.Header.Set("Authorization", "Bearer "+c.key)
        httpReq.Header.Set("Content-Type", "application/json")
        httpReq.Header.Set("Accept", "text/event-stream")

        resp, err = c.httpc.Do(httpReq)
        if err != nil {
            lastErr = err
            retries++
        } else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
            // success; stop retrying
            lastErr = nil
            break
        } else {
            // Read small portion for diagnostics, then backoff and retry
            b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
            _ = resp.Body.Close()
            wait := c.computeBackoff(attempt, resp.Header.Get("Retry-After"))
            if !c.sleepContext(ctx, wait) {
                return provider.ModelResponse{}, ctx.Err()
            }
            lastErr = fmt.Errorf("comet stream http %d: %s", resp.StatusCode, shorten(b, 200))
            retries++
            continue
        }
        // Exponential backoff for network errors
        wait := c.computeBackoff(attempt, "")
        if !c.sleepContext(ctx, wait) {
            return provider.ModelResponse{}, ctx.Err()
        }
    }
    if lastErr != nil { return provider.ModelResponse{}, lastErr }
    if resp == nil { return provider.ModelResponse{}, errors.New("no http response") }
    defer resp.Body.Close()

    // Stream parse
    br := bufio.NewReader(resp.Body)
    var final strings.Builder
    var eventLines []string
    var metaID string
    decErr := error(nil)
    for {
        line, err := br.ReadString('\n')
        if err != nil {
            if errors.Is(err, io.EOF) { break }
            decErr = err
            break
        }
        s := strings.TrimRight(line, "\r\n")
        if s == "" { // event delimiter
            // flush accumulated data lines
            for _, dl := range eventLines {
                data := strings.TrimSpace(strings.TrimPrefix(dl, "data:"))
                if data == "[DONE]" { eventLines = nil; goto DONE }
                var chunk streamChunk
                if err := json.Unmarshal([]byte(data), &chunk); err != nil { continue }
                if metaID == "" && chunk.ID != "" { metaID = chunk.ID }
                piece := ""
                if len(chunk.Choices) > 0 {
                    c0 := chunk.Choices[0]
                    if c0.Delta.Content != "" { piece = c0.Delta.Content }
                    if piece == "" && c0.Text != "" { piece = c0.Text }
                }
                if piece != "" {
                    final.WriteString(piece)
                    if onDelta != nil { _ = onDelta(piece) }
                }
            }
            eventLines = nil
            continue
        }
        if strings.HasPrefix(s, ":") { continue }
        if strings.HasPrefix(s, "data:") {
            eventLines = append(eventLines, s)
        }
    }
DONE:
    if decErr != nil { return provider.ModelResponse{}, decErr }
    // metrics for stream length
    metrics.CometStreamLength.WithLabelValues("chat_stream").Observe(float64(final.Len()))
    meta := map[string]any{"model": model}
    if metaID != "" { meta["id"] = metaID }
    return provider.ModelResponse{ Output: final.String(), Tokens: 0, Meta: meta }, nil
}

// OpenAI-compatible chat completions
type oaMsg struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}
type chatReq struct {
    Model    string  `json:"model"`
    Messages []oaMsg `json:"messages"`
    Stream   bool    `json:"stream,omitempty"`
}
type chatResp struct {
    ID      string `json:"id"`
    Choices []struct {
        Message struct{
            Role    string `json:"role"`
            Content string `json:"content"`
        } `json:"message"`
        // Some providers put text directly
        Text string `json:"text"`
    } `json:"choices"`
    Usage struct {
        PromptTokens     int `json:"prompt_tokens"`
        CompletionTokens int `json:"completion_tokens"`
        TotalTokens      int `json:"total_tokens"`
    } `json:"usage"`
}

// Stream chunk shape; compatible with OpenAI chat stream
type streamChunk struct {
    ID      string `json:"id"`
    Choices []struct {
        Delta struct {
            Content string `json:"content"`
        } `json:"delta"`
        Text string `json:"text"`
    } `json:"choices"`
}

func (c *Client) generateChat(ctx context.Context, model string, history []provider.Message, input string) (provider.ModelResponse, error) {
    msgs := make([]oaMsg, 0, len(history)+1)
    for _, m := range history {
        if m.Role == "" || m.Content == "" { continue }
        msgs = append(msgs, oaMsg{Role: m.Role, Content: m.Content})
    }
    msgs = append(msgs, oaMsg{Role: "user", Content: input})
    payload := chatReq{ Model: model, Messages: msgs }
    var out chatResp
    if err := c.postJSON(ctx, "/v1/chat/completions", payload, &out); err != nil {
        return provider.ModelResponse{}, err
    }
    if len(out.Choices) == 0 {
        return provider.ModelResponse{}, errors.New("no choices returned")
    }
    content := out.Choices[0].Message.Content
    if content == "" { content = out.Choices[0].Text }
    return provider.ModelResponse{
        Output: content,
        Tokens: out.Usage.TotalTokens,
        Meta:   map[string]any{"id": out.ID, "model": model},
    }, nil
}

// OpenAI-ish images API (best-effort, non-stream)
type imgReq struct {
    Model  string `json:"model"`
    Prompt string `json:"prompt"`
    Size   string `json:"size,omitempty"`
}
type imgResp struct {
    Created int64 `json:"created"`
    Data    []struct{ URL string `json:"url"` } `json:"data"`
}

func (c *Client) generateImage(ctx context.Context, model string, prompt string) (provider.ModelResponse, error) {
    payload := imgReq{ Model: model, Prompt: prompt }
    var out imgResp
    if err := c.postJSON(ctx, "/v1/images/generations", payload, &out); err != nil {
        return provider.ModelResponse{}, err
    }
    if len(out.Data) == 0 || out.Data[0].URL == "" {
        return provider.ModelResponse{}, errors.New("no image url returned")
    }
    return provider.ModelResponse{ Output: out.Data[0].URL, Tokens: 0, Meta: nil }, nil
}

// Hypothetical videos API; returns URL as text
type vidReq struct {
    Model  string `json:"model"`
    Prompt string `json:"prompt"`
}
type vidResp struct {
    ID   string `json:"id"`
    URL  string `json:"url"`
    Data []struct{ URL string `json:"url"` } `json:"data"`
}

func (c *Client) generateVideo(ctx context.Context, model string, prompt string) (provider.ModelResponse, error) {
    payload := vidReq{ Model: model, Prompt: prompt }
    var out vidResp
    if err := c.postJSON(ctx, "/v1/videos/generations", payload, &out); err != nil {
        return provider.ModelResponse{}, err
    }
    url := out.URL
    if url == "" && len(out.Data) > 0 {
        url = out.Data[0].URL
    }
    if url == "" { return provider.ModelResponse{}, errors.New("no video url returned") }
    return provider.ModelResponse{ Output: url, Tokens: 0, Meta: map[string]any{"id": out.ID} }, nil
}

func (c *Client) postJSON(ctx context.Context, path string, payload any, out any) error {
    if c.key == "" { return errors.New("comet api key is empty") }
    body, err := json.Marshal(payload)
    if err != nil { return err }
    attempts := 3
    var lastErr error
    start := time.Now()
    retries := 0
    for attempt := 0; attempt < attempts; attempt++ {
        if ctx.Err() != nil { return ctx.Err() }
        req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
        if err != nil { return err }
        req.Header.Set("Authorization", "Bearer "+c.key)
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Accept", "application/json")

        resp, err := c.httpc.Do(req)
        if err != nil {
            lastErr = err
            retries++
        } else {
            b, _ := io.ReadAll(resp.Body)
            resp.Body.Close()
            if resp.StatusCode >= 200 && resp.StatusCode < 300 {
                if out != nil {
                    if len(b) > 0 {
                        if err := json.Unmarshal(b, out); err != nil { return err }
                    }
                }
                // metrics
                durMs := float64(time.Since(start).Milliseconds())
                metrics.CometRequests.WithLabelValues(labelFor(path), "ok", "200").Inc()
                metrics.CometLatencyMs.WithLabelValues(labelFor(path), "ok").Observe(durMs)
                if retries > 0 { metrics.CometRetries.WithLabelValues(labelFor(path)).Add(float64(retries)) }
                return nil
            }
            if resp.StatusCode == 429 || resp.StatusCode >= 500 {
                lastErr = fmt.Errorf("comet http %d: %s", resp.StatusCode, shorten(b, 200))
                wait := c.computeBackoff(attempt, resp.Header.Get("Retry-After"))
                if !c.sleepContext(ctx, wait) { return ctx.Err() }
                retries++
                continue
            }
            // metrics for error
            durMs := float64(time.Since(start).Milliseconds())
            metrics.CometRequests.WithLabelValues(labelFor(path), "error", fmt.Sprintf("%d", resp.StatusCode)).Inc()
            metrics.CometLatencyMs.WithLabelValues(labelFor(path), "error").Observe(durMs)
            if retries > 0 { metrics.CometRetries.WithLabelValues(labelFor(path)).Add(float64(retries)) }
            return fmt.Errorf("comet http %d: %s", resp.StatusCode, shorten(b, 200))
        }
        wait := c.computeBackoff(attempt, "")
        if !c.sleepContext(ctx, wait) { return ctx.Err() }
    }
    if lastErr != nil {
        durMs := float64(time.Since(start).Milliseconds())
        metrics.CometRequests.WithLabelValues(labelFor(path), "error", "neterr").Inc()
        metrics.CometLatencyMs.WithLabelValues(labelFor(path), "error").Observe(durMs)
        if retries > 0 { metrics.CometRetries.WithLabelValues(labelFor(path)).Add(float64(retries)) }
        return lastErr
    }
    return errors.New("comet request failed")
}

func (c *Client) computeBackoff(attempt int, retryAfter string) time.Duration {
    // Respect Retry-After seconds if present
    if retryAfter != "" {
        if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs >= 0 {
            return capBackoff(time.Duration(secs) * time.Second)
        }
        if t, err := http.ParseTime(retryAfter); err == nil {
            dur := time.Until(t)
            if dur > 0 { return capBackoff(dur) }
        }
    }
    base := 300 * time.Millisecond
    // exponential backoff: base * 2^attempt
    d := base << uint(attempt)
    // true random jitter up to 200ms
    jitter := time.Duration(rand.Int63n(int64(200 * time.Millisecond)))
    return capBackoff(d + jitter)
}

func (c *Client) sleepContext(ctx context.Context, d time.Duration) bool {
    if d <= 0 { return true }
    t := time.NewTimer(d)
    defer t.Stop()
    select {
    case <-ctx.Done():
        return false
    case <-t.C:
        return true
    }
}

func shorten(b []byte, max int) string {
    if len(b) <= max { return string(b) }
    return string(b[:max]) + "..."
}

func labelFor(path string) string {
    if strings.Contains(path, "/chat/") { return "chat" }
    if strings.Contains(path, "/images/") { return "images" }
    if strings.Contains(path, "/videos/") { return "videos" }
    return path
}

func capBackoff(d time.Duration) time.Duration {
    max := 5 * time.Second
    if d > max { return max }
    return d
}
