package moderation

import (
	"testing"
	"time"
)

func TestNewOpenAIClientDefaults(t *testing.T) {
	c := NewOpenAIClient("", "k", "", 0)
	if c.base != "https://api.openai.com" {
		t.Fatalf("unexpected base: %q", c.base)
	}
	if c.model != "omni-moderation-latest" {
		t.Fatalf("unexpected model: %q", c.model)
	}
	if c.httpc == nil || c.httpc.Timeout != 12*time.Second {
		t.Fatalf("unexpected timeout: %v", c.httpc)
	}

	c = NewOpenAIClient("https://example.com/", "k", "m", 5*time.Second)
	if c.base != "https://example.com" {
		t.Fatalf("unexpected trimmed base: %q", c.base)
	}
	if c.model != "m" {
		t.Fatalf("unexpected model: %q", c.model)
	}
}

func TestNSFWReasons(t *testing.T) {
	reasons := nsfwReasons(map[string]bool{
		"sexual":           true,
		"violence_graphic": true,
	}, false)
	if len(reasons) != 2 || reasons[0] != "sexual" || reasons[1] != "violence/graphic" {
		t.Fatalf("unexpected reasons: %#v", reasons)
	}

	reasons = nsfwReasons(map[string]bool{}, true)
	if len(reasons) != 1 || reasons[0] != "flagged" {
		t.Fatalf("unexpected fallback reasons: %#v", reasons)
	}
}
