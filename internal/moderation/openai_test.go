package moderation

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCheckPromptAllowed(t *testing.T) {
	c := NewOpenAIClient("https://api.openai.com", "test-key", "omni-moderation-latest", 2*time.Second)
	c.httpc = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			if r.URL.Path != "/v1/moderations" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Fatalf("unexpected auth header: %s", got)
			}
			return jsonResponse(http.StatusOK, `{"results":[{"flagged":false,"categories":{"sexual":false}}]}`), nil
		}),
	}
	res, err := c.CheckPrompt(context.Background(), "a safe prompt")
	if err != nil {
		t.Fatalf("CheckPrompt returned error: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("expected allowed=true, got false with reasons: %v", res.Reasons)
	}
}

func TestCheckPromptBlockedBySexual(t *testing.T) {
	c := NewOpenAIClient("https://api.openai.com", "test-key", "omni-moderation-latest", 2*time.Second)
	c.httpc = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"results":[{"flagged":true,"categories":{"sexual":true}}]}`), nil
		}),
	}
	res, err := c.CheckPrompt(context.Background(), "blocked")
	if err != nil {
		t.Fatalf("CheckPrompt returned error: %v", err)
	}
	if res.Allowed {
		t.Fatalf("expected allowed=false, got true")
	}
	if len(res.Reasons) == 0 || res.Reasons[0] != "sexual" {
		t.Fatalf("expected sexual reason, got %v", res.Reasons)
	}
}

func TestCheckPromptBlockedByFlaggedFallback(t *testing.T) {
	c := NewOpenAIClient("https://api.openai.com", "test-key", "omni-moderation-latest", 2*time.Second)
	c.httpc = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"results":[{"flagged":true,"categories":{"violence":true}}]}`), nil
		}),
	}
	res, err := c.CheckPrompt(context.Background(), "blocked")
	if err != nil {
		t.Fatalf("CheckPrompt returned error: %v", err)
	}
	if res.Allowed {
		t.Fatalf("expected allowed=false, got true")
	}
	if len(res.Reasons) == 0 || res.Reasons[0] != "flagged" {
		t.Fatalf("expected flagged fallback, got %v", res.Reasons)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
