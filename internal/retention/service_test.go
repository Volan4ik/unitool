package retention

import (
	"testing"
	"time"
)

func TestNextDailyRunUTC(t *testing.T) {
	now := time.Date(2026, 4, 11, 2, 0, 0, 0, time.UTC)
	run, err := nextDailyRunUTC(now, "04:10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := time.Date(2026, 4, 11, 4, 10, 0, 0, time.UTC); !run.Equal(want) {
		t.Fatalf("got %s want %s", run, want)
	}

	now = time.Date(2026, 4, 11, 5, 0, 0, 0, time.UTC)
	run, err = nextDailyRunUTC(now, "04:10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := time.Date(2026, 4, 12, 4, 10, 0, 0, time.UTC); !run.Equal(want) {
		t.Fatalf("got %s want %s", run, want)
	}
}

func TestNextDailyRunUTCInvalidTime(t *testing.T) {
	if _, err := nextDailyRunUTC(time.Now().UTC(), "bad"); err == nil {
		t.Fatal("expected parse error")
	}
}
