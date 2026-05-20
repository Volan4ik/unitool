package comet

import (
	"bytes"
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

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestBuildKlingVideoRequest(t *testing.T) {
	t.Run("text to video without references", func(t *testing.T) {
		path, payload := buildKlingVideoRequest("kling-v1-6", "move", nil)
		if path != "/kling/v1/videos/text2video" {
			t.Fatalf("unexpected path: %q", path)
		}
		if payload.Prompt != "move" || payload.ModelName != "kling-v1-6" || payload.Duration != "5" {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		if payload.Image != "" || len(payload.ImageList) != 0 {
			t.Fatalf("unexpected refs in payload: %+v", payload)
		}
	})

	t.Run("image to video with one reference", func(t *testing.T) {
		path, payload := buildKlingVideoRequest("kling-v1-6", "move", []string{"data:image/png;base64,QUJD"})
		if path != "/kling/v1/videos/image2video" {
			t.Fatalf("unexpected path: %q", path)
		}
		if payload.Image != "QUJD" {
			t.Fatalf("unexpected image: %q", payload.Image)
		}
		if len(payload.ImageList) != 0 {
			t.Fatalf("image list must be empty: %+v", payload.ImageList)
		}
	})

	t.Run("multi image to video with multiple references", func(t *testing.T) {
		path, payload := buildKlingVideoRequest("kling-v1-6", "move", []string{
			" first ",
			"data:image/jpeg;base64,U0VDT05E",
			"third",
			"fourth",
			"fifth",
		})
		if path != "/kling/v1/videos/multi-image2video" {
			t.Fatalf("unexpected path: %q", path)
		}
		if payload.Image != "" {
			t.Fatalf("single image must be empty: %q", payload.Image)
		}
		if len(payload.ImageList) != 4 {
			t.Fatalf("unexpected image list: %+v", payload.ImageList)
		}
		if payload.ImageList[0].Image != "first" || payload.ImageList[1].Image != "U0VDT05E" {
			t.Fatalf("unexpected normalized refs: %+v", payload.ImageList)
		}
		if payload.ImageList[3].Image != "fourth" {
			t.Fatalf("expected provider-side cap at four refs: %+v", payload.ImageList)
		}
	})
}

func TestInputReferencesFromParams(t *testing.T) {
	refs := inputReferencesFromParams(map[string]any{
		"input_reference":  "fallback",
		"input_references": []string{" one ", "", "two"},
	})
	if len(refs) != 2 || refs[0] != "one" || refs[1] != "two" {
		t.Fatalf("unexpected refs: %+v", refs)
	}

	refs = inputReferencesFromParams(map[string]any{"input_reference": " single "})
	if len(refs) != 1 || refs[0] != "single" {
		t.Fatalf("unexpected fallback refs: %+v", refs)
	}
}

func TestGenerateDoubaoSeedanceVideoUsesVideosEndpoint(t *testing.T) {
	c := New("https://api.test", "test-key", time.Second)
	c.httpc = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/videos":
			if err := r.ParseMultipartForm(16 << 20); err != nil {
				t.Fatalf("parse multipart: %v", err)
			}
			if got := r.MultipartForm.Value["model"]; len(got) != 1 || got[0] != "doubao-seedance-2-0" {
				t.Fatalf("unexpected model field: %+v", got)
			}
			if got := r.MultipartForm.Value["prompt"]; len(got) != 1 || got[0] != "pan right" {
				t.Fatalf("unexpected prompt field: %+v", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"id":"video-1","status":"queued"}`)),
				Header:     make(http.Header),
			}, nil
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/video-1":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"id":"video-1","status":"completed","url":"https://example.com/out.mp4"}`)),
				Header:     make(http.Header),
			}, nil
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			return nil, nil
		}
	})}

	got, err := c.Generate(context.Background(), provider.ModelRequest{
		Model:  "doubao-seedance-2-0",
		Input:  "pan right",
		Params: map[string]any{"kind": "video"},
	})
	if err != nil {
		t.Fatalf("unexpected err=%v", err)
	}
	if got.Output != "https://example.com/out.mp4" {
		t.Fatalf("unexpected output: %q", got.Output)
	}
	if got.Meta["model"] != "doubao-seedance-2-0" || got.Meta["id"] != "video-1" {
		t.Fatalf("unexpected meta: %+v", got.Meta)
	}
}

func TestGenerateImageDefaultsToGPTImage2ImagesEndpoint(t *testing.T) {
	c := New("https://api.test", "test-key", time.Second)
	c.httpc = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var payload imgReq
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.Model != "gpt-image-2" {
			t.Fatalf("unexpected model: %q", payload.Model)
		}
		if payload.Prompt != "draw a poster" {
			t.Fatalf("unexpected prompt: %q", payload.Prompt)
		}
		if payload.N != 1 || payload.Quality != "low" || payload.Size != "1024x1024" || payload.OutputFormat != "jpeg" {
			t.Fatalf("unexpected GPT image controls: %+v", payload)
		}
		body := []byte(`{
			"created": 1777935055,
			"output_format": "jpeg",
			"usage": {"total_tokens": 224},
			"data": [{"b64_json": "QUJD"}]
		}`)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    r,
		}, nil
	})}

	got, err := c.Generate(context.Background(), provider.ModelRequest{
		Input:  "draw a poster",
		Params: map[string]any{"kind": "image"},
	})
	if err != nil {
		t.Fatalf("unexpected err=%v", err)
	}
	if got.Output != "data:image/jpeg;base64,QUJD" {
		t.Fatalf("unexpected output: %q", got.Output)
	}
	if got.Tokens != 224 {
		t.Fatalf("unexpected tokens: %d", got.Tokens)
	}
	if got.Meta["model"] != "gpt-image-2" || got.Meta["source"] != "images/generations:b64_json" {
		t.Fatalf("unexpected meta: %+v", got.Meta)
	}
}

func TestGenerateGPTImageWithReferenceUsesEditsEndpoint(t *testing.T) {
	c := New("https://api.test", "test-key", time.Second)
	c.httpc = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/images/edits" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if got := r.MultipartForm.Value["model"]; len(got) != 1 || got[0] != "gpt-image-2" {
			t.Fatalf("unexpected model field: %+v", got)
		}
		if got := r.MultipartForm.Value["prompt"]; len(got) != 1 || got[0] != "add a red ribbon" {
			t.Fatalf("unexpected prompt field: %+v", got)
		}
		if got := r.MultipartForm.Value["output_format"]; len(got) != 1 || got[0] != "jpeg" {
			t.Fatalf("unexpected output_format field: %+v", got)
		}
		files := r.MultipartForm.File["image"]
		if len(files) != 1 {
			t.Fatalf("unexpected image files: %+v", r.MultipartForm.File)
		}
		f, err := files[0].Open()
		if err != nil {
			t.Fatalf("open uploaded file: %v", err)
		}
		defer f.Close()
		blob, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("read uploaded file: %v", err)
		}
		if string(blob) != "ABC" {
			t.Fatalf("unexpected uploaded bytes: %q", string(blob))
		}
		body := []byte(`{
			"created": 1777935055,
			"output_format": "jpeg",
			"usage": {"total_tokens": 981},
			"data": [{"b64_json": "RURJVA=="}]
		}`)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    r,
		}, nil
	})}

	got, err := c.Generate(context.Background(), provider.ModelRequest{
		Input: "add a red ribbon",
		Model: "gpt-image-2",
		Params: map[string]any{
			"kind":              "image",
			"input_references":  []string{"data:image/png;base64,QUJD"},
			"unrelated_setting": "ignored",
		},
	})
	if err != nil {
		t.Fatalf("unexpected err=%v", err)
	}
	if got.Output != "data:image/jpeg;base64,RURJVA==" {
		t.Fatalf("unexpected output: %q", got.Output)
	}
	if got.Tokens != 981 {
		t.Fatalf("unexpected tokens: %d", got.Tokens)
	}
	if got.Meta["model"] != "gpt-image-2" || got.Meta["source"] != "images/edits:b64_json" || got.Meta["references"] != 1 {
		t.Fatalf("unexpected meta: %+v", got.Meta)
	}
}

func TestGenerateImageViaImagesEndpointKeepsURLResponses(t *testing.T) {
	c := New("https://api.test", "test-key", time.Second)
	c.httpc = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if _, ok := raw["output_format"]; ok {
			t.Fatalf("imagen request must not include GPT output_format: %+v", raw)
		}
		body := []byte(`{"created":1777935055,"data":[{"url":"https://example.com/image.png"}]}`)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    r,
		}, nil
	})}

	got, err := c.generateImage(context.Background(), "imagen-4", "draw", nil)
	if err != nil {
		t.Fatalf("unexpected err=%v", err)
	}
	if !strings.HasPrefix(got.Output, "https://example.com/image.png") {
		t.Fatalf("unexpected output: %q", got.Output)
	}
	if got.Meta["source"] != "images/generations" {
		t.Fatalf("unexpected meta: %+v", got.Meta)
	}
}

func TestGetKlingVideoStatusAllowsInProgressWithoutTopLevelCode(t *testing.T) {
	c := New("https://api.test", "test-key", time.Second)
	c.httpc = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/kling/v1/videos/multi-image2video/task-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body := []byte(`{
			"task_id": "task-1",
			"task_info": {},
			"created_at": 1777935055866,
			"updated_at": 1777935055866,
			"task_status": "submitted"
		}`)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    r,
		}, nil
	})}

	got, err := c.getKlingVideoStatus(context.Background(), "/kling/v1/videos/multi-image2video", "task-1")
	if err != nil {
		t.Fatalf("unexpected err=%v", err)
	}
	if status := klingTaskStatus(got); status != "submitted" {
		t.Fatalf("unexpected status: %q", status)
	}
}

func TestKlingFlatResponseHelpers(t *testing.T) {
	var out klingVideoTaskEnvelope
	raw := []byte(`{
		"task_id": "task-1",
		"task_status": "succeed",
		"task_result": {
			"videos": [
				{"url": "https://example.com/video.mp4"}
			]
		}
	}`)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := klingTaskID(out); got != "task-1" {
		t.Fatalf("unexpected task id: %q", got)
	}
	if got := klingTaskStatus(out); got != "succeed" {
		t.Fatalf("unexpected status: %q", got)
	}
	if got := firstKlingVideoURL(out); got != "https://example.com/video.mp4" {
		t.Fatalf("unexpected video url: %q", got)
	}
	if err := validateKlingStatusEnvelope(out, raw); err != nil {
		t.Fatalf("unexpected validation err=%v", err)
	}
}

func TestValidateKlingStatusEnvelopeRejectsNonSuccessCode(t *testing.T) {
	var out klingVideoTaskEnvelope
	out.Code = "1001"
	out.Message = "bad request"
	if err := validateKlingStatusEnvelope(out, []byte(`{}`)); err == nil || err.Error() != "comet kling status error: bad request" {
		t.Fatalf("unexpected err=%v", err)
	}
}
