package comet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"unitool/pkg/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestTrimRightSlash(t *testing.T) {
	if got := trimRightSlash("https://api.example.com///"); got != "https://api.example.com" {
		t.Fatalf("unexpected trimmed value: %q", got)
	}
	if got := trimRightSlash(""); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestLabelFor(t *testing.T) {
	if got := labelFor("/v1/chat/completions"); got != "chat" {
		t.Fatalf("expected chat, got %q", got)
	}
	if got := labelFor("/v1/images/generations"); got != "images" {
		t.Fatalf("expected images, got %q", got)
	}
	if got := labelFor("/v1/videos/generations"); got != "videos" {
		t.Fatalf("expected videos, got %q", got)
	}
}

func TestCapBackoff(t *testing.T) {
	if got := capBackoff(7 * time.Second); got != 5*time.Second {
		t.Fatalf("expected cap to 5s, got %s", got)
	}
	if got := capBackoff(2 * time.Second); got != 2*time.Second {
		t.Fatalf("unexpected backoff: %s", got)
	}
}

func TestComputeBackoffRetryAfterSeconds(t *testing.T) {
	c := New("https://api.example.com", "key", time.Second)
	if got := c.computeBackoff(0, "3"); got != 3*time.Second {
		t.Fatalf("expected 3s from retry-after, got %s", got)
	}
}

func TestGenerateUnsupportedKind(t *testing.T) {
	c := New("https://api.example.com", "key", time.Second)
	_, err := c.Generate(context.Background(), provider.ModelRequest{
		Input:  "hello",
		Model:  "gpt-4o",
		Params: map[string]any{"kind": "unknown"},
	})
	if err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}

func TestPostJSONEmptyKey(t *testing.T) {
	c := New("https://api.example.com", "", time.Second)
	err := c.postJSON(context.Background(), "/v1/chat/completions", map[string]any{"a": 1}, nil)
	if err == nil {
		t.Fatal("expected error when api key is empty")
	}
	if !strings.Contains(err.Error(), "api key is empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizeVideoModel(t *testing.T) {
	if got := normalizeVideoModel(""); got != "sora-2" {
		t.Fatalf("expected default sora-2, got %q", got)
	}
	if got := normalizeVideoModel("Kling"); got != "kling-v1-6" {
		t.Fatalf("expected kling-v1-6, got %q", got)
	}
	if got := normalizeVideoModel("kling-v1.6"); got != "kling-v1-6" {
		t.Fatalf("expected kling-v1-6, got %q", got)
	}
	if got := normalizeVideoModel("Veo 3"); got != "veo3" {
		t.Fatalf("expected veo3, got %q", got)
	}
	if !isSupportedVideoModel("kling") {
		t.Fatal("expected kling to be supported")
	}
	if !isSupportedVideoModel("veo3") {
		t.Fatal("expected veo3 to be supported")
	}
}

func TestShouldUseImagesEndpoint(t *testing.T) {
	if !shouldUseImagesEndpoint("imagen-3") {
		t.Fatal("expected imagen-3 to use images endpoint")
	}
	if !shouldUseImagesEndpoint("IMAGEN-4-ultra") {
		t.Fatal("expected imagen prefix check to be case-insensitive")
	}
	if shouldUseImagesEndpoint("gpt-4o-image") {
		t.Fatal("did not expect gpt-4o-image to use images endpoint")
	}
	if shouldUseImagesEndpoint("gemini-3.1-flash-image-preview") {
		t.Fatal("did not expect preview chat image model to use images endpoint")
	}
}

func TestExtractFirstHTTPURL(t *testing.T) {
	in := "Result: https://cdn.example.com/out.png, done"
	if got := extractFirstHTTPURL(in); got != "https://cdn.example.com/out.png" {
		t.Fatalf("unexpected extracted url: %q", got)
	}
	if got := extractFirstHTTPURL("no url here"); got != "" {
		t.Fatalf("expected empty url, got %q", got)
	}
}

func TestExtractMessageContentFromArrayShape(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"text","text":"Here is your image"},
		{"type":"image_url","image_url":{"url":"https://cdn.example.com/generated.png"}}
	]`)

	text := extractMessageContentText(raw)
	if !strings.Contains(text, "Here is your image") {
		t.Fatalf("unexpected extracted text: %q", text)
	}

	url := extractMessageContentFirstURL(raw)
	if url != "https://cdn.example.com/generated.png" {
		t.Fatalf("unexpected extracted url: %q", url)
	}
}

func TestExtractFirstImageReferenceDataURI(t *testing.T) {
	in := "![image](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB)"
	got := extractFirstImageReference(in)
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("expected data uri, got %q", got)
	}
}

func TestGenerateVideoKlingTextUsesKlingTextEndpoint(t *testing.T) {
	t.Helper()

	var createSeen bool
	var pollSeen bool
	c := New("https://api.example.com", "key", time.Second)
	c.httpc = &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/kling/v1/videos/text2video":
				createSeen = true
				var req map[string]any
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decode create body: %v", err)
				}
				if got := req["model_name"]; got != "kling-v1-6" {
					t.Fatalf("unexpected model_name: %#v", got)
				}
				if got := req["duration"]; got != "5" {
					t.Fatalf("unexpected duration: %#v", got)
				}
				if got := req["prompt"]; got != "A cat walks on the beach" {
					t.Fatalf("unexpected prompt: %#v", got)
				}
				if _, ok := req["image"]; ok {
					t.Fatalf("did not expect image in text2video request: %#v", req)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"success","data":{"task_id":"task-text-1","task_status":"submitted","created_at":1,"updated_at":1}}`)),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/kling/v1/videos/text2video/task-text-1":
				pollSeen = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"success","data":{"task_id":"task-text-1","task_status":"succeed","task_result":{"videos":[{"url":"https://cdn.example.com/kling-text.mp4"}]},"created_at":1,"updated_at":2}}`)),
				}, nil
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				return nil, nil
			}
		}),
	}

	resp, err := c.generateVideo(context.Background(), "Kling", "A cat walks on the beach", "")
	if err != nil {
		t.Fatalf("generateVideo returned error: %v", err)
	}
	if resp.Output != "https://cdn.example.com/kling-text.mp4" {
		t.Fatalf("unexpected output: %q", resp.Output)
	}
	if !createSeen || !pollSeen {
		t.Fatalf("expected create and poll requests, create=%v poll=%v", createSeen, pollSeen)
	}
}

func TestGenerateVideoKlingImageUsesKlingImageEndpoint(t *testing.T) {
	t.Helper()

	const dataURI = "data:image/png;base64,aGVsbG8="

	var createSeen bool
	var pollSeen bool
	c := New("https://api.example.com", "key", time.Second)
	c.httpc = &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/kling/v1/videos/image2video":
				createSeen = true
				var req map[string]any
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decode create body: %v", err)
				}
				if got := req["model_name"]; got != "kling-v1" {
					t.Fatalf("unexpected model_name: %#v", got)
				}
				if got := req["duration"]; got != "5" {
					t.Fatalf("unexpected duration: %#v", got)
				}
				if got := req["image"]; got != "aGVsbG8=" {
					t.Fatalf("expected stripped base64 payload, got %#v", got)
				}
				if got := req["prompt"]; got != "Animate this photo" {
					t.Fatalf("unexpected prompt: %#v", got)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"success","data":{"task_id":"task-image-1","task_status":"submitted","created_at":1,"updated_at":1}}`)),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/kling/v1/videos/image2video/task-image-1":
				pollSeen = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"success","data":{"task_id":"task-image-1","task_status":"succeed","task_result":{"videos":[{"url":"https://cdn.example.com/kling-image.mp4"}]},"created_at":1,"updated_at":2}}`)),
				}, nil
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				return nil, nil
			}
		}),
	}

	resp, err := c.generateVideo(context.Background(), "kling-v1", "Animate this photo", dataURI)
	if err != nil {
		t.Fatalf("generateVideo returned error: %v", err)
	}
	if resp.Output != "https://cdn.example.com/kling-image.mp4" {
		t.Fatalf("unexpected output: %q", resp.Output)
	}
	if !createSeen || !pollSeen {
		t.Fatalf("expected create and poll requests, create=%v poll=%v", createSeen, pollSeen)
	}
}

func TestGenerateVideoKlingTextSupportsStatusResponseWithStringCode(t *testing.T) {
	t.Helper()

	var createSeen bool
	var pollCalls int
	c := New("https://api.example.com", "key", time.Second)
	c.httpc = &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/kling/v1/videos/text2video":
				createSeen = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"success","data":{"task_id":"task-text-string-code","task_status":"submitted","created_at":1,"updated_at":1}}`)),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/kling/v1/videos/text2video/task-text-string-code":
				pollCalls++
				if pollCalls == 1 {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"code":"success","message":"","data":{"task_id":"task-text-string-code","action":"TEXT2VIDEO","status":"NOT_START","fail_reason":"","progress":"0"}}`)),
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":"success","message":"","data":{"task_id":"task-text-string-code","action":"TEXT2VIDEO","status":"SUCCEED","fail_reason":"","task_result":{"videos":[{"url":"https://cdn.example.com/kling-text-string-code.mp4"}]}}}`)),
				}, nil
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				return nil, nil
			}
		}),
	}

	resp, err := c.generateVideo(context.Background(), "Kling", "A robot waves", "")
	if err != nil {
		t.Fatalf("generateVideo returned error: %v", err)
	}
	if resp.Output != "https://cdn.example.com/kling-text-string-code.mp4" {
		t.Fatalf("unexpected output: %q", resp.Output)
	}
	if !createSeen {
		t.Fatal("expected create request to be sent")
	}
	if pollCalls != 2 {
		t.Fatalf("expected two poll requests, got %d", pollCalls)
	}
}

func TestGenerateVideoKlingTextUsesResultURLFromNestedSuccessPayload(t *testing.T) {
	t.Helper()

	var createSeen bool
	var pollSeen bool
	c := New("https://api.example.com", "key", time.Second)
	c.httpc = &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/kling/v1/videos/text2video":
				createSeen = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"success","data":{"task_id":"task-text-result-url","task_status":"submitted","created_at":1,"updated_at":1}}`)),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/kling/v1/videos/text2video/task-text-result-url":
				pollSeen = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":"success","message":"","data":{"task_id":"task-text-result-url","action":"TEXT2VIDEO","status":"SUCCESS","fail_reason":"","result_url":"https://cdn.example.com/from-result-url.mp4","submit_time":1,"start_time":2,"finish_time":3,"progress":"100%","data":{"code":0,"data":{"task_id":"task-text-result-url","task_info":{},"created_at":1,"updated_at":2,"task_result":{"images":[],"videos":[{"id":"vid-1","url":"https://cdn.example.com/from-nested-task-result.mp4","duration":"5"}]},"task_status":"succeed"},"message":"success","request_id":""}}}`)),
				}, nil
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				return nil, nil
			}
		}),
	}

	resp, err := c.generateVideo(context.Background(), "Kling", "A robot waves", "")
	if err != nil {
		t.Fatalf("generateVideo returned error: %v", err)
	}
	if resp.Output != "https://cdn.example.com/from-result-url.mp4" {
		t.Fatalf("unexpected output: %q", resp.Output)
	}
	if !createSeen || !pollSeen {
		t.Fatalf("expected create and poll requests, create=%v poll=%v", createSeen, pollSeen)
	}
}

func TestGenerateImageGeminiUsesGenerateContentEndpoint(t *testing.T) {
	t.Helper()

	var seen bool
	c := New("https://api.example.com", "key", time.Second)
	c.httpc = &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost || r.URL.Path != "/v1beta/models/gemini-3.1-flash-image-preview:generateContent" {
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			seen = true
			if got := r.Header.Get("x-goog-api-key"); got != "key" {
				t.Fatalf("expected x-goog-api-key header, got %q", got)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer key" {
				t.Fatalf("expected bearer auth, got %q", got)
			}

			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}

			contents, ok := req["contents"].([]any)
			if !ok || len(contents) != 1 {
				t.Fatalf("unexpected contents: %#v", req["contents"])
			}
			first, ok := contents[0].(map[string]any)
			if !ok {
				t.Fatalf("unexpected first content: %#v", contents[0])
			}
			if got := first["role"]; got != "user" {
				t.Fatalf("unexpected role: %#v", got)
			}
			parts, ok := first["parts"].([]any)
			if !ok || len(parts) != 1 {
				t.Fatalf("unexpected parts: %#v", first["parts"])
			}
			part, ok := parts[0].(map[string]any)
			if !ok || part["text"] != "Sunset over ocean" {
				t.Fatalf("unexpected part: %#v", parts[0])
			}

			cfg, ok := req["generationConfig"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected generationConfig: %#v", req["generationConfig"])
			}
			mods, ok := cfg["responseModalities"].([]any)
			if !ok || len(mods) != 1 || mods[0] != "IMAGE" {
				t.Fatalf("unexpected responseModalities: %#v", cfg["responseModalities"])
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"role":"model","parts":[{"text":"done"},{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]}}],"usageMetadata":{"totalTokenCount":77}}`)),
			}, nil
		}),
	}

	resp, err := c.generateImage(context.Background(), "gemini-3.1-flash-image-preview", "Sunset over ocean", nil)
	if err != nil {
		t.Fatalf("generateImage returned error: %v", err)
	}
	if resp.Output != "data:image/png;base64,aGVsbG8=" {
		t.Fatalf("unexpected output: %q", resp.Output)
	}
	if resp.Tokens != 77 {
		t.Fatalf("unexpected tokens: %d", resp.Tokens)
	}
	if !seen {
		t.Fatal("expected gemini generateContent request")
	}
}

func TestGenerateImageGeminiWithRefsUsesInlineData(t *testing.T) {
	t.Helper()

	const ref1 = "data:image/jpeg;base64,Zm9v"
	const ref2 = "data:image/png;base64,YmFy"

	var seen bool
	c := New("https://api.example.com", "key", time.Second)
	c.httpc = &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost || r.URL.Path != "/v1beta/models/gemini-3.1-flash-image-preview:generateContent" {
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			seen = true

			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}

			contents := req["contents"].([]any)
			first := contents[0].(map[string]any)
			parts := first["parts"].([]any)
			if len(parts) != 3 {
				t.Fatalf("expected text + 2 refs, got %#v", parts)
			}
			if got := parts[0].(map[string]any)["text"]; got != "Blend these into one scene" {
				t.Fatalf("unexpected prompt part: %#v", got)
			}

			inline1 := parts[1].(map[string]any)["inline_data"].(map[string]any)
			if got := inline1["mime_type"]; got != "image/jpeg" {
				t.Fatalf("unexpected ref1 mime: %#v", got)
			}
			if got := inline1["data"]; got != "Zm9v" {
				t.Fatalf("unexpected ref1 data: %#v", got)
			}

			inline2 := parts[2].(map[string]any)["inline_data"].(map[string]any)
			if got := inline2["mime_type"]; got != "image/png" {
				t.Fatalf("unexpected ref2 mime: %#v", got)
			}
			if got := inline2["data"]; got != "YmFy" {
				t.Fatalf("unexpected ref2 data: %#v", got)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"c2NlbmU="}}]}}],"usageMetadata":{"totalTokenCount":19}}`)),
			}, nil
		}),
	}

	resp, err := c.generateImage(context.Background(), "gemini-3.1-flash-image-preview", "Blend these into one scene", []string{ref1, ref2})
	if err != nil {
		t.Fatalf("generateImage returned error: %v", err)
	}
	if resp.Output != "data:image/png;base64,c2NlbmU=" {
		t.Fatalf("unexpected output: %q", resp.Output)
	}
	if !seen {
		t.Fatal("expected gemini generateContent request")
	}
}

func TestGenerateImageKlingUsesKlingEndpoint(t *testing.T) {
	t.Helper()

	var createSeen bool
	var pollSeen bool

	c := New("https://api.example.com", "key", time.Second)
	c.httpc = &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/kling/v1/images/generations":
				createSeen = true
				var req map[string]any
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decode create body: %v", err)
				}
				if got := req["model_name"]; got != "kling-v2" {
					t.Fatalf("unexpected model_name: %#v", got)
				}
				if got := req["prompt"]; got != "A glass teapot in snow" {
					t.Fatalf("unexpected prompt: %#v", got)
				}
				if _, ok := req["subject_image_list"]; ok {
					t.Fatalf("did not expect subject_image_list in text-only request: %#v", req)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"success","data":{"task_id":"kling-image-1","task_status":"submitted","created_at":1,"updated_at":1}}`)),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/kling/v1/images/generations/kling-image-1":
				pollSeen = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"success","data":{"task_id":"kling-image-1","task_status":"succeed","task_result":{"images":[{"url":"https://cdn.example.com/kling-image.png"}]},"created_at":1,"updated_at":2}}`)),
				}, nil
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				return nil, nil
			}
		}),
	}

	resp, err := c.generateImage(context.Background(), "kling-v2", "A glass teapot in snow", nil)
	if err != nil {
		t.Fatalf("generateImage returned error: %v", err)
	}
	if resp.Output != "https://cdn.example.com/kling-image.png" {
		t.Fatalf("unexpected output: %q", resp.Output)
	}
	if !createSeen || !pollSeen {
		t.Fatalf("expected create and poll requests, create=%v poll=%v", createSeen, pollSeen)
	}
}

func TestGenerateImageKlingWithMultipleRefsUsesSubjectImageList(t *testing.T) {
	t.Helper()

	const ref1 = "data:image/jpeg;base64,Zm9v"
	const ref2 = "https://cdn.example.com/ref-2.png"

	var createSeen bool
	var pollSeen bool

	c := New("https://api.example.com", "key", time.Second)
	c.httpc = &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/kling/v1/images/generations":
				createSeen = true
				var req map[string]any
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decode create body: %v", err)
				}
				if _, ok := req["image"]; ok {
					t.Fatalf("did not expect single image field when multiple refs are provided: %#v", req)
				}
				items, ok := req["subject_image_list"].([]any)
				if !ok || len(items) != 2 {
					t.Fatalf("unexpected subject_image_list: %#v", req["subject_image_list"])
				}
				first := items[0].(map[string]any)
				second := items[1].(map[string]any)
				if got := first["subject_image"]; got != "Zm9v" {
					t.Fatalf("expected first ref stripped to base64, got %#v", got)
				}
				if got := second["subject_image"]; got != "https://cdn.example.com/ref-2.png" {
					t.Fatalf("unexpected second ref: %#v", got)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"success","data":{"task_id":"kling-image-2","task_status":"submitted","created_at":1,"updated_at":1}}`)),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/kling/v1/images/generations/kling-image-2":
				pollSeen = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"success","data":{"task_id":"kling-image-2","task_status":"succeed","task_result":{"url":"https://cdn.example.com/kling-image-2.png"},"created_at":1,"updated_at":2}}`)),
				}, nil
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				return nil, nil
			}
		}),
	}

	resp, err := c.generateImage(context.Background(), "kling-v2", "Blend subjects", []string{ref1, ref2})
	if err != nil {
		t.Fatalf("generateImage returned error: %v", err)
	}
	if resp.Output != "https://cdn.example.com/kling-image-2.png" {
		t.Fatalf("unexpected output: %q", resp.Output)
	}
	if !createSeen || !pollSeen {
		t.Fatalf("expected create and poll requests, create=%v poll=%v", createSeen, pollSeen)
	}
}

func TestGenerateImageKlingUsesResultURLFromNestedSuccessPayload(t *testing.T) {
	t.Helper()

	c := New("https://api.example.com", "key", time.Second)
	c.httpc = &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/kling/v1/images/generations":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"success","data":{"task_id":"kling-image-result-url","task_status":"submitted","created_at":1,"updated_at":1}}`)),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/kling/v1/images/generations/kling-image-result-url":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":"success","message":"","data":{"task_id":"kling-image-result-url","action":"IMAGE","status":"SUCCESS","fail_reason":"","result_url":"https://cdn.example.com/kling-image-result.png?cacheKey=abc\u0026x=1","submit_time":1,"start_time":2,"finish_time":3,"progress":"100%","data":{"code":0,"data":{"task_id":"kling-image-result-url","task_info":{},"created_at":1,"updated_at":2,"task_result":{"images":[{"url":"https://cdn.example.com/kling-image-from-task-result.png"}]},"task_status":"succeed"},"message":"success","request_id":""}}}`)),
				}, nil
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				return nil, nil
			}
		}),
	}

	resp, err := c.generateImage(context.Background(), "kling-v2", "A blue ceramic cup", nil)
	if err != nil {
		t.Fatalf("generateImage returned error: %v", err)
	}
	if resp.Output != "https://cdn.example.com/kling-image-result.png?cacheKey=abc&x=1" {
		t.Fatalf("unexpected output: %q", resp.Output)
	}
}

func TestGenerateImageKlingUsesRawURLFallbackForDeeplyNestedSuccessPayload(t *testing.T) {
	t.Helper()

	c := New("https://api.example.com", "key", time.Second)
	c.httpc = &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/kling/v1/images/generations":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":0,"message":"success","data":{"task_id":"kling-image-deep-nested","task_status":"submitted","created_at":1,"updated_at":1}}`)),
				}, nil
			case r.Method == http.MethodGet && r.URL.Path == "/kling/v1/images/generations/kling-image-deep-nested":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"code":"success","message":"","data":{"task_id":"kling-image-deep-nested","status":"SUCCESS","data":{"code":0,"data":{"data":{"task_id":"kling-image-deep-nested","task_status":"succeed","task_result":{"images":[{"url":"https://cdn.example.com/kling-image-deep-nested.png"}]}}}}}}`)),
				}, nil
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				return nil, nil
			}
		}),
	}

	resp, err := c.generateImage(context.Background(), "kling-v2", "A mountain lake at dawn", nil)
	if err != nil {
		t.Fatalf("generateImage returned error: %v", err)
	}
	if resp.Output != "https://cdn.example.com/kling-image-deep-nested.png" {
		t.Fatalf("unexpected output: %q", resp.Output)
	}
}
