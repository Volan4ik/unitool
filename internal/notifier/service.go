package notifier

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
	"unitool/internal/storage"
)

const (
	defaultPollEvery          = 30 * time.Second
	defaultLifecycleBatch     = 200
	defaultBroadcastBatch     = 120
	defaultBroadcastDelay     = 60 * time.Millisecond
	defaultRunningCampaignTTL = 10 * time.Minute
	tgMessageLimit            = 4096
)

const (
	msgOnboarding1h   = "Хочешь сделать что-то крутое? Начни с фото — это быстрее всего 📸"
	msgNoPurchase24   = "⚡ У тебя почти получилось — открой доступ к полным возможностям"
	msgReactivation7d = "👀 Мы добавили новые возможности — попробуй сейчас"
	msgReactivation15 = "⏳ Мы будем ждать тебя. Это последнее напоминание — загляни, когда будет удобно"
)

type Service struct {
	pg  *storage.PG
	bot *tgbotapi.BotAPI

	pollEvery          time.Duration
	lifecycleBatchSize int
	broadcastBatchSize int
	broadcastDelay     time.Duration
	runningCampaignTTL time.Duration

	wg sync.WaitGroup
}

type dueUser struct {
	UserID int64
	TgID   int64
	Anchor time.Time
}

type broadcastCampaign struct {
	ID         int64
	Message    string
	LastUserID int64
}

type broadcastRecipient struct {
	ID   int64
	TgID int64
}

func NewService(pg *storage.PG, bot *tgbotapi.BotAPI) *Service {
	return &Service{
		pg:                 pg,
		bot:                bot,
		pollEvery:          defaultPollEvery,
		lifecycleBatchSize: defaultLifecycleBatch,
		broadcastBatchSize: defaultBroadcastBatch,
		broadcastDelay:     defaultBroadcastDelay,
		runningCampaignTTL: defaultRunningCampaignTTL,
	}
}

func (s *Service) Start(ctx context.Context) {
	if s == nil || s.pg == nil || s.bot == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runCycle(ctx)
		ticker := time.NewTicker(s.pollEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runCycle(ctx)
			}
		}
	}()
}

func (s *Service) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) EnqueueBroadcast(ctx context.Context, createdByTGID int64, text string) (int64, error) {
	if s == nil || s.pg == nil {
		return 0, errors.New("notifier is not initialized")
	}
	body := strings.TrimSpace(text)
	if body == "" {
		return 0, errors.New("broadcast text is empty")
	}
	if len(body) > tgMessageLimit {
		return 0, fmt.Errorf("broadcast text is too long (max %d chars)", tgMessageLimit)
	}

	const q = `
INSERT INTO broadcast_campaigns (created_by_tg_id, message_text, total_users)
VALUES ($1, $2, (SELECT COUNT(*)::bigint FROM users WHERE is_banned = FALSE))
RETURNING id;
`
	var id int64
	if err := s.pg.Pool.QueryRow(ctx, q, createdByTGID, body).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Service) runCycle(ctx context.Context) {
	if err := s.processLifecycleNotifications(ctx); err != nil {
		log.Printf("notifier: lifecycle cycle failed: %v", err)
	}
	if err := s.processBroadcastBatch(ctx); err != nil {
		log.Printf("notifier: broadcast cycle failed: %v", err)
	}
}

func (s *Service) processLifecycleNotifications(ctx context.Context) error {
	if err := s.processOnboarding1h(ctx); err != nil {
		return err
	}
	if err := s.processNoPurchase24h(ctx); err != nil {
		return err
	}
	if err := s.processReactivation7d(ctx); err != nil {
		return err
	}
	if err := s.processReactivation15d(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Service) processOnboarding1h(ctx context.Context) error {
	const q = `
SELECT
  u.id,
  u.tg_id
FROM users u
LEFT JOIN user_start_attribution sa ON sa.user_id = u.id
LEFT JOIN user_notification_state ns ON ns.user_id = u.id
WHERE u.is_banned = FALSE
  AND ns.onboarding_1h_sent_at IS NULL
  AND now() >= COALESCE(sa.created_at, u.created_at) + interval '1 hour'
  AND NOT EXISTS (
    SELECT 1
    FROM generation_requests gr
    WHERE gr.user_id = u.id
  )
ORDER BY u.id
LIMIT $1;
`
	users, err := s.queryDueUsers(ctx, q, s.lifecycleBatchSize, false)
	if err != nil {
		return err
	}
	for _, u := range users {
		claimed, claimErr := s.claimOnboarding1h(ctx, u.UserID)
		if claimErr != nil {
			log.Printf("notifier: claim onboarding failed user_id=%d err=%v", u.UserID, claimErr)
			continue
		}
		if !claimed {
			continue
		}
		if err := s.sendText(u.TgID, msgOnboarding1h); err != nil {
			log.Printf("notifier: send onboarding failed user_id=%d tg_id=%d err=%v", u.UserID, u.TgID, err)
		}
	}
	return nil
}

func (s *Service) processNoPurchase24h(ctx context.Context) error {
	const q = `
SELECT
  u.id,
  u.tg_id
FROM users u
LEFT JOIN user_start_attribution sa ON sa.user_id = u.id
LEFT JOIN user_notification_state ns ON ns.user_id = u.id
WHERE u.is_banned = FALSE
  AND ns.no_purchase_24h_sent_at IS NULL
  AND now() >= COALESCE(sa.created_at, u.created_at) + interval '24 hours'
  AND NOT EXISTS (
    SELECT 1
    FROM orders o
    WHERE o.user_id = u.id
      AND o.status = 'paid'
  )
ORDER BY u.id
LIMIT $1;
`
	users, err := s.queryDueUsers(ctx, q, s.lifecycleBatchSize, false)
	if err != nil {
		return err
	}
	for _, u := range users {
		claimed, claimErr := s.claimNoPurchase24h(ctx, u.UserID)
		if claimErr != nil {
			log.Printf("notifier: claim no-purchase failed user_id=%d err=%v", u.UserID, claimErr)
			continue
		}
		if !claimed {
			continue
		}
		if err := s.sendText(u.TgID, msgNoPurchase24); err != nil {
			log.Printf("notifier: send no-purchase failed user_id=%d tg_id=%d err=%v", u.UserID, u.TgID, err)
		}
	}
	return nil
}

func (s *Service) processReactivation7d(ctx context.Context) error {
	const q = `
SELECT
  u.id,
  u.tg_id,
  u.updated_at
FROM users u
LEFT JOIN user_notification_state ns ON ns.user_id = u.id
WHERE u.is_banned = FALSE
  AND now() >= u.updated_at + interval '7 days'
  AND (ns.reactivation_7d_anchor IS NULL OR ns.reactivation_7d_anchor < u.updated_at)
ORDER BY u.updated_at ASC, u.id ASC
LIMIT $1;
`
	users, err := s.queryDueUsers(ctx, q, s.lifecycleBatchSize, true)
	if err != nil {
		return err
	}
	for _, u := range users {
		claimed, claimErr := s.claimReactivation7d(ctx, u.UserID, u.Anchor)
		if claimErr != nil {
			log.Printf("notifier: claim reactivation 7d failed user_id=%d err=%v", u.UserID, claimErr)
			continue
		}
		if !claimed {
			continue
		}
		if err := s.sendText(u.TgID, msgReactivation7d); err != nil {
			log.Printf("notifier: send reactivation 7d failed user_id=%d tg_id=%d err=%v", u.UserID, u.TgID, err)
		}
	}
	return nil
}

func (s *Service) processReactivation15d(ctx context.Context) error {
	const q = `
SELECT
  u.id,
  u.tg_id,
  u.updated_at
FROM users u
LEFT JOIN user_notification_state ns ON ns.user_id = u.id
WHERE u.is_banned = FALSE
  AND now() >= u.updated_at + interval '15 days'
  AND (ns.reactivation_15d_anchor IS NULL OR ns.reactivation_15d_anchor < u.updated_at)
ORDER BY u.updated_at ASC, u.id ASC
LIMIT $1;
`
	users, err := s.queryDueUsers(ctx, q, s.lifecycleBatchSize, true)
	if err != nil {
		return err
	}
	for _, u := range users {
		claimed, claimErr := s.claimReactivation15d(ctx, u.UserID, u.Anchor)
		if claimErr != nil {
			log.Printf("notifier: claim reactivation 15d failed user_id=%d err=%v", u.UserID, claimErr)
			continue
		}
		if !claimed {
			continue
		}
		if err := s.sendText(u.TgID, msgReactivation15); err != nil {
			log.Printf("notifier: send reactivation 15d failed user_id=%d tg_id=%d err=%v", u.UserID, u.TgID, err)
		}
	}
	return nil
}

func (s *Service) processBroadcastBatch(ctx context.Context) error {
	campaign, ok, err := s.claimBroadcastCampaign(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	recipients, err := s.listBroadcastRecipients(ctx, campaign.LastUserID, int32(s.broadcastBatchSize))
	if err != nil {
		_ = s.failCampaign(ctx, campaign.ID, err.Error())
		return err
	}
	if len(recipients) == 0 {
		return s.finishCampaign(ctx, campaign.ID)
	}

	var sentCount int64
	var failedCount int64
	lastUserID := campaign.LastUserID
	for i, rec := range recipients {
		lastUserID = rec.ID
		if sendErr := s.sendText(rec.TgID, campaign.Message); sendErr != nil {
			failedCount++
			log.Printf("notifier: broadcast send failed campaign_id=%d user_id=%d tg_id=%d err=%v", campaign.ID, rec.ID, rec.TgID, sendErr)
		} else {
			sentCount++
		}

		if i == len(recipients)-1 || s.broadcastDelay <= 0 {
			continue
		}
		timer := time.NewTimer(s.broadcastDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			if saveErr := s.saveCampaignProgress(ctx, campaign.ID, lastUserID, sentCount, failedCount, "pending", ""); saveErr != nil {
				log.Printf("notifier: save campaign progress on cancel failed campaign_id=%d err=%v", campaign.ID, saveErr)
			}
			return ctx.Err()
		case <-timer.C:
		}
	}

	return s.saveCampaignProgress(ctx, campaign.ID, lastUserID, sentCount, failedCount, "pending", "")
}

func (s *Service) claimOnboarding1h(ctx context.Context, userID int64) (bool, error) {
	const q = `
INSERT INTO user_notification_state (user_id, onboarding_1h_sent_at)
VALUES ($1, now())
ON CONFLICT (user_id) DO UPDATE
SET onboarding_1h_sent_at = EXCLUDED.onboarding_1h_sent_at,
    updated_at = now()
WHERE user_notification_state.onboarding_1h_sent_at IS NULL;
`
	tag, err := s.pg.Pool.Exec(ctx, q, userID)
	return tag.RowsAffected() > 0, err
}

func (s *Service) claimNoPurchase24h(ctx context.Context, userID int64) (bool, error) {
	const q = `
INSERT INTO user_notification_state (user_id, no_purchase_24h_sent_at)
VALUES ($1, now())
ON CONFLICT (user_id) DO UPDATE
SET no_purchase_24h_sent_at = EXCLUDED.no_purchase_24h_sent_at,
    updated_at = now()
WHERE user_notification_state.no_purchase_24h_sent_at IS NULL;
`
	tag, err := s.pg.Pool.Exec(ctx, q, userID)
	return tag.RowsAffected() > 0, err
}

func (s *Service) claimReactivation7d(ctx context.Context, userID int64, anchor time.Time) (bool, error) {
	const q = `
INSERT INTO user_notification_state (user_id, reactivation_7d_anchor)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE
SET reactivation_7d_anchor = EXCLUDED.reactivation_7d_anchor,
    updated_at = now()
WHERE user_notification_state.reactivation_7d_anchor IS NULL
   OR user_notification_state.reactivation_7d_anchor < EXCLUDED.reactivation_7d_anchor;
`
	tag, err := s.pg.Pool.Exec(ctx, q, userID, anchor.UTC())
	return tag.RowsAffected() > 0, err
}

func (s *Service) claimReactivation15d(ctx context.Context, userID int64, anchor time.Time) (bool, error) {
	const q = `
INSERT INTO user_notification_state (user_id, reactivation_15d_anchor)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE
SET reactivation_15d_anchor = EXCLUDED.reactivation_15d_anchor,
    updated_at = now()
WHERE user_notification_state.reactivation_15d_anchor IS NULL
   OR user_notification_state.reactivation_15d_anchor < EXCLUDED.reactivation_15d_anchor;
`
	tag, err := s.pg.Pool.Exec(ctx, q, userID, anchor.UTC())
	return tag.RowsAffected() > 0, err
}

func (s *Service) claimBroadcastCampaign(ctx context.Context) (broadcastCampaign, bool, error) {
	const q = `
WITH picked AS (
  SELECT id
  FROM broadcast_campaigns
  WHERE status = 'pending'
     OR (status = 'running' AND updated_at < now() - $1::interval)
  ORDER BY id
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
UPDATE broadcast_campaigns c
SET status = 'running',
    started_at = COALESCE(c.started_at, now()),
    error_message = NULL,
    updated_at = now()
FROM picked
WHERE c.id = picked.id
RETURNING c.id, c.message_text, c.last_user_id;
`
	var out broadcastCampaign
	if err := s.pg.Pool.QueryRow(ctx, q, durationToIntervalLiteral(s.runningCampaignTTL)).Scan(&out.ID, &out.Message, &out.LastUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return broadcastCampaign{}, false, nil
		}
		return broadcastCampaign{}, false, err
	}
	return out, true, nil
}

func (s *Service) listBroadcastRecipients(ctx context.Context, afterID int64, limit int32) ([]broadcastRecipient, error) {
	const q = `
SELECT id, tg_id
FROM users
WHERE is_banned = FALSE
  AND id > $1
ORDER BY id ASC
LIMIT $2;
`
	rows, err := s.pg.Pool.Query(ctx, q, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]broadcastRecipient, 0, limit)
	for rows.Next() {
		var rec broadcastRecipient
		if scanErr := rows.Scan(&rec.ID, &rec.TgID); scanErr != nil {
			return nil, scanErr
		}
		items = append(items, rec)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (s *Service) saveCampaignProgress(
	ctx context.Context,
	campaignID int64,
	lastUserID int64,
	sentDelta int64,
	failedDelta int64,
	status string,
	errorText string,
) error {
	errMsg := strings.TrimSpace(errorText)
	if len(errMsg) > 700 {
		errMsg = errMsg[:700]
	}
	const q = `
UPDATE broadcast_campaigns
SET
  last_user_id = $2,
  sent_count = sent_count + $3,
  failed_count = failed_count + $4,
  status = $5,
  error_message = CASE WHEN $6 = '' THEN NULL ELSE $6 END,
  finished_at = CASE WHEN $5 IN ('done', 'failed') THEN now() ELSE finished_at END,
  updated_at = now()
WHERE id = $1
  AND status = 'running';
`
	_, err := s.pg.Pool.Exec(ctx, q, campaignID, lastUserID, sentDelta, failedDelta, status, errMsg)
	return err
}

func (s *Service) finishCampaign(ctx context.Context, campaignID int64) error {
	const q = `
UPDATE broadcast_campaigns
SET
  status = 'done',
  error_message = NULL,
  finished_at = now(),
  updated_at = now()
WHERE id = $1
  AND status = 'running';
`
	_, err := s.pg.Pool.Exec(ctx, q, campaignID)
	return err
}

func (s *Service) failCampaign(ctx context.Context, campaignID int64, errText string) error {
	msg := strings.TrimSpace(errText)
	if len(msg) > 700 {
		msg = msg[:700]
	}
	const q = `
UPDATE broadcast_campaigns
SET
  status = 'failed',
  error_message = CASE WHEN $2 = '' THEN NULL ELSE $2 END,
  finished_at = now(),
  updated_at = now()
WHERE id = $1
  AND status = 'running';
`
	_, err := s.pg.Pool.Exec(ctx, q, campaignID, msg)
	return err
}

func (s *Service) queryDueUsers(ctx context.Context, query string, limit int, withAnchor bool) ([]dueUser, error) {
	rows, err := s.pg.Pool.Query(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]dueUser, 0, limit)
	for rows.Next() {
		var u dueUser
		if withAnchor {
			if scanErr := rows.Scan(&u.UserID, &u.TgID, &u.Anchor); scanErr != nil {
				return nil, scanErr
			}
		} else {
			if scanErr := rows.Scan(&u.UserID, &u.TgID); scanErr != nil {
				return nil, scanErr
			}
		}
		out = append(out, u)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return out, nil
}

func (s *Service) sendText(chatID int64, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	_, err := s.bot.Send(tgbotapi.NewMessage(chatID, text))
	return err
}

func durationToIntervalLiteral(d time.Duration) string {
	if d <= 0 {
		d = defaultRunningCampaignTTL
	}
	sec := int64(d / time.Second)
	if sec <= 0 {
		sec = 1
	}
	return fmt.Sprintf("%d seconds", sec)
}
