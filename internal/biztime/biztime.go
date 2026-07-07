package biztime

import (
	"fmt"
	"strings"
	"time"
)

const DefaultLocationName = "Europe/Moscow"

func LoadLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = DefaultLocationName
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("load business timezone %q: %w", name, err)
	}
	return loc, nil
}

func DayBounds(now time.Time, loc *time.Location) (time.Time, time.Time) {
	if loc == nil {
		loc = time.UTC
	}
	localNow := now.In(loc)
	start := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1)
}
