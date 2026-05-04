package comet

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
		return c.generateImage(ctx, model, req.Input, inputReferencesFromParams(req.Params))
	case "video":
		return c.generateVideo(ctx, model, req.Input, inputReferencesFromParams(req.Params))
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
	Content any    `json:"content"`
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

type klingImageCreateReq struct {
	Prompt           string                   `json:"prompt,omitempty"`
	Image            string                   `json:"image,omitempty"`
	ModelName        string                   `json:"model_name,omitempty"`
	SubjectImageList []klingSubjectImageEntry `json:"subject_image_list,omitempty"`
}

type klingSubjectImageEntry struct {
	SubjectImage string `json:"subject_image,omitempty"`
}

type geminiGenerateContentReq struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inline_data,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
}

type geminiGenerationConfig struct {
	ResponseModalities []string `json:"responseModalities,omitempty"`
}

type geminiGenerateContentResp struct {
	Candidates []struct {
		Content struct {
			Role  string `json:"role"`
			Parts []struct {
				Text       string `json:"text"`
				InlineData struct {
					MimeType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
				InlineDataAlt struct {
					MimeType string `json:"mime_type"`
					Data     string `json:"data"`
				} `json:"inline_data"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
		ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
}

func (c *Client) generateImage(ctx context.Context, model string, prompt string, refs []string) (provider.ModelResponse, error) {
	if model == "" {
		model = "gpt-4o-image"
	}
	if isKlingImageModel(model) {
		return c.generateKlingImage(ctx, model, prompt, refs)
	}
	if isGeminiNativeImageModel(model) {
		if canUseGeminiGenerateContent(refs) {
			return c.generateImageViaGemini(ctx, model, prompt, refs)
		}
		return c.generateImageViaChat(ctx, model, prompt, refs)
	}
	if len(refs) > 0 {
		return c.generateImageViaChat(ctx, model, prompt, refs)
	}
	// Comet /v1/images/generations accepts imagen-family models.
	// Non-imagen models are routed through chat-compatible image generation.
	if shouldUseImagesEndpoint(model) {
		return c.generateImageViaImagesEndpoint(ctx, model, prompt)
	}
	return c.generateImageViaChat(ctx, model, prompt, nil)
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

type klingVideoCreateReq struct {
	Prompt    string                 `json:"prompt,omitempty"`
	Image     string                 `json:"image,omitempty"`
	ImageList []klingVideoImageEntry `json:"image_list,omitempty"`
	ModelName string                 `json:"model_name,omitempty"`
	Duration  string                 `json:"duration,omitempty"`
}

type klingVideoImageEntry struct {
	Image string `json:"image,omitempty"`
}

type klingTaskAsset struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Duration string `json:"duration"`
	Index    int    `json:"index"`
}

type klingTaskResult struct {
	Images []klingTaskAsset `json:"images"`
	Videos []klingTaskAsset `json:"videos"`
	Image  string           `json:"image"`
	URL    string           `json:"url"`
}

type klingResponseCode string

func (c *klingResponseCode) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) || len(data) == 0 {
		*c = ""
		return nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*c = klingResponseCode(strings.TrimSpace(s))
		return nil
	}

	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		*c = klingResponseCode(strconv.Itoa(n))
		return nil
	}

	var f float64
	if err := json.Unmarshal(data, &f); err == nil {
		*c = klingResponseCode(strconv.FormatFloat(f, 'f', -1, 64))
		return nil
	}

	return fmt.Errorf("unsupported kling code: %s", string(data))
}

func (c klingResponseCode) IsSuccess() bool {
	switch strings.ToLower(strings.TrimSpace(string(c))) {
	case "0", "success", "ok":
		return true
	default:
		return false
	}
}

type klingVideoTaskEnvelope struct {
	Code          klingResponseCode `json:"code"`
	Message       string            `json:"message"`
	RequestID     string            `json:"request_id"`
	TaskID        string            `json:"task_id"`
	TaskStatus    string            `json:"task_status"`
	Status        string            `json:"status"`
	TaskStatusMsg string            `json:"task_status_msg"`
	FailReason    string            `json:"fail_reason"`
	ResultURL     string            `json:"result_url"`
	TaskResult    klingTaskResult   `json:"task_result"`
	CreatedAt     int64             `json:"created_at"`
	UpdatedAt     int64             `json:"updated_at"`
	Data          struct {
		TaskID        string          `json:"task_id"`
		TaskStatus    string          `json:"task_status"`
		Status        string          `json:"status"`
		TaskStatusMsg string          `json:"task_status_msg"`
		FailReason    string          `json:"fail_reason"`
		ResultURL     string          `json:"result_url"`
		TaskResult    klingTaskResult `json:"task_result"`
		CreatedAt     int64           `json:"created_at"`
		UpdatedAt     int64           `json:"updated_at"`
		Data          struct {
			Code          klingResponseCode `json:"code"`
			Message       string            `json:"message"`
			RequestID     string            `json:"request_id"`
			TaskID        string            `json:"task_id"`
			TaskStatus    string            `json:"task_status"`
			Status        string            `json:"status"`
			TaskStatusMsg string            `json:"task_status_msg"`
			FailReason    string            `json:"fail_reason"`
			ResultURL     string            `json:"result_url"`
			TaskResult    klingTaskResult   `json:"task_result"`
			Data          struct {
				TaskID        string          `json:"task_id"`
				TaskStatus    string          `json:"task_status"`
				Status        string          `json:"status"`
				TaskStatusMsg string          `json:"task_status_msg"`
				FailReason    string          `json:"fail_reason"`
				ResultURL     string          `json:"result_url"`
				TaskResult    klingTaskResult `json:"task_result"`
				CreatedAt     int64           `json:"created_at"`
				UpdatedAt     int64           `json:"updated_at"`
			} `json:"data"`
		} `json:"data"`
	} `json:"data"`
}

func klingTaskStatus(out klingVideoTaskEnvelope) string {
	if status := strings.TrimSpace(out.TaskStatus); status != "" {
		return status
	}
	if status := strings.TrimSpace(out.Status); status != "" {
		return status
	}
	if status := strings.TrimSpace(out.Data.TaskStatus); status != "" {
		return status
	}
	if status := strings.TrimSpace(out.Data.Data.TaskStatus); status != "" {
		return status
	}
	if status := strings.TrimSpace(out.Data.Data.Data.TaskStatus); status != "" {
		return status
	}
	if status := strings.TrimSpace(out.Data.Data.Status); status != "" {
		return status
	}
	return strings.TrimSpace(out.Data.Status)
}

func klingTaskFailureReason(out klingVideoTaskEnvelope) string {
	if msg := strings.TrimSpace(out.TaskStatusMsg); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(out.FailReason); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(out.Data.TaskStatusMsg); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(out.Data.Data.TaskStatusMsg); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(out.Data.FailReason); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(out.Data.Data.FailReason); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(out.Data.Data.Data.TaskStatusMsg); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(out.Data.Data.Data.FailReason); msg != "" {
		return msg
	}
	return strings.TrimSpace(out.Message)
}

func isSupportedVideoModel(model string) bool {
	switch normalizeVideoModel(model) {
	case "sora-2", "kling-v1", "kling-v1-6", "veo3":
		return true
	default:
		return false
	}
}

func (c *Client) generateVideo(ctx context.Context, model string, prompt string, inputReferences []string) (provider.ModelResponse, error) {
	model = normalizeVideoModel(model)
	if !isSupportedVideoModel(model) {
		return provider.ModelResponse{}, fmt.Errorf("unsupported video model %q for Comet video generation (supported: sora-2, kling-v1, kling-v1-6, veo3)", model)
	}
	if isKlingVideoModel(model) {
		return c.generateKlingVideo(ctx, model, prompt, inputReferences)
	}

	inputReference := ""
	if len(inputReferences) > 0 {
		inputReference = inputReferences[0]
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
	case "kling", "kling-v2-master":
		return "kling-v1-6"
	case "kling-v1":
		return "kling-v1"
	case "kling-v1-6", "kling-v1.6":
		return "kling-v1-6"
	case "veo 3", "veo-3", "veo3", "veo3.1", "veo-3.1":
		return "veo3"
	default:
		return strings.TrimSpace(model)
	}
}

func isKlingVideoModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "kling-v1", "kling-v1-6", "kling-v2-master":
		return true
	default:
		return false
	}
}

func shouldUseImagesEndpoint(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "imagen")
}

func isKlingImageModel(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), "kling-v2")
}

func isGeminiNativeImageModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "gemini-") && strings.Contains(m, "image")
}

func canUseGeminiGenerateContent(refs []string) bool {
	for _, ref := range refs {
		if _, _, ok := splitDataImageURI(ref); !ok {
			return false
		}
	}
	return true
}

func (c *Client) generateKlingVideo(ctx context.Context, model string, prompt string, inputReferences []string) (provider.ModelResponse, error) {
	path, payload := buildKlingVideoRequest(model, prompt, inputReferences)

	task, err := c.createKlingVideoTask(ctx, path, payload)
	if err != nil {
		return provider.ModelResponse{}, err
	}
	status, err := c.waitKlingVideoComplete(ctx, path, task)
	if err != nil {
		return provider.ModelResponse{}, err
	}
	outURL := firstKlingVideoURL(status)
	if outURL == "" {
		return provider.ModelResponse{}, fmt.Errorf("kling task %s finished without output url", task)
	}
	return provider.ModelResponse{
		Output: outURL,
		Tokens: 0,
		Meta: map[string]any{
			"id":         task,
			"status":     klingTaskStatus(status),
			"model":      model,
			"request_id": status.RequestID,
			"source":     path,
		},
	}, nil
}

func buildKlingVideoRequest(model string, prompt string, inputReferences []string) (string, klingVideoCreateReq) {
	refs := normalizeKlingVideoReferences(inputReferences)
	payload := klingVideoCreateReq{
		Prompt:    prompt,
		ModelName: model,
		Duration:  "5",
	}
	switch len(refs) {
	case 0:
		return "/kling/v1/videos/text2video", payload
	case 1:
		payload.Image = refs[0]
		return "/kling/v1/videos/image2video", payload
	default:
		payload.ImageList = make([]klingVideoImageEntry, 0, len(refs))
		for _, ref := range refs {
			payload.ImageList = append(payload.ImageList, klingVideoImageEntry{Image: ref})
		}
		return "/kling/v1/videos/multi-image2video", payload
	}
}

func normalizeKlingVideoReferences(inputReferences []string) []string {
	refs := make([]string, 0, len(inputReferences))
	for _, inputReference := range inputReferences {
		if ref := normalizeKlingImageInput(inputReference); ref != "" {
			refs = append(refs, ref)
			if len(refs) == 4 {
				break
			}
		}
	}
	return refs
}

func normalizeKlingImageInput(inputReference string) string {
	ref := strings.TrimSpace(inputReference)
	if ref == "" {
		return ""
	}
	lower := strings.ToLower(ref)
	if !strings.HasPrefix(lower, "data:image/") {
		return ref
	}
	comma := strings.IndexByte(ref, ',')
	if comma <= 0 {
		return ref
	}
	meta := strings.ToLower(ref[:comma])
	if !strings.Contains(meta, ";base64") {
		return ref
	}
	return strings.TrimSpace(ref[comma+1:])
}

func (c *Client) createKlingVideoTask(ctx context.Context, path string, payload klingVideoCreateReq) (string, error) {
	if c.key == "" {
		return "", errors.New("comet api key is empty")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	attempts := 3
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpc.Do(req)
		if err != nil {
			lastErr = err
		} else {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var out klingVideoTaskEnvelope
				if err := json.Unmarshal(b, &out); err != nil {
					return "", fmt.Errorf("comet kling create invalid json (http %d): %s", resp.StatusCode, shorten(b, 200))
				}
				if code := strings.TrimSpace(string(out.Code)); code != "" && !out.Code.IsSuccess() {
					msg := strings.TrimSpace(out.Message)
					if msg == "" {
						msg = "unknown error"
					}
					return "", fmt.Errorf("comet kling create error: %s", msg)
				}
				taskID := klingTaskID(out)
				if taskID == "" {
					return "", fmt.Errorf("comet kling create response missing task_id: %s", shorten(b, 200))
				}
				return taskID, nil
			}

			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				lastErr = fmt.Errorf("comet kling create http %d: %s", resp.StatusCode, shorten(b, 200))
				wait := c.computeBackoff(attempt, resp.Header.Get("Retry-After"))
				if !c.sleepContext(ctx, wait) {
					return "", ctx.Err()
				}
				continue
			}
			return "", fmt.Errorf("comet kling create http %d: %s", resp.StatusCode, shorten(b, 200))
		}

		wait := c.computeBackoff(attempt, "")
		if !c.sleepContext(ctx, wait) {
			return "", ctx.Err()
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", errors.New("comet kling create failed")
}

func (c *Client) waitKlingVideoComplete(ctx context.Context, createPath string, taskID string) (klingVideoTaskEnvelope, error) {
	const pollEvery = 3 * time.Second
	for {
		st, err := c.getKlingVideoStatus(ctx, createPath, taskID)
		if err != nil {
			return klingVideoTaskEnvelope{}, err
		}
		switch strings.ToLower(klingTaskStatus(st)) {
		case "succeed", "succeeded", "success", "completed", "done":
			return st, nil
		case "failed", "error", "canceled", "cancelled":
			msg := klingTaskFailureReason(st)
			if msg == "" {
				msg = fmt.Sprintf("status=%s", klingTaskStatus(st))
			}
			return klingVideoTaskEnvelope{}, fmt.Errorf("comet kling video failed: %s", msg)
		}
		if !c.sleepContext(ctx, pollEvery) {
			return klingVideoTaskEnvelope{}, ctx.Err()
		}
	}
}

func (c *Client) getKlingVideoStatus(ctx context.Context, createPath string, taskID string) (klingVideoTaskEnvelope, error) {
	if c.key == "" {
		return klingVideoTaskEnvelope{}, errors.New("comet api key is empty")
	}
	path := strings.TrimRight(createPath, "/") + "/" + strings.TrimSpace(taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return klingVideoTaskEnvelope{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return klingVideoTaskEnvelope{}, err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return klingVideoTaskEnvelope{}, fmt.Errorf("comet kling status http %d: %s", resp.StatusCode, shorten(b, 200))
	}

	var out klingVideoTaskEnvelope
	if err := json.Unmarshal(b, &out); err != nil {
		return klingVideoTaskEnvelope{}, fmt.Errorf("comet kling status invalid json (http %d): %s", resp.StatusCode, shorten(b, 200))
	}
	if err := validateKlingStatusEnvelope(out, b); err != nil {
		return klingVideoTaskEnvelope{}, err
	}
	status := strings.ToLower(klingTaskStatus(out))
	if status == "succeed" || status == "succeeded" || status == "success" || status == "completed" || status == "done" {
		switch {
		case strings.Contains(createPath, "/videos/") && firstKlingVideoURL(out) == "":
			if fallback := firstKlingVideoURLFromRawJSON(b); fallback != "" {
				out.Data.ResultURL = fallback
				break
			}
			log.Printf(
				"comet kling debug: terminal video status without output url path=%s task_id=%s body=%s",
				createPath,
				strings.TrimSpace(taskID),
				shorten(b, 2000),
			)
		case strings.Contains(createPath, "/images/") && firstKlingImageURL(out) == "":
			if fallback := firstKlingImageURLFromRawJSON(b); fallback != "" {
				out.Data.ResultURL = fallback
				break
			}
			log.Printf(
				"comet kling debug: terminal image status without output url path=%s task_id=%s body=%s",
				createPath,
				strings.TrimSpace(taskID),
				shorten(b, 2000),
			)
		}
	}
	return out, nil
}

func validateKlingStatusEnvelope(out klingVideoTaskEnvelope, body []byte) error {
	code := strings.TrimSpace(string(out.Code))
	if code == "" {
		code = strings.TrimSpace(string(out.Data.Data.Code))
	}
	if code != "" && !klingResponseCode(code).IsSuccess() {
		msg := firstNonEmpty(out.Message, out.Data.Data.Message)
		if msg == "" {
			msg = "unknown error"
		}
		return fmt.Errorf("comet kling status error: %s", msg)
	}
	if code == "" && klingTaskStatus(out) == "" && firstKlingVideoURL(out) == "" && firstKlingImageURL(out) == "" {
		return fmt.Errorf("comet kling status malformed response: %s", shorten(body, 200))
	}
	return nil
}

func klingTaskID(out klingVideoTaskEnvelope) string {
	if id := strings.TrimSpace(out.TaskID); id != "" {
		return id
	}
	if id := strings.TrimSpace(out.Data.TaskID); id != "" {
		return id
	}
	if id := strings.TrimSpace(out.Data.Data.TaskID); id != "" {
		return id
	}
	return strings.TrimSpace(out.Data.Data.Data.TaskID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstKlingVideoURL(out klingVideoTaskEnvelope) string {
	for _, item := range out.TaskResult.Videos {
		if url := strings.TrimSpace(item.URL); url != "" {
			return url
		}
	}
	if url := strings.TrimSpace(out.ResultURL); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.TaskResult.URL); url != "" {
		return url
	}
	for _, item := range out.Data.TaskResult.Videos {
		if url := strings.TrimSpace(item.URL); url != "" {
			return url
		}
	}
	for _, item := range out.Data.Data.TaskResult.Videos {
		if url := strings.TrimSpace(item.URL); url != "" {
			return url
		}
	}
	if url := strings.TrimSpace(out.Data.ResultURL); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.Data.Data.ResultURL); url != "" {
		return url
	}
	for _, item := range out.Data.Data.Data.TaskResult.Videos {
		if url := strings.TrimSpace(item.URL); url != "" {
			return url
		}
	}
	if url := strings.TrimSpace(out.Data.Data.Data.ResultURL); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.Data.TaskResult.URL); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.Data.Data.TaskResult.URL); url != "" {
		return url
	}
	return ""
}

func firstKlingImageURL(out klingVideoTaskEnvelope) string {
	for _, item := range out.TaskResult.Images {
		if url := strings.TrimSpace(item.URL); url != "" {
			return url
		}
	}
	if url := strings.TrimSpace(out.ResultURL); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.TaskResult.URL); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.TaskResult.Image); url != "" {
		return url
	}
	for _, item := range out.Data.TaskResult.Images {
		if url := strings.TrimSpace(item.URL); url != "" {
			return url
		}
	}
	for _, item := range out.Data.Data.TaskResult.Images {
		if url := strings.TrimSpace(item.URL); url != "" {
			return url
		}
	}
	if url := strings.TrimSpace(out.Data.TaskResult.Image); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.Data.Data.TaskResult.Image); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.Data.TaskResult.URL); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.Data.Data.TaskResult.URL); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.Data.ResultURL); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.Data.Data.ResultURL); url != "" {
		return url
	}
	for _, item := range out.Data.Data.Data.TaskResult.Images {
		if url := strings.TrimSpace(item.URL); url != "" {
			return url
		}
	}
	if url := strings.TrimSpace(out.Data.Data.Data.TaskResult.Image); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.Data.Data.Data.TaskResult.URL); url != "" {
		return url
	}
	if url := strings.TrimSpace(out.Data.Data.Data.ResultURL); url != "" {
		return url
	}
	return ""
}

func firstKlingVideoURLFromRawJSON(body []byte) string {
	return firstKlingURLFromRawJSON(body, "video")
}

func firstKlingImageURLFromRawJSON(body []byte) string {
	return firstKlingURLFromRawJSON(body, "image")
}

func firstKlingURLFromRawJSON(body []byte, kind string) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return ""
	}
	return firstKlingURLFromNode(root, kind, 0)
}

func firstKlingURLFromNode(node any, kind string, depth int) string {
	if depth > 12 {
		return ""
	}
	obj, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	if url := stringValue(obj["result_url"]); url != "" {
		return url
	}
	if taskResult, ok := obj["task_result"]; ok {
		if url := firstKlingURLFromTaskResult(taskResult, kind); url != "" {
			return url
		}
	}
	if data, ok := obj["data"]; ok {
		switch v := data.(type) {
		case map[string]any:
			if url := firstKlingURLFromNode(v, kind, depth+1); url != "" {
				return url
			}
		case []any:
			for _, item := range v {
				if m, ok := item.(map[string]any); ok {
					if url := firstKlingURLFromNode(m, kind, depth+1); url != "" {
						return url
					}
				}
			}
		}
	}
	return ""
}

func firstKlingURLFromTaskResult(raw any, kind string) string {
	obj, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	if kind == "video" {
		if url := firstKlingAssetListURL(obj["videos"]); url != "" {
			return url
		}
	} else {
		if url := firstKlingAssetListURL(obj["images"]); url != "" {
			return url
		}
		if url := stringValue(obj["image"]); url != "" {
			return url
		}
	}
	if url := stringValue(obj["url"]); url != "" {
		return url
	}
	return ""
}

func firstKlingAssetListURL(raw any) string {
	items, ok := raw.([]any)
	if !ok {
		return ""
	}
	for _, item := range items {
		switch v := item.(type) {
		case map[string]any:
			if url := stringValue(v["url"]); url != "" {
				return url
			}
		case string:
			if url := strings.TrimSpace(v); url != "" {
				return url
			}
		}
	}
	return ""
}

func stringValue(raw any) string {
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func (c *Client) generateKlingImage(ctx context.Context, model string, prompt string, refs []string) (provider.ModelResponse, error) {
	payload := klingImageCreateReq{
		Prompt:    prompt,
		ModelName: strings.TrimSpace(model),
	}
	normalizedRefs := make([]string, 0, len(refs))
	for _, ref := range refs {
		if norm := normalizeKlingImageInput(ref); norm != "" {
			normalizedRefs = append(normalizedRefs, norm)
		}
	}
	switch len(normalizedRefs) {
	case 0:
	case 1:
		payload.Image = normalizedRefs[0]
	default:
		payload.SubjectImageList = make([]klingSubjectImageEntry, 0, len(normalizedRefs))
		for _, ref := range normalizedRefs {
			payload.SubjectImageList = append(payload.SubjectImageList, klingSubjectImageEntry{
				SubjectImage: ref,
			})
		}
	}

	const path = "/kling/v1/images/generations"
	task, err := c.createKlingImageTask(ctx, path, payload)
	if err != nil {
		return provider.ModelResponse{}, err
	}
	status, err := c.waitKlingImageComplete(ctx, path, task)
	if err != nil {
		return provider.ModelResponse{}, err
	}
	outURL := firstKlingImageURL(status)
	if outURL == "" {
		return provider.ModelResponse{}, fmt.Errorf("kling image task %s finished without output url", task)
	}
	return provider.ModelResponse{
		Output: outURL,
		Tokens: 0,
		Meta: map[string]any{
			"id":         task,
			"status":     klingTaskStatus(status),
			"model":      model,
			"request_id": status.RequestID,
			"source":     path,
		},
	}, nil
}

func (c *Client) createKlingImageTask(ctx context.Context, path string, payload klingImageCreateReq) (string, error) {
	if c.key == "" {
		return "", errors.New("comet api key is empty")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	attempts := 3
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpc.Do(req)
		if err != nil {
			lastErr = err
		} else {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var out klingVideoTaskEnvelope
				if err := json.Unmarshal(b, &out); err != nil {
					return "", fmt.Errorf("comet kling image create invalid json (http %d): %s", resp.StatusCode, shorten(b, 200))
				}
				if !out.Code.IsSuccess() {
					msg := strings.TrimSpace(out.Message)
					if msg == "" {
						msg = "unknown error"
					}
					return "", fmt.Errorf("comet kling image create error: %s", msg)
				}
				taskID := strings.TrimSpace(out.Data.TaskID)
				if taskID == "" {
					return "", fmt.Errorf("comet kling image create response missing task_id: %s", shorten(b, 200))
				}
				return taskID, nil
			}

			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				lastErr = fmt.Errorf("comet kling image create http %d: %s", resp.StatusCode, shorten(b, 200))
				wait := c.computeBackoff(attempt, resp.Header.Get("Retry-After"))
				if !c.sleepContext(ctx, wait) {
					return "", ctx.Err()
				}
				continue
			}
			return "", fmt.Errorf("comet kling image create http %d: %s", resp.StatusCode, shorten(b, 200))
		}

		wait := c.computeBackoff(attempt, "")
		if !c.sleepContext(ctx, wait) {
			return "", ctx.Err()
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", errors.New("comet kling image create failed")
}

func (c *Client) waitKlingImageComplete(ctx context.Context, createPath string, taskID string) (klingVideoTaskEnvelope, error) {
	const pollEvery = 3 * time.Second
	for {
		st, err := c.getKlingVideoStatus(ctx, createPath, taskID)
		if err != nil {
			return klingVideoTaskEnvelope{}, err
		}
		switch strings.ToLower(klingTaskStatus(st)) {
		case "succeed", "succeeded", "success", "completed", "done":
			return st, nil
		case "failed", "error", "canceled", "cancelled":
			msg := klingTaskFailureReason(st)
			if msg == "" {
				msg = fmt.Sprintf("status=%s", klingTaskStatus(st))
			}
			return klingVideoTaskEnvelope{}, fmt.Errorf("comet kling image failed: %s", msg)
		}
		if !c.sleepContext(ctx, pollEvery) {
			return klingVideoTaskEnvelope{}, ctx.Err()
		}
	}
}

func (c *Client) generateImageViaGemini(ctx context.Context, model string, prompt string, refs []string) (provider.ModelResponse, error) {
	parts := make([]geminiPart, 0, len(refs)+1)
	if strings.TrimSpace(prompt) != "" {
		parts = append(parts, geminiPart{Text: prompt})
	}
	for _, ref := range refs {
		mime, data, ok := splitDataImageURI(ref)
		if !ok {
			return provider.ModelResponse{}, fmt.Errorf("gemini image reference must be a data uri")
		}
		parts = append(parts, geminiPart{
			InlineData: &geminiInlineData{
				MimeType: mime,
				Data:     data,
			},
		})
	}
	if len(parts) == 0 {
		return provider.ModelResponse{}, errors.New("gemini image request is empty")
	}

	payload := geminiGenerateContentReq{
		Contents: []geminiContent{{
			Role:  "user",
			Parts: parts,
		}},
		GenerationConfig: geminiGenerationConfig{
			ResponseModalities: []string{"IMAGE"},
		},
	}

	path := fmt.Sprintf("/v1beta/models/%s:generateContent", strings.TrimSpace(model))
	var out geminiGenerateContentResp
	if err := c.postJSON(ctx, path, payload, &out); err != nil {
		return provider.ModelResponse{}, err
	}

	output := firstGeminiImageDataURI(out)
	if output == "" {
		return provider.ModelResponse{}, errors.New("gemini image response has no inline image")
	}
	return provider.ModelResponse{
		Output: output,
		Tokens: out.UsageMetadata.TotalTokenCount,
		Meta:   map[string]any{"model": model, "source": "v1beta/models:generateContent"},
	}, nil
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

func (c *Client) generateImageViaChat(ctx context.Context, model string, prompt string, refs []string) (provider.ModelResponse, error) {
	userContent := any(prompt)
	if len(refs) > 0 {
		parts := make([]map[string]any, 0, len(refs)+1)
		parts = append(parts, map[string]any{
			"type": "text",
			"text": prompt,
		})
		for _, ref := range refs {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			parts = append(parts, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": ref,
				},
			})
		}
		userContent = parts
	}
	payload := chatReq{
		Model: model,
		Messages: []oaMsg{
			{Role: "user", Content: userContent},
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

func splitDataImageURI(raw string) (string, string, bool) {
	candidate := extractFirstDataImageURI(raw)
	if candidate == "" {
		return "", "", false
	}
	comma := strings.IndexByte(candidate, ',')
	if comma <= 0 || comma == len(candidate)-1 {
		return "", "", false
	}
	meta := strings.ToLower(strings.TrimSpace(candidate[:comma]))
	if !strings.HasPrefix(meta, "data:image/") || !strings.Contains(meta, ";base64") {
		return "", "", false
	}
	mime := strings.TrimSpace(strings.TrimPrefix(strings.Split(meta, ";")[0], "data:"))
	data := strings.TrimSpace(candidate[comma+1:])
	data = strings.NewReplacer("\n", "", "\r", "", "\t", "", " ", "").Replace(data)
	if mime == "" || data == "" {
		return "", "", false
	}
	return mime, data, true
}

func firstGeminiImageDataURI(out geminiGenerateContentResp) string {
	for _, candidate := range out.Candidates {
		for _, part := range candidate.Content.Parts {
			mime := strings.TrimSpace(part.InlineData.MimeType)
			data := strings.TrimSpace(part.InlineData.Data)
			if mime == "" || data == "" {
				mime = strings.TrimSpace(part.InlineDataAlt.MimeType)
				data = strings.TrimSpace(part.InlineDataAlt.Data)
			}
			if mime != "" && data != "" {
				return fmt.Sprintf("data:%s;base64,%s", mime, data)
			}
		}
	}
	return ""
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
		if blob, fileName, ok := decodeDataImageURI(ref); ok {
			part, err := w.CreateFormFile("input_reference", fileName)
			if err != nil {
				return "", err
			}
			if _, err := part.Write(blob); err != nil {
				return "", err
			}
		} else {
			_ = w.WriteField("input_reference", ref)
		}
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
		if strings.HasPrefix(path, "/v1beta/models/") {
			req.Header.Set("x-goog-api-key", c.key)
		}

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

func inputReferencesFromParams(params map[string]any) []string {
	if len(params) == 0 {
		return nil
	}
	refs := make([]string, 0, 2)
	if raw, ok := params["input_references"]; ok {
		switch v := raw.(type) {
		case []string:
			for _, item := range v {
				if item = strings.TrimSpace(item); item != "" {
					refs = append(refs, item)
				}
			}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					if s = strings.TrimSpace(s); s != "" {
						refs = append(refs, s)
					}
				}
			}
		}
	}
	if len(refs) == 0 {
		if ref, ok := params["input_reference"].(string); ok {
			if ref = strings.TrimSpace(ref); ref != "" {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

func decodeDataImageURI(raw string) ([]byte, string, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "data:image/") {
		return nil, "", false
	}
	comma := strings.IndexByte(raw, ',')
	if comma <= 0 {
		return nil, "", false
	}
	meta := strings.ToLower(raw[:comma])
	if !strings.Contains(meta, ";base64") {
		return nil, "", false
	}
	payload := raw[comma+1:]
	blob, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, "", false
	}
	fileName := "reference.jpg"
	switch {
	case strings.Contains(meta, "image/png"):
		fileName = "reference.png"
	case strings.Contains(meta, "image/webp"):
		fileName = "reference.webp"
	}
	return blob, fileName, true
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
