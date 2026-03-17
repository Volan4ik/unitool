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
