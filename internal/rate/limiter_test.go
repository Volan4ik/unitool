package rate

import (
	"testing"
	"time"
)

func TestLimiterAllowAndRefill(t *testing.T) {
	l := New(1, 1, 20*time.Millisecond)
	if !l.Allow() {
		t.Fatal("first request must pass")
	}
	if l.Allow() {
		t.Fatal("second request must be limited before refill")
	}
	time.Sleep(30 * time.Millisecond)
	if !l.Allow() {
		t.Fatal("request must pass after refill interval")
	}
}

func TestLimiterByKindAndFallback(t *testing.T) {
	l := NewByKind(map[string]Config{
		"":      {Max: 2, Refill: 0, Interval: time.Second},
		"image": {Max: 1, Refill: 0, Interval: time.Second},
	})

	if !l.AllowKind("image") {
		t.Fatal("image request #1 must pass")
	}
	if l.AllowKind("image") {
		t.Fatal("image request #2 must fail")
	}

	// Unknown kind should fallback to default bucket.
	if !l.AllowKind("unknown") || !l.AllowKind("unknown") {
		t.Fatal("default bucket should allow first two requests")
	}
	if l.AllowKind("unknown") {
		t.Fatal("default bucket should reject when exhausted")
	}
}

func TestBucketForKindAndMin(t *testing.T) {
	l := NewByKind(map[string]Config{
		"":      {Max: 1, Refill: 1, Interval: time.Second},
		"video": {Max: 3, Refill: 1, Interval: time.Second},
	})
	if b := l.bucketForKind("video"); b == nil || b.max != 3 {
		t.Fatalf("unexpected video bucket: %+v", b)
	}
	if b := l.bucketForKind("missing"); b == nil || b.max != 1 {
		t.Fatalf("unexpected fallback bucket: %+v", b)
	}

	if min(1, 2) != 1 || min(5, -1) != -1 {
		t.Fatal("min helper returned unexpected value")
	}
}
