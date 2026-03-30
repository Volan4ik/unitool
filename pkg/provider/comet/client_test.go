package comet

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"unitool/pkg/provider"
)

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
	if got := normalizeVideoModel("Kling"); got != "kling" {
		t.Fatalf("expected kling, got %q", got)
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
