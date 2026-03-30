package rate

import (
	"testing"
	"time"
)

func TestLimiterAllowsAndRefills(t *testing.T) {
	l := New(2, 1, 50*time.Millisecond)

	if !l.Allow() {
		t.Fatal("expected first token")
	}
	if !l.Allow() {
		t.Fatal("expected second token")
	}
	if l.Allow() {
		t.Fatal("expected limiter to deny when tokens exhausted")
	}

	time.Sleep(60 * time.Millisecond)
	if !l.Allow() {
		t.Fatal("expected refill after interval")
	}
}

func TestLimiterAllowKindFallsBackAndSupportsPerKindBuckets(t *testing.T) {
	l := NewByKind(map[string]Config{
		"":      {Max: 1, Refill: 1, Interval: time.Hour},
		"image": {Max: 0, Refill: 0, Interval: time.Hour},
	})

	if !l.AllowKind("text") {
		t.Fatal("expected text kind to use default bucket")
	}
	if l.AllowKind("text") {
		t.Fatal("expected default bucket to be exhausted")
	}
	if l.AllowKind("image") {
		t.Fatal("expected image-specific bucket to deny immediately")
	}
}
