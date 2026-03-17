package weekly

import (
	"testing"
	"time"
)

func TestNextWeekRunSameWeek(t *testing.T) {
	now := time.Date(2026, 3, 16, 2, 0, 0, 0, time.UTC) // Mon
	next, err := nextWeekRun(now, "Mon 03:00")
	if err != nil {
		t.Fatalf("nextWeekRun error: %v", err)
	}
	want := time.Date(2026, 3, 16, 3, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("want %s, got %s", want, next)
	}
}

func TestNextWeekRunNextWeek(t *testing.T) {
	now := time.Date(2026, 3, 16, 4, 0, 0, 0, time.UTC) // Mon after target
	next, err := nextWeekRun(now, "Mon 03:00")
	if err != nil {
		t.Fatalf("nextWeekRun error: %v", err)
	}
	want := time.Date(2026, 3, 23, 3, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("want %s, got %s", want, next)
	}
}

func TestNextWeekRunInvalidSpec(t *testing.T) {
	now := time.Date(2026, 3, 16, 4, 0, 0, 0, time.UTC)
	if _, err := nextWeekRun(now, "Bad 03:00"); err == nil {
		t.Fatal("expected error for invalid day")
	}
	if _, err := nextWeekRun(now, "Mon bad"); err == nil {
		t.Fatal("expected error for invalid time")
	}
}
