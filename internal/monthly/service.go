package monthly

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"unitool/internal/db/generated"
	"unitool/internal/storage"
)

// Service adds monthly free credits to users based on settings.free
type Service struct {
	pg     *storage.PG
	atHHMM string // UTC, format "HH:MM"
}

func NewService(pg *storage.PG, atHHMM string) *Service {
	return &Service{pg: pg, atHHMM: atHHMM}
}

// Start runs the scheduler loop in a goroutine.
func (s *Service) Start(ctx context.Context) {
	// Run once on startup to ensure current month is granted (idempotent)
	go func() {
		if err := s.applyMonthlyFree(context.Background()); err != nil {
			log.Printf("monthly: initial apply failed: %v", err)
		}
		s.loop(ctx)
	}()
}

func (s *Service) loop(ctx context.Context) {
	for {
		next, err := nextMonthRun(time.Now().UTC(), s.atHHMM)
		if err != nil {
			log.Printf("monthly: parse time failed: %v", err)
			// default to next month 03:00 UTC
			next, _ = nextMonthRun(time.Now().UTC(), "03:00")
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
			if err := s.applyMonthlyFree(context.Background()); err != nil {
				log.Printf("monthly: apply free failed: %v", err)
			}
			// continue to schedule next month
		}
	}
}

// applyMonthlyFree reads settings.free and inserts one monthly_free record per user
// for the current month, skipping users who already received it this month.
func (s *Service) applyMonthlyFree(ctx context.Context) error {
	q := generated.New(s.pg.Pool)

	raw, err := q.GetSetting(ctx, "free")
	if err != nil {
		return err
	}

	var free struct {
		TextPerMonth  int `json:"text_per_month"`
		ImagePerMonth int `json:"image_per_month"`
		VideoPerMonth int `json:"video_per_month"`
	}
	if err := json.Unmarshal(raw, &free); err != nil {
		return err
	}

	// If all zero — nothing to do
	if free.TextPerMonth == 0 && free.ImagePerMonth == 0 && free.VideoPerMonth == 0 {
		return nil
	}

	meta := struct {
		Month  string `json:"month"`
		Source string `json:"source"`
	}{Month: time.Now().UTC().Format("2006-01"), Source: "settings.free"}
	metaBytes, _ := json.Marshal(meta)

	// Idempotent monthly grant keyed by op_key(month + user_id)
	const sql = `
INSERT INTO credit_ledger (user_id, delta_text, delta_image, delta_video, reason, meta, op_key)
SELECT
  u.id,
  $1::int,
  $2::int,
  $3::int,
  'monthly_free',
  $4::jsonb,
  ('monthly_free:' || to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM') || ':' || u.id::text)
FROM users u
ON CONFLICT (op_key) DO NOTHING;
`
	_, err = s.pg.Pool.Exec(ctx, sql,
		int32(free.TextPerMonth),
		int32(free.ImagePerMonth),
		int32(free.VideoPerMonth),
		string(metaBytes),
	)
	return err
}

// nextMonthRun returns the next time at given HH:MM (UTC) on the first day of a month, not earlier than now.
func nextMonthRun(now time.Time, hhmm string) (time.Time, error) {
	// parse HH:MM
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, err
	}
	y, m := now.Year(), now.Month()
	candidate := time.Date(y, m, 1, t.Hour(), t.Minute(), 0, 0, time.UTC)
	if now.Before(candidate) {
		return candidate, nil
	}
	// next month
	nm := m + 1
	ny := y
	if nm > 12 {
		nm = 1
		ny++
	}
	return time.Date(ny, nm, 1, t.Hour(), t.Minute(), 0, 0, time.UTC), nil
}
