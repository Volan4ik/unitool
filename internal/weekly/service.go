package weekly

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"unitool/internal/storage"
)

// Service grants free text generations each week.
type Service struct {
	pg         *storage.PG
	atDayHHMM  string // UTC, format: "Mon 03:00"
	textAmount int
}

func NewService(pg *storage.PG, atDayHHMM string, textAmount int) *Service {
	return &Service{pg: pg, atDayHHMM: atDayHHMM, textAmount: textAmount}
}

func (s *Service) Start(ctx context.Context) {
	go func() {
		if err := s.applyWeeklyText(context.Background()); err != nil {
			log.Printf("weekly: initial apply failed: %v", err)
		}
		s.loop(ctx)
	}()
}

func (s *Service) loop(ctx context.Context) {
	for {
		next, err := nextWeekRun(time.Now().UTC(), s.atDayHHMM)
		if err != nil {
			log.Printf("weekly: parse schedule failed: %v", err)
			next, _ = nextWeekRun(time.Now().UTC(), "Mon 03:00")
		}
		delay := time.Until(next)
		if delay < 0 {
			delay = 0
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if err := s.applyWeeklyText(context.Background()); err != nil {
				log.Printf("weekly: apply failed: %v", err)
			}
		}
	}
}

func (s *Service) applyWeeklyText(ctx context.Context) error {
	if s.textAmount <= 0 {
		return nil
	}

	meta := struct {
		Week   string `json:"week"`
		Source string `json:"source"`
	}{Week: time.Now().UTC().Format("2006-01-02"), Source: "weekly.free_text"}
	metaBytes, _ := json.Marshal(meta)

	const sql = `
INSERT INTO credit_ledger (user_id, delta_text, reason, meta, op_key)
SELECT
  u.id,
  $1::int,
  'weekly_free',
  $2::jsonb,
  ('weekly_free:' || to_char(now() AT TIME ZONE 'UTC', 'IYYY-IW') || ':' || u.id::text)
FROM users u
ON CONFLICT (op_key) DO NOTHING;
`
	_, err := s.pg.Pool.Exec(ctx, sql, int32(s.textAmount), string(metaBytes))
	return err
}

func nextWeekRun(now time.Time, spec string) (time.Time, error) {
	parts := strings.Fields(spec)
	if len(parts) != 2 {
		return time.Time{}, errors.New("invalid weekly schedule format")
	}
	dayName := strings.ToLower(parts[0])
	clock, err := time.Parse("15:04", parts[1])
	if err != nil {
		return time.Time{}, err
	}

	dayMap := map[string]time.Weekday{
		"sun": time.Sunday,
		"mon": time.Monday,
		"tue": time.Tuesday,
		"wed": time.Wednesday,
		"thu": time.Thursday,
		"fri": time.Friday,
		"sat": time.Saturday,
	}
	targetDay, ok := dayMap[dayName]
	if !ok {
		return time.Time{}, errors.New("invalid weekly schedule day")
	}

	diff := int(targetDay - now.Weekday())
	if diff < 0 {
		diff += 7
	}
	candidate := time.Date(
		now.Year(), now.Month(), now.Day(),
		clock.Hour(), clock.Minute(), 0, 0,
		time.UTC,
	).AddDate(0, 0, diff)
	if !now.Before(candidate) {
		candidate = candidate.AddDate(0, 0, 7)
	}
	return candidate, nil
}
