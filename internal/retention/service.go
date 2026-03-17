package retention

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"unitool/internal/storage"
)

type Service struct {
	pg              *storage.PG
	atHHMMUTC       string
	keepChatDays    int
	keepLedgerDays  int
	keepRequestDays int
}

func NewService(pg *storage.PG, atHHMMUTC string, keepChatDays, keepLedgerDays, keepRequestDays int) *Service {
	if atHHMMUTC == "" {
		atHHMMUTC = "04:10"
	}
	if keepChatDays <= 0 {
		keepChatDays = 180
	}
	if keepLedgerDays <= 0 {
		keepLedgerDays = 730
	}
	if keepRequestDays <= 0 {
		keepRequestDays = 365
	}
	return &Service{
		pg:              pg,
		atHHMMUTC:       atHHMMUTC,
		keepChatDays:    keepChatDays,
		keepLedgerDays:  keepLedgerDays,
		keepRequestDays: keepRequestDays,
	}
}

func (s *Service) Start(ctx context.Context) {
	go func() {
		if err := s.run(context.Background()); err != nil {
			log.Printf("retention: initial run failed: %v", err)
		}
		s.loop(ctx)
	}()
}

func (s *Service) loop(ctx context.Context) {
	for {
		next, err := nextDailyRunUTC(time.Now().UTC(), s.atHHMMUTC)
		if err != nil {
			log.Printf("retention: parse schedule failed: %v", err)
			next, _ = nextDailyRunUTC(time.Now().UTC(), "04:10")
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
			if err := s.run(context.Background()); err != nil {
				log.Printf("retention: run failed: %v", err)
			}
		}
	}
}

func (s *Service) run(ctx context.Context) error {
	const q = `
SELECT chat_messages_deleted, credit_ledger_deleted, generation_requests_deleted
FROM run_retention_cleanup(
  ($1::int || ' days')::interval,
  ($2::int || ' days')::interval,
  ($3::int || ' days')::interval
);`

	var chatDeleted, ledgerDeleted, reqDeleted int64
	if err := s.pg.Pool.QueryRow(
		ctx,
		q,
		s.keepChatDays,
		s.keepLedgerDays,
		s.keepRequestDays,
	).Scan(&chatDeleted, &ledgerDeleted, &reqDeleted); err != nil {
		return err
	}

	log.Printf(
		"retention: ok chat=%d ledger=%d requests=%d (keep chat=%dd ledger=%dd requests=%dd)",
		chatDeleted,
		ledgerDeleted,
		reqDeleted,
		s.keepChatDays,
		s.keepLedgerDays,
		s.keepRequestDays,
	)
	return nil
}

func nextDailyRunUTC(now time.Time, hhmm string) (time.Time, error) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, err
	}
	candidate := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	if now.Before(candidate) {
		return candidate, nil
	}
	next := candidate.Add(24 * time.Hour)
	if next.Location() != time.UTC {
		return time.Time{}, errors.New("internal: expected UTC time")
	}
	return next, nil
}

func (s *Service) String() string {
	return fmt.Sprintf(
		"retention(at=%s, chat=%d, ledger=%d, requests=%d)",
		s.atHHMMUTC, s.keepChatDays, s.keepLedgerDays, s.keepRequestDays,
	)
}
