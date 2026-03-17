package retention

import (
	"testing"
	"time"
)

func TestNextDailyRunUTCSameDay(t *testing.T) {
	now := time.Date(2026, 3, 17, 1, 0, 0, 0, time.UTC)
	next, err := nextDailyRunUTC(now, "04:10")
	if err != nil {
		t.Fatalf("nextDailyRunUTC error: %v", err)
	}
	want := time.Date(2026, 3, 17, 4, 10, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("want %s, got %s", want, next)
	}
}

func TestNextDailyRunUTCNextDay(t *testing.T) {
	now := time.Date(2026, 3, 17, 10, 0, 0, 0, time.UTC)
	next, err := nextDailyRunUTC(now, "04:10")
	if err != nil {
		t.Fatalf("nextDailyRunUTC error: %v", err)
	}
	want := time.Date(2026, 3, 18, 4, 10, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("want %s, got %s", want, next)
	}
}

func TestNextDailyRunUTCInvalidTime(t *testing.T) {
	now := time.Date(2026, 3, 17, 10, 0, 0, 0, time.UTC)
	if _, err := nextDailyRunUTC(now, "bad"); err == nil {
		t.Fatal("expected parse error")
	}
}
