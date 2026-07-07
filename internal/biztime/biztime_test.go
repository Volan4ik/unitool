package biztime

import (
	"testing"
	"time"
)

func TestDayBoundsUsesBusinessLocation(t *testing.T) {
	loc, err := LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	now := time.Date(2026, 6, 15, 22, 30, 0, 0, time.UTC)
	start, end := DayBounds(now, loc)

	wantStart := time.Date(2026, 6, 16, 0, 0, 0, 0, loc)
	wantEnd := time.Date(2026, 6, 17, 0, 0, 0, 0, loc)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("bounds=%s..%s want=%s..%s", start, end, wantStart, wantEnd)
	}
}

func TestLoadLocationDefaultsToBusinessTimezone(t *testing.T) {
	loc, err := LoadLocation(" ")
	if err != nil {
		t.Fatalf("load default location: %v", err)
	}
	if loc.String() != DefaultLocationName {
		t.Fatalf("location=%q want=%q", loc.String(), DefaultLocationName)
	}
}
