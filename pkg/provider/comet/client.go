package comet

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"math/rand"
	"unitool/internal/metrics"
	"unitool/pkg/provider"
)

type Client struct {
	base  string
	key   string
	httpc *http.Client
}

func New(base, key string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	return &Client{
		base:  trimRightSlash(base),
		key:   key,
		httpc: &http.Client{Timeout: timeout},
	}
}

func trimRightSlash(s string) string {
	if s == "" {
		return s
	}
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func (c *Client) Generate(ctx context.Context, req provider.ModelRequest) (provider.ModelResponse, error) {
	kind, _ := req.Params["kind"].(string)
	if kind == "" {
		kind = "text"
	}
	model := req.Model
	if model == "" {
		model = "gpt-4o"
	}

	switch kind {
	case "text":
		return c.generateChat(ctx, model, req.History, req.Input)
	case "image":
		return c.generateImage(ctx, model, req.Input)
	case "video":
		inputReference, _ := req.Params["input_reference"].(string)
		return c.generateVideo(ctx, model, req.Input, inputReference)
	default:
		return provider.ModelResponse{}, fmt.Errorf("unsupported kind: %s", kind)
	}
}

func (c *Client) GenerateStream(ctx context.Context, req provider.ModelRequest, onDelta func(string) error) (provider.ModelResponse, error) {
	kind, _ := req.Params["kind"].(string)
	if kind == "" {
		kind = "text"
	}
	if kind != "text" {
		// Fallback to non-stream generation for non-text kinds
		return c.Generate(ctx, req)
	}
	model := req.Model
	if model == "" {
		model = "gpt-4o"
	}

	// Build messages
	msgs := make([]oaMsg, 0, len(req.History)+1)
	for _, m := range req.History {
		if m.Role == "" || m.Content == "" {
			continue
		}
		msgs = append(msgs, oaMsg{Role: m.Role, Content: m.Content})
	}
	msgs = append(msgs, oaMsg{Role: "user", Content: req.Input})
	payload := chatReq{Model: model, Messages: msgs, Stream: true}

	// Attempt to establish stream with retries
	path := "/v1/chat/completions"
	attempts := 3
	var lastErr error
	var resp *http.Response
	var reqBody []byte
	var err error
	reqBody, err = json.Marshal(payload)
	if err != nil {
		return provider.ModelResponse{}, err
	}
	retries := 0
	for attempt := 0; attempt < attempts; attempt++ {
		// Respect context deadline; if cancelled, stop
		if ctx.Err() != nil {
			return provider.ModelResponse{}, ctx.Err()
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(reqBody))
		if err != nil {
			lastErr = err
			break
		}
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
	if lastErr != nil {
		return provider.ModelResponse{}, lastErr
	}
	if resp == nil {
		return provider.ModelResponse{}, errors.New("no http response")
	}
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
			if errors.Is(err, io.EOF) {
				break
			}
			decErr = err
			break
		}
		s := strings.TrimRight(line, "\r\n")
		if s == "" { // event delimiter
			// flush accumulated data lines
			for _, dl := range eventLines {
				data := strings.TrimSpace(strings.TrimPrefix(dl, "data:"))
				if data == "[DONE]" {
					eventLines = nil
					goto DONE
				}
				var chunk streamChunk
				if err := json.Unmarshal([]byte(data), &chunk); err != nil {
					continue
				}
				if metaID == "" && chunk.ID != "" {
					metaID = chunk.ID
				}
				piece := ""
				if len(chunk.Choices) > 0 {
					c0 := chunk.Choices[0]
					if c0.Delta.Content != "" {
						piece = c0.Delta.Content
					}
					if piece == "" && c0.Text != "" {
						piece = c0.Text
					}
				}
				if piece != "" {
					final.WriteString(piece)
					if onDelta != nil {
						_ = onDelta(piece)
					}
				}
			}
			eventLines = nil
			continue
		}
		if strings.HasPrefix(s, ":") {
			continue
		}
		if strings.HasPrefix(s, "data:") {
			eventLines = append(eventLines, s)
		}
	}
DONE:
	if decErr != nil {
		return provider.ModelResponse{}, decErr
	}
	// metrics for stream length
	metrics.CometStreamLength.WithLabelValues("chat_stream").Observe(float64(final.Len()))
	meta := map[string]any{"model": model}
	if metaID != "" {
		meta["id"] = metaID
	}
	return provider.ModelResponse{Output: final.String(), Tokens: 0, Meta: meta}, nil
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
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
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
		if m.Role == "" || m.Content == "" {
			continue
		}
		msgs = append(msgs, oaMsg{Role: m.Role, Content: m.Content})
	}
	msgs = append(msgs, oaMsg{Role: "user", Content: input})
	payload := chatReq{Model: model, Messages: msgs}
	var out chatResp
	if err := c.postJSON(ctx, "/v1/chat/completions", payload, &out); err != nil {
		return provider.ModelResponse{}, err
	}
	if len(out.Choices) == 0 {
		return provider.ModelResponse{}, errors.New("no choices returned")
	}
	content := extractMessageContentText(out.Choices[0].Message.Content)
	if content == "" {
		content = out.Choices[0].Text
	}
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
	Data    []struct {
		URL string `json:"url"`
	} `json:"data"`
}

func (c *Client) generateImage(ctx context.Context, model string, prompt string) (provider.ModelResponse, error) {
	if model == "" {
		model = "gpt-4o-image"
	}
	// Comet /v1/images/generations accepts imagen-family models.
	// Non-imagen models are routed through chat-compatible image generation.
	if shouldUseImagesEndpoint(model) {
		return c.generateImageViaImagesEndpoint(ctx, model, prompt)
	}
	return c.generateImageViaChat(ctx, model, prompt)
}

type cometAPIError struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
	Param   any    `json:"param"`
	Type    string `json:"type"`
}

type vidCreateResp struct {
	ID     string         `json:"id"`
	Status string         `json:"status"`
	Error  *cometAPIError `json:"error"`
}

type vidStatusResp struct {
	ID         string         `json:"id"`
	Status     string         `json:"status"`
	Progress   int            `json:"progress"`
	Error      *cometAPIError `json:"error"`
	URL        string         `json:"url"`
	OutputURL  string         `json:"output_url"`
	ContentURL string         `json:"content_url"`
	Data       struct {
		URL        string `json:"url"`
		OutputURL  string `json:"output_url"`
		ContentURL string `json:"content_url"`
	} `json:"data"`
}

func isSupportedVideoModel(model string) bool {
	switch normalizeVideoModel(model) {
	case "sora-2", "kling", "veo3":
		return true
	default:
		return false
	}
}

func (c *Client) generateVideo(ctx context.Context, model string, prompt string, inputReference string) (provider.ModelResponse, error) {
	model = normalizeVideoModel(model)
	if !isSupportedVideoModel(model) {
		return provider.ModelResponse{}, fmt.Errorf("unsupported video model %q for Comet /v1/videos (supported: sora-2, kling, veo3)", model)
	}

	videoID, err := c.createVideoTask(ctx, model, prompt, inputReference)
	if err != nil {
		return provider.ModelResponse{}, err
	}
	status, err := c.waitVideoComplete(ctx, videoID)
	if err != nil {
		return provider.ModelResponse{}, err
	}

	outURL := strings.TrimSpace(status.URL)
	if outURL == "" {
		outURL = strings.TrimSpace(status.OutputURL)
	}
	if outURL == "" {
		outURL = strings.TrimSpace(status.ContentURL)
	}
	if outURL == "" {
		outURL = strings.TrimSpace(status.Data.URL)
	}
	if outURL == "" {
		outURL = strings.TrimSpace(status.Data.OutputURL)
	}
	if outURL == "" {
		outURL = strings.TrimSpace(status.Data.ContentURL)
	}
	// Fallback endpoint from docs.
	if outURL == "" {
		outURL = fmt.Sprintf("%s/v1/videos/%s/content", c.base, videoID)
	}

	return provider.ModelResponse{
		Output: outURL,
		Tokens: 0,
		Meta: map[string]any{
			"id":       videoID,
			"status":   status.Status,
			"progress": status.Progress,
			"model":    model,
		},
	}, nil
}

func normalizeVideoModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch m {
	case "", "sora2", "sora-2":
		return "sora-2"
	case "kling", "kling-v1", "kling-v1-6", "kling-v1.6":
		return "kling"
	case "veo 3", "veo-3", "veo3", "veo3.1", "veo-3.1":
		return "veo3"
	default:
		return strings.TrimSpace(model)
	}
}

func shouldUseImagesEndpoint(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "imagen")
}

func (c *Client) generateImageViaImagesEndpoint(ctx context.Context, model string, prompt string) (provider.ModelResponse, error) {
	payload := imgReq{Model: model, Prompt: prompt}
	var out imgResp
	if err := c.postJSON(ctx, "/v1/images/generations", payload, &out); err != nil {
		return provider.ModelResponse{}, err
	}
	if len(out.Data) == 0 || strings.TrimSpace(out.Data[0].URL) == "" {
		return provider.ModelResponse{}, errors.New("no image url returned")
	}
	return provider.ModelResponse{
		Output: strings.TrimSpace(out.Data[0].URL),
		Tokens: 0,
		Meta:   map[string]any{"model": model, "source": "images/generations"},
	}, nil
}

func (c *Client) generateImageViaChat(ctx context.Context, model string, prompt string) (provider.ModelResponse, error) {
	payload := chatReq{
		Model: model,
		Messages: []oaMsg{
			{Role: "user", Content: prompt},
		},
	}
	var out chatResp
	if err := c.postJSON(ctx, "/v1/chat/completions", payload, &out); err != nil {
		return provider.ModelResponse{}, err
	}
	if len(out.Choices) == 0 {
		return provider.ModelResponse{}, errors.New("no choices returned")
	}
	content := extractMessageContentText(out.Choices[0].Message.Content)
	imageURL := extractMessageContentFirstURL(out.Choices[0].Message.Content)
	if imageURL == "" {
		imageURL = extractFirstImageReference(content)
	}
	if imageURL == "" {
		imageURL = extractFirstImageReference(strings.TrimSpace(out.Choices[0].Text))
	}
	if content == "" {
		content = strings.TrimSpace(out.Choices[0].Text)
	}
	if imageURL == "" {
		return provider.ModelResponse{}, fmt.Errorf("chat image response has no url: %q", shorten([]byte(content), 180))
	}
	return provider.ModelResponse{
		Output: imageURL,
		Tokens: out.Usage.TotalTokens,
		Meta:   map[string]any{"model": model, "source": "chat/completions", "id": out.ID},
	}, nil
}

var firstHTTPURL = regexp.MustCompile(`https?://[^\s<>"'\)\]]+`)

func extractFirstHTTPURL(text string) string {
	m := firstHTTPURL.FindString(strings.TrimSpace(text))
	if m == "" {
		return ""
	}
	return strings.TrimRight(m, ".,;:")
}

func extractFirstImageReference(text string) string {
	if u := extractFirstHTTPURL(text); u != "" {
		return u
	}
	return extractFirstDataImageURI(text)
}

func extractFirstDataImageURI(text string) string {
	s := strings.TrimSpace(text)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	start := strings.Index(lower, "data:image/")
	if start < 0 {
		return ""
	}
	rest := s[start:]
	end := len(rest)
	for i := 0; i < len(rest); i++ {
		if isDataURIStopByte(rest[i]) {
			end = i
			break
		}
	}
	candidate := strings.TrimSpace(rest[:end])
	if candidate == "" {
		return ""
	}
	lcand := strings.ToLower(candidate)
	if !strings.Contains(lcand, ";base64,") {
		return ""
	}
	return strings.TrimRight(candidate, ".,;:")
}

func isDataURIStopByte(b byte) bool {
	switch b {
	case ' ', '\n', '\r', '\t', ')', ']', '"', '\'':
		return true
	default:
		return false
	}
}

func extractMessageContentText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var v any
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return ""
	}
	parts := make([]string, 0, 4)
	collectTextParts(v, 0, &parts)
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func extractMessageContentFirstURL(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		return extractFirstImageReference(s)
	}
	var v any
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return ""
	}
	return findFirstURL(v, 0)
}

func collectTextParts(v any, depth int, out *[]string) {
	if depth > 8 {
		return
	}
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s != "" {
			*out = append(*out, s)
		}
	case []any:
		for _, it := range t {
			collectTextParts(it, depth+1, out)
		}
	case map[string]any:
		// Handle common OpenAI-compatible shapes first.
		if txt, ok := t["text"].(string); ok {
			collectTextParts(txt, depth+1, out)
		}
		if c, ok := t["content"]; ok {
			collectTextParts(c, depth+1, out)
		}
		// Fallback: scan nested values, useful for provider-specific wrappers.
		for _, val := range t {
			collectTextParts(val, depth+1, out)
		}
	}
}

func findFirstURL(v any, depth int) string {
	if depth > 10 {
		return ""
	}
	switch t := v.(type) {
	case string:
		return extractFirstImageReference(t)
	case []any:
		for _, it := range t {
			if u := findFirstURL(it, depth+1); u != "" {
				return u
			}
		}
	case map[string]any:
		// Prefer explicit url/image_url fields.
		if u, ok := t["url"].(string); ok {
			if out := extractFirstImageReference(u); out != "" {
				return out
			}
		}
		if v2, ok := t["image_url"]; ok {
			if out := findFirstURL(v2, depth+1); out != "" {
				return out
			}
		}
		if v2, ok := t["content"]; ok {
			if out := findFirstURL(v2, depth+1); out != "" {
				return out
			}
		}
		if txt, ok := t["text"].(string); ok {
			if out := extractFirstImageReference(txt); out != "" {
				return out
			}
		}
		for _, val := range t {
			if out := findFirstURL(val, depth+1); out != "" {
				return out
			}
		}
	}
	return ""
}

func (c *Client) createVideoTask(ctx context.Context, model, prompt, inputReference string) (string, error) {
	if c.key == "" {
		return "", errors.New("comet api key is empty")
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("prompt", prompt)
	_ = w.WriteField("model", model)
	if ref := strings.TrimSpace(inputReference); ref != "" {
		_ = w.WriteField("input_reference", ref)
	}
	_ = w.WriteField("seconds", "8")
	_ = w.WriteField("size", "720x1280")
	if err := w.Close(); err != nil {
		return "", err
	}

	attempts := 3
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/videos", bytes.NewReader(body.Bytes()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpc.Do(req)
		if err != nil {
			lastErr = err
		} else {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var out vidCreateResp
				if err := json.Unmarshal(b, &out); err != nil {
					return "", fmt.Errorf("comet video create invalid json (http %d): %s", resp.StatusCode, shorten(b, 200))
				}
				if out.Error != nil && out.Error.Message != "" {
					return "", fmt.Errorf("comet video create error: %s", out.Error.Message)
				}
				if strings.TrimSpace(out.ID) == "" {
					return "", fmt.Errorf("comet video create response missing id: %s", shorten(b, 200))
				}
				return out.ID, nil
			}

			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				lastErr = fmt.Errorf("comet video create http %d: %s", resp.StatusCode, shorten(b, 200))
				wait := c.computeBackoff(attempt, resp.Header.Get("Retry-After"))
				if !c.sleepContext(ctx, wait) {
					return "", ctx.Err()
				}
				continue
			}
			return "", fmt.Errorf("comet video create http %d: %s", resp.StatusCode, shorten(b, 200))
		}

		wait := c.computeBackoff(attempt, "")
		if !c.sleepContext(ctx, wait) {
			return "", ctx.Err()
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", errors.New("comet video create failed")
}

func (c *Client) waitVideoComplete(ctx context.Context, videoID string) (vidStatusResp, error) {
	const pollEvery = 3 * time.Second
	for {
		st, err := c.getVideoStatus(ctx, videoID)
		if err != nil {
			return vidStatusResp{}, err
		}
		switch strings.ToLower(strings.TrimSpace(st.Status)) {
		case "completed", "succeeded", "success", "done":
			return st, nil
		case "failed", "error", "canceled", "cancelled":
			if st.Error != nil && st.Error.Message != "" {
				return vidStatusResp{}, fmt.Errorf("comet video failed: %s", st.Error.Message)
			}
			return vidStatusResp{}, fmt.Errorf("comet video failed: status=%s", st.Status)
		}
		if !c.sleepContext(ctx, pollEvery) {
			return vidStatusResp{}, ctx.Err()
		}
	}
}

func (c *Client) getVideoStatus(ctx context.Context, videoID string) (vidStatusResp, error) {
	if c.key == "" {
		return vidStatusResp{}, errors.New("comet api key is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/videos/"+videoID, nil)
	if err != nil {
		return vidStatusResp{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return vidStatusResp{}, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return vidStatusResp{}, fmt.Errorf("comet video status http %d: %s", resp.StatusCode, shorten(b, 200))
	}
	var out vidStatusResp
	if err := json.Unmarshal(b, &out); err != nil {
		return vidStatusResp{}, fmt.Errorf("comet video status invalid json (http %d): %s", resp.StatusCode, shorten(b, 200))
	}
	return out, nil
}

func (c *Client) postJSON(ctx context.Context, path string, payload any, out any) error {
	if c.key == "" {
		return errors.New("comet api key is empty")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	attempts := 3
	var lastErr error
	start := time.Now()
	retries := 0
	for attempt := 0; attempt < attempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
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
						if err := json.Unmarshal(b, out); err != nil {
							errMsg := fmt.Errorf("comet invalid json response (http %d): %v; body: %s", resp.StatusCode, err, shorten(b, 200))
							if attempt < attempts-1 && isRetryableJSONDecodeError(err) {
								lastErr = errMsg
								wait := c.computeBackoff(attempt, resp.Header.Get("Retry-After"))
								if !c.sleepContext(ctx, wait) {
									return ctx.Err()
								}
								retries++
								continue
							}
							return errMsg
						}
					}
				}
				// metrics
				durMs := float64(time.Since(start).Milliseconds())
				metrics.CometRequests.WithLabelValues(labelFor(path), "ok", "200").Inc()
				metrics.CometLatencyMs.WithLabelValues(labelFor(path), "ok").Observe(durMs)
				if retries > 0 {
					metrics.CometRetries.WithLabelValues(labelFor(path)).Add(float64(retries))
				}
				return nil
			}
			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				lastErr = fmt.Errorf("comet http %d: %s", resp.StatusCode, shorten(b, 200))
				wait := c.computeBackoff(attempt, resp.Header.Get("Retry-After"))
				if !c.sleepContext(ctx, wait) {
					return ctx.Err()
				}
				retries++
				continue
			}
			// metrics for error
			durMs := float64(time.Since(start).Milliseconds())
			metrics.CometRequests.WithLabelValues(labelFor(path), "error", fmt.Sprintf("%d", resp.StatusCode)).Inc()
			metrics.CometLatencyMs.WithLabelValues(labelFor(path), "error").Observe(durMs)
			if retries > 0 {
				metrics.CometRetries.WithLabelValues(labelFor(path)).Add(float64(retries))
			}
			return fmt.Errorf("comet http %d: %s", resp.StatusCode, shorten(b, 200))
		}
		wait := c.computeBackoff(attempt, "")
		if !c.sleepContext(ctx, wait) {
			return ctx.Err()
		}
	}
	if lastErr != nil {
		durMs := float64(time.Since(start).Milliseconds())
		metrics.CometRequests.WithLabelValues(labelFor(path), "error", "neterr").Inc()
		metrics.CometLatencyMs.WithLabelValues(labelFor(path), "error").Observe(durMs)
		if retries > 0 {
			metrics.CometRetries.WithLabelValues(labelFor(path)).Add(float64(retries))
		}
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
			if dur > 0 {
				return capBackoff(dur)
			}
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
	if d <= 0 {
		return true
	}
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
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}

func labelFor(path string) string {
	if strings.Contains(path, "/chat/") {
		return "chat"
	}
	if strings.Contains(path, "/images/") {
		return "images"
	}
	if strings.Contains(path, "/videos/") {
		return "videos"
	}
	return path
}

func capBackoff(d time.Duration) time.Duration {
	max := 5 * time.Second
	if d > max {
		return max
	}
	return d
}

func isRetryableJSONDecodeError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unexpected end of json input")
}
