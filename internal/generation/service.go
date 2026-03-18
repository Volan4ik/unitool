package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "unitool/internal/db/generated"
	"unitool/pkg/provider"
)

type Service struct {
	Bot        *tgbotapi.BotAPI
	Q          *db.Queries
	Prov       provider.ModelProvider
	Workers    int
	PollEvery  time.Duration
	GenTimeout time.Duration
	StaleAfter time.Duration

	wg sync.WaitGroup
}

const (
	mediaSendAttempts     = 3
	mediaSendBaseBackoff  = 700 * time.Millisecond
	maxProviderOutputSize = 512
	staleFailReason       = "stale running job exhausted attempts"
)

func NewService(bot *tgbotapi.BotAPI, q *db.Queries, prov provider.ModelProvider, workers int, pollEvery, genTimeout time.Duration) *Service {
	if workers <= 0 {
		workers = 2
	}
	if pollEvery <= 0 {
		pollEvery = 700 * time.Millisecond
	}
	if genTimeout <= 0 {
		genTimeout = 120 * time.Second
	}
	staleAfter := 10 * time.Minute
	return &Service{
		Bot:        bot,
		Q:          q,
		Prov:       prov,
		Workers:    workers,
		PollEvery:  pollEvery,
		GenTimeout: genTimeout,
		StaleAfter: staleAfter,
	}
}

func (s *Service) Start(ctx context.Context) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.recoverStaleLoop(ctx)
	}()
	for i := 0; i < s.Workers; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.loop(ctx)
		}()
	}
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

func (s *Service) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := s.Q.ClaimNextGenerationJob(ctx)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				time.Sleep(s.PollEvery)
				continue
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}

		s.runClaimedJob(ctx, job)
	}
}

func (s *Service) runClaimedJob(ctx context.Context, job db.GenerationJob) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic: %v", r)
			log.Printf(
				"generation: panic recovered job_id=%d user_id=%d chat_id=%d kind=%s err=%v stack=%s",
				job.ID, job.UserID, job.ChatID, job.Kind, err, string(debug.Stack()),
			)
			s.handleFailedAttempt(ctx, job, err, 0)
		}
	}()
	s.processJob(ctx, job)
}

func (s *Service) recoverStaleLoop(ctx context.Context) {
	if s.StaleAfter <= 0 {
		s.StaleAfter = 10 * time.Minute
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	s.recoverStaleRunningJobs(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.recoverStaleRunningJobs(ctx)
		}
	}
}

func (s *Service) recoverStaleRunningJobs(ctx context.Context) {
	staleSeconds := int32(s.StaleAfter / time.Second)
	if staleSeconds <= 0 {
		staleSeconds = 600
	}
	requeued, err := s.Q.RequeueStaleRunningJobs(ctx, staleSeconds)
	if err != nil {
		log.Printf("generation: reclaim stale running jobs failed: %v", err)
		return
	}
	failed, err := s.Q.FailStaleRunningJobs(ctx, staleSeconds)
	if err != nil {
		log.Printf("generation: fail stale running jobs failed: %v", err)
		return
	}
	for _, item := range failed {
		s.finalizeFailedSideEffects(
			ctx,
			item.ID,
			item.GenerationRequestID,
			item.UserID,
			item.ChatID,
			item.Kind,
			staleFailReason,
			0,
			"stale_recovery",
			true,
		)
	}
	if requeued > 0 || len(failed) > 0 {
		log.Printf("generation: recovered stale jobs requeued=%d failed=%d stale_after_sec=%d", requeued, len(failed), staleSeconds)
	}
}

func (s *Service) processJob(ctx context.Context, job db.GenerationJob) {
	t0 := time.Now()
	ctxGen, cancel := context.WithTimeout(ctx, s.GenTimeout)
	defer cancel()

	resp, err := s.Prov.Generate(ctxGen, provider.ModelRequest{
		UserID: job.UserID,
		Input:  job.Prompt,
		Model:  job.Model,
		Params: map[string]any{"kind": job.Kind},
	})
	latency := int32(time.Since(t0).Milliseconds())
	if err != nil {
		s.handleFailedAttempt(ctx, job, err, latency)
		return
	}
	output := strings.TrimSpace(resp.Output)
	log.Printf(
		"generation: provider result job_id=%d user_id=%d chat_id=%d kind=%s output=%q",
		job.ID, job.UserID, job.ChatID, job.Kind, shortOutput(output),
	)

	if _, err := s.Q.MarkGenerationJobDone(ctx, db.MarkGenerationJobDoneParams{
		ID:         job.ID,
		ResultText: pgtype.Text{String: output, Valid: output != ""},
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf(
				"generation: skip duplicate completion job_id=%d user_id=%d chat_id=%d kind=%s",
				job.ID, job.UserID, job.ChatID, job.Kind,
			)
			return
		}
		log.Printf(
			"generation: mark done failed job_id=%d user_id=%d chat_id=%d kind=%s err=%v",
			job.ID, job.UserID, job.ChatID, job.Kind, err,
		)
		return
	}

	// Finalize generation request metrics/cost only for terminally-claimed runner.
	var cImg, cVid pgtype.Int4
	switch job.Kind {
	case "image":
		cImg = pgtype.Int4{Int32: 1, Valid: true}
	case "video":
		cVid = pgtype.Int4{Int32: 1, Valid: true}
	}
	reqRows, err := s.Q.FinishGenerationRequest(ctx, db.FinishGenerationRequestParams{
		ID:               job.GenerationRequestID,
		OutputTokens:     pgtype.Int4{Int32: int32(resp.Tokens), Valid: resp.Tokens > 0},
		LatencyMs:        pgtype.Int4{Int32: latency, Valid: true},
		CostCreditsText:  pgtype.Int4{},
		CostCreditsImage: cImg,
		CostCreditsVideo: cVid,
	})
	if err != nil {
		log.Printf(
			"generation: finish request failed job_id=%d gen_id=%d kind=%s err=%v",
			job.ID, job.GenerationRequestID, job.Kind, err,
		)
	} else if reqRows == 0 {
		log.Printf(
			"generation: finish request skipped terminal-state job_id=%d gen_id=%d kind=%s",
			job.ID, job.GenerationRequestID, job.Kind,
		)
	}

	if _, err := s.Q.InsertChatMessage(ctx, db.InsertChatMessageParams{
		UserID:              job.UserID,
		ConversationID:      job.ConversationID,
		Kind:                job.Kind,
		Role:                "assistant",
		ContentText:         pgtype.Text{String: resp.Output, Valid: true},
		AttachmentUrl:       pgtype.Text{},
		Provider:            pgtype.Text{String: job.Provider, Valid: true},
		Model:               pgtype.Text{String: job.Model, Valid: true},
		InputTokens:         pgtype.Int4{},
		OutputTokens:        pgtype.Int4{Int32: int32(resp.Tokens), Valid: resp.Tokens > 0},
		GenerationRequestID: pgtype.Int8{Int64: job.GenerationRequestID, Valid: true},
	}); err != nil {
		log.Printf(
			"generation: insert assistant message failed job_id=%d gen_id=%d kind=%s err=%v",
			job.ID, job.GenerationRequestID, job.Kind, err,
		)
	}

	s.sendMediaResult(ctx, job, output)
}

func (s *Service) handleFailedAttempt(ctx context.Context, job db.GenerationJob, callErr error, latency int32) {
	errText := callErr.Error()
	if int(job.Attempts) < int(job.MaxAttempts) {
		backoff := time.Duration(job.Attempts*2) * time.Second
		if backoff < time.Second {
			backoff = time.Second
		}
		rows, err := s.Q.RequeueGenerationJob(ctx, db.RequeueGenerationJobParams{
			ID:           job.ID,
			ErrorMessage: pgtype.Text{String: errText, Valid: true},
			NextAttemptAt: pgtype.Timestamptz{
				Time:  time.Now().Add(backoff),
				Valid: true,
			},
		})
		if err != nil {
			log.Printf(
				"generation: requeue failed job_id=%d user_id=%d chat_id=%d kind=%s attempt=%d/%d err=%q requeue_err=%v",
				job.ID, job.UserID, job.ChatID, job.Kind, job.Attempts, job.MaxAttempts, errText, err,
			)
			// Safe fallback: finalize as permanent failure for this running claim.
			s.finalizeFailedAttempt(ctx, job, errText, latency)
			return
		}
		if rows == 0 {
			log.Printf(
				"generation: requeue skipped stale runner job_id=%d user_id=%d chat_id=%d kind=%s attempt=%d/%d err=%q",
				job.ID, job.UserID, job.ChatID, job.Kind, job.Attempts, job.MaxAttempts, errText,
			)
			return
		}
		log.Printf(
			"generation: requeue success job_id=%d user_id=%d chat_id=%d kind=%s attempt=%d/%d err=%q",
			job.ID, job.UserID, job.ChatID, job.Kind, job.Attempts, job.MaxAttempts, errText,
		)
		return
	}

	s.finalizeFailedAttempt(ctx, job, errText, latency)
}

func (s *Service) finalizeFailedAttempt(ctx context.Context, job db.GenerationJob, errText string, latency int32) {
	rows, err := s.Q.MarkGenerationJobFailed(ctx, db.MarkGenerationJobFailedParams{
		ID:           job.ID,
		ErrorMessage: pgtype.Text{String: errText, Valid: true},
	})
	if err != nil {
		log.Printf(
			"generation: mark failed transition error job_id=%d user_id=%d chat_id=%d kind=%s err=%v",
			job.ID, job.UserID, job.ChatID, job.Kind, err,
		)
		return
	}
	if rows == 0 {
		log.Printf(
			"generation: stale runner skip finalization job_id=%d user_id=%d chat_id=%d kind=%s err=%q",
			job.ID, job.UserID, job.ChatID, job.Kind, errText,
		)
		return
	}

	s.finalizeFailedSideEffects(ctx, job.ID, job.GenerationRequestID, job.UserID, job.ChatID, job.Kind, errText, latency, "async_generation_failed", true)
}

func (s *Service) finalizeFailedSideEffects(
	ctx context.Context,
	jobID int64,
	genID int64,
	userID int64,
	chatID int64,
	kind string,
	errText string,
	latency int32,
	source string,
	notifyUser bool,
) {
	rows, err := s.Q.FailGenerationRequest(ctx, db.FailGenerationRequestParams{
		ID:           genID,
		Column2:      "failed",
		ErrorMessage: pgtype.Text{String: errText, Valid: true},
		LatencyMs:    pgtype.Int4{Int32: latency, Valid: true},
	})
	if err != nil {
		log.Printf(
			"generation: fail generation request write failed job_id=%d gen_id=%d source=%s err=%v",
			jobID, genID, source, err,
		)
		return
	}
	if rows == 0 {
		log.Printf(
			"generation: fail generation request skipped terminal-state job_id=%d gen_id=%d source=%s",
			jobID, genID, source,
		)
		return
	}

	opKey := fmt.Sprintf("refund:job:%d", jobID)
	meta, _ := json.Marshal(map[string]any{
		"job_id": jobID,
		"gen_id": genID,
		"source": source,
		"err":    errText,
	})
	refundErr := s.refundOneCredit(ctx, userID, kind, meta, opKey)
	if refundErr != nil {
		log.Printf(
			"generation: refund failed job_id=%d gen_id=%d user_id=%d kind=%s source=%s op_key=%s err=%v reconcile_required=true",
			jobID, genID, userID, kind, source, opKey, refundErr,
		)
	}
	log.Printf(
		"generation: failed permanently job_id=%d gen_id=%d user_id=%d chat_id=%d kind=%s source=%s refunded=%t err=%q",
		jobID, genID, userID, chatID, kind, source, refundErr == nil, errText,
	)

	if !notifyUser {
		return
	}
	msg := "Ошибка генерации медиа. Попытка возвращена."
	if refundErr != nil {
		msg = "Ошибка генерации медиа. Возврат попытки в обработке, попробуйте позже."
	}
	if err := s.sendText(chatID, msg); err != nil {
		log.Printf(
			"generation: send failure notice failed job_id=%d gen_id=%d user_id=%d chat_id=%d kind=%s source=%s err=%v",
			jobID, genID, userID, chatID, kind, source, err,
		)
	}
}

func (s *Service) refundOneCredit(ctx context.Context, userID int64, kind string, meta []byte, opKey string) error {
	switch kind {
	case "text":
		return s.Q.RefundText(ctx, db.RefundTextParams{UserID: userID, Meta: meta, OpKey: pgtype.Text{String: opKey, Valid: true}})
	case "image":
		return s.Q.RefundImage(ctx, db.RefundImageParams{UserID: userID, Meta: meta, OpKey: pgtype.Text{String: opKey, Valid: true}})
	case "video":
		return s.Q.RefundVideo(ctx, db.RefundVideoParams{UserID: userID, Meta: meta, OpKey: pgtype.Text{String: opKey, Valid: true}})
	default:
		return fmt.Errorf("unknown refund kind: %s", kind)
	}
}

func (s *Service) sendMediaResult(ctx context.Context, job db.GenerationJob, output string) {
	if output == "" {
		log.Printf(
			"generation: empty output fallback job_id=%d user_id=%d chat_id=%d kind=%s",
			job.ID, job.UserID, job.ChatID, job.Kind,
		)
		if err := s.sendText(job.ChatID, "Генерация завершена, но сервис не вернул ссылку на файл."); err != nil {
			log.Printf(
				"generation: fallback send failed job_id=%d user_id=%d chat_id=%d kind=%s err=%v",
				job.ID, job.UserID, job.ChatID, job.Kind, err,
			)
		}
		return
	}

	if !isHTTPURL(output) {
		log.Printf(
			"generation: invalid output url fallback job_id=%d user_id=%d chat_id=%d kind=%s output=%q",
			job.ID, job.UserID, job.ChatID, job.Kind, shortOutput(output),
		)
		msg := fmt.Sprintf("Генерация завершена. Ссылка на медиа некорректна: %s", output)
		if err := s.sendText(job.ChatID, msg); err != nil {
			log.Printf(
				"generation: invalid-url fallback send failed job_id=%d user_id=%d chat_id=%d kind=%s err=%v",
				job.ID, job.UserID, job.ChatID, job.Kind, err,
			)
		}
		return
	}

	var lastErr error
	for attempt := 1; attempt <= mediaSendAttempts; attempt++ {
		switch job.Kind {
		case "image":
			photo := tgbotapi.NewPhoto(job.ChatID, tgbotapi.FileURL(output))
			photo.Caption = "Готово ✅"
			_, lastErr = s.Bot.Send(photo)
		case "video":
			video := tgbotapi.NewVideo(job.ChatID, tgbotapi.FileURL(output))
			video.Caption = "Готово ✅"
			_, lastErr = s.Bot.Send(video)
		default:
			lastErr = fmt.Errorf("unsupported media kind: %s", job.Kind)
		}
		if lastErr == nil {
			log.Printf(
				"generation: telegram media send ok job_id=%d user_id=%d chat_id=%d kind=%s attempt=%d output=%q",
				job.ID, job.UserID, job.ChatID, job.Kind, attempt, shortOutput(output),
			)
			return
		}
		log.Printf(
			"generation: telegram media send failed job_id=%d user_id=%d chat_id=%d kind=%s attempt=%d err=%v output=%q",
			job.ID, job.UserID, job.ChatID, job.Kind, attempt, lastErr, shortOutput(output),
		)
		if attempt == mediaSendAttempts {
			break
		}
		wait := time.Duration(attempt) * mediaSendBaseBackoff
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}

	msg := fmt.Sprintf("Не удалось отправить медиа как файл. Вот ссылка на результат: %s", output)
	if err := s.sendText(job.ChatID, msg); err != nil {
		log.Printf(
			"generation: telegram text fallback failed job_id=%d user_id=%d chat_id=%d kind=%s err=%v",
			job.ID, job.UserID, job.ChatID, job.Kind, err,
		)
		return
	}
	log.Printf(
		"generation: telegram fallback sent job_id=%d user_id=%d chat_id=%d kind=%s output=%q last_err=%v",
		job.ID, job.UserID, job.ChatID, job.Kind, shortOutput(output), lastErr,
	)
}

func (s *Service) sendText(chatID int64, text string) error {
	_, err := s.Bot.Send(tgbotapi.NewMessage(chatID, text))
	return err
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	return true
}

func shortOutput(raw string) string {
	r := strings.TrimSpace(raw)
	if len(r) <= maxProviderOutputSize {
		return r
	}
	return r[:maxProviderOutputSize] + "...(truncated)"
}
