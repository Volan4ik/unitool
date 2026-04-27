package generation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
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
	mediaFetchTimeout     = 45 * time.Second
	stateWriteTimeout     = 8 * time.Second
	maxDownloadedMedia    = 80 * 1024 * 1024
	maxInlineImageBytes   = 15 * 1024 * 1024
	maxProviderOutputSize = 512
	staleFailReason       = "stale running job exhausted attempts"
	promptRulesURL        = "https://teletype.in/@loonagpt"
)

var downloadMediaFn = downloadMediaBytes

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

		s.runClaimedJob(ctx, generationJobFromClaimed(job))
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

	input := job.Prompt
	params := map[string]any{"kind": job.Kind}
	if job.Kind == "video" || job.Kind == "image" {
		cleanPrompt, inputReferences := splitPromptInputReferences(job.Prompt)
		input = cleanPrompt
		if len(inputReferences) > 0 {
			params["input_reference"] = inputReferences[0]
			params["input_references"] = inputReferences
			if job.Kind == "image" {
				params["input_references"] = inputReferences
			}
		}
	}

	resp, err := s.Prov.Generate(ctxGen, provider.ModelRequest{
		UserID: job.UserID,
		Input:  input,
		Model:  job.Model,
		Params: params,
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
	var cImg, cVid int32
	switch job.Kind {
	case "image":
		cImg = 1
	case "video":
		cVid = 1
	}
	reqRows, err := s.Q.FinishGenerationRequest(ctx, db.FinishGenerationRequestParams{
		ID:               job.GenerationRequestID,
		OutputTokens:     pgtype.Int4{Int32: int32(resp.Tokens), Valid: resp.Tokens > 0},
		LatencyMs:        pgtype.Int4{Int32: latency, Valid: true},
		CostCreditsText:  0,
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
	if isProviderModerationError(errText) {
		s.finalizeFailedAttempt(ctx, job, errText, latency)
		return
	}
	if int(job.Attempts) < int(job.MaxAttempts) {
		backoff := time.Duration(job.Attempts*2) * time.Second
		if backoff < time.Second {
			backoff = time.Second
		}
		stateCtx, cancel := withStateWriteCtx(ctx)
		defer cancel()
		rows, err := s.Q.RequeueGenerationJob(stateCtx, db.RequeueGenerationJobParams{
			ID:           job.ID,
			ErrorMessage: pgtype.Text{String: errText, Valid: true},
			NextAttemptAt: pgtype.Timestamptz{
				Time:  time.Now().Add(backoff),
				Valid: true,
			},
		})
		if err != nil {
			log.Printf(
				"generation: requeue failed job_id=%d user_id=%d chat_id=%d kind=%s model=%s attempt=%d/%d err=%q requeue_err=%v",
				job.ID, job.UserID, job.ChatID, job.Kind, job.Model, job.Attempts, job.MaxAttempts, errText, err,
			)
			// Safe fallback: finalize as permanent failure for this running claim.
			s.finalizeFailedAttempt(ctx, job, errText, latency)
			return
		}
		if rows == 0 {
			log.Printf(
				"generation: requeue skipped stale runner job_id=%d user_id=%d chat_id=%d kind=%s model=%s attempt=%d/%d err=%q",
				job.ID, job.UserID, job.ChatID, job.Kind, job.Model, job.Attempts, job.MaxAttempts, errText,
			)
			return
		}
		log.Printf(
			"generation: requeue success job_id=%d user_id=%d chat_id=%d kind=%s model=%s attempt=%d/%d err=%q",
			job.ID, job.UserID, job.ChatID, job.Kind, job.Model, job.Attempts, job.MaxAttempts, errText,
		)
		return
	}

	s.finalizeFailedAttempt(ctx, job, errText, latency)
}

func (s *Service) finalizeFailedAttempt(ctx context.Context, job db.GenerationJob, errText string, latency int32) {
	stateCtx, cancel := withStateWriteCtx(ctx)
	defer cancel()
	rows, err := s.Q.MarkGenerationJobFailed(stateCtx, db.MarkGenerationJobFailedParams{
		ID:           job.ID,
		ErrorMessage: pgtype.Text{String: errText, Valid: true},
	})
	if err != nil {
		log.Printf(
			"generation: mark failed transition error job_id=%d user_id=%d chat_id=%d kind=%s model=%s err=%v",
			job.ID, job.UserID, job.ChatID, job.Kind, job.Model, err,
		)
		return
	}
	if rows == 0 {
		log.Printf(
			"generation: stale runner skip finalization job_id=%d user_id=%d chat_id=%d kind=%s model=%s err=%q",
			job.ID, job.UserID, job.ChatID, job.Kind, job.Model, errText,
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
	stateCtx, cancel := withStateWriteCtx(ctx)
	defer cancel()
	rows, err := s.Q.FailGenerationRequest(stateCtx, db.FailGenerationRequestParams{
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
	refundErr := s.refundOneCredit(stateCtx, userID, kind, meta, opKey)
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
	msg := userFailureMessage(errText, refundErr != nil)
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
	triedDownloadedVideo := false
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

	if job.Kind == "image" && isDataImageURI(output) {
		s.sendInlineImageResult(ctx, job, output)
		return
	}

	if job.Kind == "video" && shouldDownloadVideoFirst(output) {
		triedDownloadedVideo = true
		if err := s.sendDownloadedVideo(ctx, job, output); err == nil {
			return
		} else {
			log.Printf(
				"generation: pre-download video send failed job_id=%d user_id=%d chat_id=%d kind=%s err=%v output=%q",
				job.ID, job.UserID, job.ChatID, job.Kind, err, shortOutput(output),
			)
		}
	}

	if !isHTTPURL(output) {
		log.Printf(
			"generation: invalid output url fallback job_id=%d user_id=%d chat_id=%d kind=%s output=%q",
			job.ID, job.UserID, job.ChatID, job.Kind, shortOutput(output),
		)
		msg := "Генерация завершена, но не удалось обработать ссылку на медиа."
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
			if lastErr == nil {
				break
			}
			// Some providers return video content URLs with non-video MIME type.
			// In that case Telegram rejects sendVideo, but sendDocument can still work.
			doc := tgbotapi.NewDocument(job.ChatID, tgbotapi.FileURL(output))
			doc.Caption = "Готово ✅"
			if _, docErr := s.Bot.Send(doc); docErr == nil {
				log.Printf(
					"generation: telegram video sent as document job_id=%d user_id=%d chat_id=%d kind=%s attempt=%d output=%q",
					job.ID, job.UserID, job.ChatID, job.Kind, attempt, shortOutput(output),
				)
				return
			} else {
				lastErr = fmt.Errorf("sendVideo: %v; sendDocument: %v", lastErr, docErr)
			}
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

	msg := "Не удалось отправить медиа как файл. Попробуйте позже."
	if job.Kind == "video" && !triedDownloadedVideo {
		if err := s.sendDownloadedVideo(ctx, job, output); err == nil {
			return
		} else {
			log.Printf(
				"generation: downloaded video fallback failed job_id=%d user_id=%d chat_id=%d kind=%s err=%v output=%q",
				job.ID, job.UserID, job.ChatID, job.Kind, err, shortOutput(output),
			)
		}
	}

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

func withStateWriteCtx(parent context.Context) (context.Context, context.CancelFunc) {
	if parent != nil && parent.Err() == nil {
		return parent, func() {}
	}
	return context.WithTimeout(context.Background(), stateWriteTimeout)
}

func (s *Service) sendInlineImageResult(ctx context.Context, job db.GenerationJob, output string) {
	mime, blob, err := decodeDataImageURI(output)
	if err != nil {
		log.Printf(
			"generation: invalid inline image fallback job_id=%d user_id=%d chat_id=%d kind=%s err=%v output=%q",
			job.ID, job.UserID, job.ChatID, job.Kind, err, shortOutput(output),
		)
		msg := "Генерация завершена, но встроенное изображение повреждено."
		if sendErr := s.sendText(job.ChatID, msg); sendErr != nil {
			log.Printf(
				"generation: invalid-inline-image fallback send failed job_id=%d user_id=%d chat_id=%d kind=%s err=%v",
				job.ID, job.UserID, job.ChatID, job.Kind, sendErr,
			)
		}
		return
	}

	fileName := "generated.png"
	if strings.Contains(mime, "jpeg") || strings.Contains(mime, "jpg") {
		fileName = "generated.jpg"
	} else if strings.Contains(mime, "webp") {
		fileName = "generated.webp"
	}

	var lastErr error
	for attempt := 1; attempt <= mediaSendAttempts; attempt++ {
		photo := tgbotapi.NewPhoto(job.ChatID, tgbotapi.FileBytes{Name: fileName, Bytes: blob})
		photo.Caption = "Готово ✅"
		_, lastErr = s.Bot.Send(photo)
		if lastErr == nil {
			log.Printf(
				"generation: telegram inline image send ok job_id=%d user_id=%d chat_id=%d kind=%s attempt=%d bytes=%d",
				job.ID, job.UserID, job.ChatID, job.Kind, attempt, len(blob),
			)
			return
		}
		log.Printf(
			"generation: telegram inline image send failed job_id=%d user_id=%d chat_id=%d kind=%s attempt=%d err=%v",
			job.ID, job.UserID, job.ChatID, job.Kind, attempt, lastErr,
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

	if err := s.sendText(job.ChatID, "Не удалось отправить сгенерированное изображение."); err != nil {
		log.Printf(
			"generation: inline-image text fallback failed job_id=%d user_id=%d chat_id=%d kind=%s err=%v",
			job.ID, job.UserID, job.ChatID, job.Kind, err,
		)
		return
	}
	log.Printf(
		"generation: inline-image fallback sent job_id=%d user_id=%d chat_id=%d kind=%s last_err=%v",
		job.ID, job.UserID, job.ChatID, job.Kind, lastErr,
	)
}

func (s *Service) sendDownloadedVideo(ctx context.Context, job db.GenerationJob, output string) error {
	if !isHTTPURL(output) {
		return fmt.Errorf("output is not http url")
	}
	data, contentType, err := downloadMediaFn(ctx, output)
	if err != nil {
		return err
	}
	fileName := "generated.mp4"
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(ct, "webm") {
		fileName = "generated.webm"
	} else if strings.Contains(ct, "quicktime") {
		fileName = "generated.mov"
	}

	video := tgbotapi.NewVideo(job.ChatID, tgbotapi.FileBytes{Name: fileName, Bytes: data})
	video.Caption = "Готово ✅"
	if _, err := s.Bot.Send(video); err == nil {
		log.Printf(
			"generation: downloaded video sent as video job_id=%d user_id=%d chat_id=%d kind=%s bytes=%d",
			job.ID, job.UserID, job.ChatID, job.Kind, len(data),
		)
		return nil
	} else {
		doc := tgbotapi.NewDocument(job.ChatID, tgbotapi.FileBytes{Name: fileName, Bytes: data})
		doc.Caption = "Готово ✅"
		if _, docErr := s.Bot.Send(doc); docErr == nil {
			log.Printf(
				"generation: downloaded video sent as document job_id=%d user_id=%d chat_id=%d kind=%s bytes=%d",
				job.ID, job.UserID, job.ChatID, job.Kind, len(data),
			)
			return nil
		} else {
			return fmt.Errorf("send downloaded video failed: sendVideo: %v; sendDocument: %v", err, docErr)
		}
	}
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

func shouldDownloadVideoFirst(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Host))
	if !strings.Contains(host, "api.cometapi.com") {
		return false
	}
	p := strings.ToLower(strings.TrimSpace(u.Path))
	return strings.Contains(p, "/v1/videos/") && strings.Contains(p, "/content")
}

func isDataImageURI(raw string) bool {
	candidate := extractDataImageURI(raw)
	return candidate != ""
}

func extractDataImageURI(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	start := strings.Index(lower, "data:image/")
	if start < 0 {
		return ""
	}
	rest := s[start:]
	end := len(rest)
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case ' ', '\n', '\r', '\t', ')', ']', '"', '\'':
			end = i
			goto done
		}
	}
done:
	candidate := strings.TrimSpace(rest[:end])
	if candidate == "" {
		return ""
	}
	return strings.TrimRight(candidate, ".,;:")
}

func decodeDataImageURI(raw string) (string, []byte, error) {
	candidate := extractDataImageURI(raw)
	if candidate == "" {
		return "", nil, errors.New("data image uri not found")
	}

	comma := strings.Index(candidate, ",")
	if comma <= 0 || comma == len(candidate)-1 {
		return "", nil, errors.New("invalid data uri format")
	}
	header := strings.ToLower(strings.TrimSpace(candidate[:comma]))
	if !strings.HasPrefix(header, "data:image/") {
		return "", nil, errors.New("not an image data uri")
	}
	if !strings.Contains(header, ";base64") {
		return "", nil, errors.New("data uri is not base64 encoded")
	}

	enc := strings.TrimSpace(candidate[comma+1:])
	enc = strings.NewReplacer("\n", "", "\r", "", "\t", "", " ", "").Replace(enc)
	blob, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		blob, err = base64.RawStdEncoding.DecodeString(enc)
		if err != nil {
			return "", nil, fmt.Errorf("decode base64: %w", err)
		}
	}
	if len(blob) == 0 {
		return "", nil, errors.New("decoded image is empty")
	}
	if len(blob) > maxInlineImageBytes {
		return "", nil, fmt.Errorf("decoded image too large: %d bytes", len(blob))
	}
	return header, blob, nil
}

func splitPromptInputReferences(raw string) (string, []string) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	lines := strings.Split(raw, "\n")
	kept := make([]string, 0, len(lines))
	inputReferences := make([]string, 0, 1)
	for _, line := range lines {
		if ref, ok := parseInputReferenceLine(line); ok {
			inputReferences = append(inputReferences, ref)
			continue
		}
		kept = append(kept, line)
	}
	prompt := strings.TrimSpace(strings.Join(kept, "\n"))
	return prompt, inputReferences
}

func parseInputReferenceLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "input_reference") {
		return "", false
	}
	tail := strings.TrimSpace(trimmed[len("input_reference"):])
	if tail == "" {
		return "", false
	}
	if tail[0] != ':' && tail[0] != '=' {
		return "", false
	}
	value := strings.TrimSpace(tail[1:])
	value = strings.Trim(value, `"'`)
	value = strings.TrimPrefix(value, "<")
	value = strings.TrimSuffix(value, ">")
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
}

func downloadMediaBytes(ctx context.Context, rawURL string) ([]byte, string, error) {
	ctxFetch, cancel := context.WithTimeout(ctx, mediaFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctxFetch, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "*/*")

	// Comet /v1/videos/{id}/content may require bearer auth and is not
	// fetchable by Telegram directly.
	if strings.Contains(strings.ToLower(rawURL), "api.cometapi.com/v1/videos/") {
		if key := strings.TrimSpace(os.Getenv("COMET_API_KEY")); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}

	client := &http.Client{Timeout: mediaFetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", fmt.Errorf("download media http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	limited := io.LimitReader(resp.Body, maxDownloadedMedia+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", err
	}
	if int64(len(payload)) > maxDownloadedMedia {
		return nil, "", fmt.Errorf("downloaded media exceeds limit: %d bytes", len(payload))
	}
	if len(payload) == 0 {
		return nil, "", errors.New("downloaded media is empty")
	}
	return payload, resp.Header.Get("Content-Type"), nil
}

func generationJobFromClaimed(row db.ClaimNextGenerationJobRow) db.GenerationJob {
	return db.GenerationJob{
		ID:                  row.ID,
		GenerationRequestID: row.GenerationRequestID,
		UserID:              row.UserID,
		ChatID:              row.ChatID,
		ConversationID:      row.ConversationID,
		Kind:                row.Kind,
		Provider:            row.Provider,
		Model:               row.Model,
		Prompt:              row.Prompt,
		Status:              row.Status,
		ResultText:          row.ResultText,
		ErrorMessage:        row.ErrorMessage,
		Attempts:            row.Attempts,
		MaxAttempts:         row.MaxAttempts,
		NextAttemptAt:       row.NextAttemptAt,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
		FinishedAt:          row.FinishedAt,
	}
}

func shortOutput(raw string) string {
	r := strings.TrimSpace(raw)
	if len(r) <= maxProviderOutputSize {
		return r
	}
	return r[:maxProviderOutputSize] + "...(truncated)"
}

func isProviderModerationError(errText string) bool {
	msg := strings.ToLower(strings.TrimSpace(errText))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "blocked by our moderation system") ||
		strings.Contains(msg, "moderation system when checking inputs")
}

func userFailureMessage(errText string, refundPending bool) string {
	if isProviderModerationError(errText) {
		msg := fmt.Sprintf(
			"Промпт не соответствует правилам сервиса. Измените описание и попробуйте снова.\nПравила: %s\nПопытка возвращена.",
			promptRulesURL,
		)
		if refundPending {
			msg = fmt.Sprintf(
				"Промпт не соответствует правилам сервиса. Измените описание и попробуйте снова.\nПравила: %s\nВозврат попытки в обработке, попробуйте позже.",
				promptRulesURL,
			)
		}
		return msg
	}

	if refundPending {
		return "Ошибка генерации медиа. Возврат попытки в обработке, попробуйте позже."
	}
	return "Ошибка генерации медиа. Попытка возвращена."
}
