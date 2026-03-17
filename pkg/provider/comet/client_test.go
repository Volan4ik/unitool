package comet

import (
	"context"
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
