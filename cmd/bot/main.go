package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	migr "unitool"
	"unitool/internal/admin"
	"unitool/internal/config"
	db "unitool/internal/db/generated"
	"unitool/internal/generation"
	"unitool/internal/health"
	"unitool/internal/logger"
	"unitool/internal/metrics"
	"unitool/internal/moderation"
	"unitool/internal/notifier"
	"unitool/internal/payments"
	"unitool/internal/rate"
	"unitool/internal/retention"
	"unitool/internal/storage"
	"unitool/internal/telegram"
	"unitool/internal/workers"
	cometprov "unitool/pkg/provider/comet"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const webhookUpdateTimeout = 15 * time.Second

type updateDispatchResult struct {
	handled bool
	queued  bool
}

func shouldHandleUpdateSynchronously(upd *tgbotapi.Update) bool {
	return upd != nil && upd.PreCheckoutQuery != nil
}

func dispatchTelegramUpdate(
	upd *tgbotapi.Update,
	process func() error,
	submit func(workers.Job) bool,
) (updateDispatchResult, error) {
	if shouldHandleUpdateSynchronously(upd) {
		if process == nil {
			return updateDispatchResult{}, nil
		}
		return updateDispatchResult{handled: true}, process()
	}
	if submit == nil {
		return updateDispatchResult{}, nil
	}
	if submit(func(context.Context) error {
		if process == nil {
			return nil
		}
		return process()
	}) {
		return updateDispatchResult{queued: true}, nil
	}
	return updateDispatchResult{}, nil
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	logg, logSink, err := logger.New(logger.Options{
		AppEnv:        cfg.AppEnv,
		Level:         cfg.LogLevel,
		FilePath:      cfg.LogFilePath,
		RotateMaxMB:   cfg.LogRotateMaxMB,
		RotateBackups: cfg.LogRotateBackups,
		DedupeWindow:  time.Duration(cfg.LogDedupeWindowMs) * time.Millisecond,
	})
	if err != nil {
		log.Fatal(err)
	}
	if logSink != nil {
		defer func() {
			if cerr := logSink.Close(); cerr != nil {
				log.Printf("logger close failed: %v", cerr)
			}
		}()
	}
	logFilePath := strings.TrimSpace(cfg.LogFilePath)
	logg.Info().
		Str("log_level", cfg.LogLevel).
		Str("log_file_path", logFilePath).
		Bool("file_logging_enabled", logFilePath != "").
		Msg("logger initialized")
	metrics.Serve(cfg.MetricsAddr)
	if cfg.CometKey == "" {
		logg.Fatal().Msg("COMET_API_KEY is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pg, err := storage.New(ctx, cfg.DBURL, cfg.DBMaxConns, cfg.DBMinConns)
	if err != nil {
		logg.Fatal().Err(err).Msg("pg connect")
	}
	defer pg.Close()

	healthSrv := health.New(cfg.HTTPAddr, pg)
	metricsToken := strings.TrimSpace(cfg.MetricsToken)
	if metricsToken == "" {
		logg.Warn().Msg("METRICS_TOKEN is empty; protected /metrics on HTTP_ADDR is disabled")
	} else {
		metricsHandler := metrics.Handler()
		healthSrv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusMethodNotAllowed)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "method must be GET"})
				return
			}
			if !authorizedMetricsRequest(r, metricsToken) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
			metricsHandler.ServeHTTP(w, r)
		})
	}

	bot, err := telegram.New(cfg.TelegramToken)
	if err != nil {
		logg.Fatal().Err(err).Msg("tg bot")
	}

	// Apply schema if enabled
	if cfg.AutoMigrate {
		if err := migr.Apply(ctx, pg.Pool); err != nil {
			logg.Fatal().Err(err).Msg("apply migrations")
		}
	}

	queries := db.New(pg.Pool)
	adminIDs, err := cfg.ParseAdminIDs()
	if err != nil {
		logg.Fatal().Err(err).Msg("parse ADMIN_IDS")
	}
	adminSvc := admin.NewService(queries)
	pay := payments.NewService(bot.API, cfg.ProviderToken, pg.Pool, queries, payments.YooKassaOptions{
		Enabled:   cfg.YooKassaEnabled,
		ShopID:    cfg.YooKassaShopID,
		SecretKey: cfg.YooKassaSecretKey,
		ReturnURL: cfg.YooKassaReturnURL,
		APIBase:   cfg.YooKassaAPIBase,
	})
	// Comet provider (OpenAI-compatible endpoints; minimal wiring)
	comet := cometprov.New(cfg.CometBase, cfg.CometKey, cfg.CometTimeout)
	// Global provider rate limit
	rl := rate.New(cfg.CometBurst, cfg.CometRPS, time.Second)
	editThrottle := time.Duration(cfg.TGEditThrottleMs) * time.Millisecond
	var modClient moderation.Client
	if cfg.ModerationEnabled {
		if cfg.OpenAIKey == "" {
			logg.Fatal().Msg("OPENAI_API_KEY is required when MODERATION_ENABLED=true")
		}
		modClient = moderation.NewOpenAIClient(cfg.OpenAIBase, cfg.OpenAIKey, cfg.ModerationModel, cfg.ModerationTimeout)
	}
	router := telegram.NewRouter(
		bot,
		adminSvc,
		adminIDs,
		pay,
		queries,
		comet,
		rl,
		modClient,
		editThrottle,
		cfg.TGEditMaxPerMin,
		cfg.StartGuideAnimation,
	)
	notifierSvc := notifier.NewService(pg, bot.API)
	router.Notifier = notifierSvc
	notifierSvc.Start(ctx)
	updatePool := workers.NewPool(cfg.QueueBuffer, cfg.MaxWorkers)
	genSvc := generation.NewService(
		bot.API,
		queries,
		comet,
		cfg.JobWorkers,
		time.Duration(cfg.JobPollMs)*time.Millisecond,
		cfg.MediaGenTimeout,
	)
	genSvc.Start(ctx)
	var retentionSvc *retention.Service
	if cfg.RetentionEnabled {
		retentionSvc = retention.NewService(
			pg,
			cfg.RetentionDailyAtUTC,
			cfg.RetentionKeepChatDays,
			cfg.RetentionKeepCreditLedgerDays,
			cfg.RetentionKeepRequestDays,
			cfg.RetentionKeepUpdatesDays,
		)
		retentionSvc.Start(ctx)
	}

	webhookURLStr := strings.TrimSpace(cfg.WebhookURL)
	webhookURL, err := url.Parse(webhookURLStr)
	if err != nil || webhookURL.Scheme == "" || webhookURL.Host == "" {
		logg.Fatal().Msg("WEBHOOK_URL must be a valid absolute URL")
	}
	if webhookURL.Path == "" || webhookURL.Path == "/" {
		logg.Fatal().Msg("WEBHOOK_URL must include a non-root path")
	}
	webhookSecret := strings.TrimSpace(cfg.WebhookSecret)
	if webhookSecret == "" {
		logg.Fatal().Msg("WEBHOOK_SECRET_TOKEN is required")
	}

	healthSrv.HandleFunc("/yookassa/webhook", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "method must be POST"})
			return
		}
		if cfg.WebhookMaxBody > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.WebhookMaxBody)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			var maxBodyErr *http.MaxBytesError
			if errors.As(err, &maxBodyErr) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
			} else {
				w.WriteHeader(http.StatusBadRequest)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if err := pay.HandleYooKassaWebhook(r.Context(), body); err != nil {
			logg.Error().Err(err).Msg("yookassa webhook failed")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "webhook processing failed"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	healthSrv.HandleFunc("/yookassa/return", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Оплата обрабатывается. Вернитесь в Telegram, бот пришлет сообщение после подтверждения платежа."))
	})

	healthSrv.HandleFunc(webhookURL.Path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "method must be POST"})
			return
		}
		recvSecret := strings.TrimSpace(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"))
		if subtle.ConstantTimeCompare([]byte(recvSecret), []byte(webhookSecret)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid webhook secret"})
			return
		}
		if cfg.WebhookMaxBody > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.WebhookMaxBody)
		}
		upd, err := bot.API.HandleUpdate(r)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			var maxBodyErr *http.MaxBytesError
			if errors.As(err, &maxBodyErr) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
			} else {
				w.WriteHeader(http.StatusBadRequest)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		updateID := int64(upd.UpdateID)
		if updateID > 0 {
			inserted, err := queries.BeginUpdateProcessing(r.Context(), db.BeginUpdateProcessingParams{
				UpdateID: updateID,
				Column2:  int32(cfg.WebhookStaleSec),
			})
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to deduplicate update"})
				return
			}
			if inserted == 0 {
				row, gerr := queries.GetTelegramUpdateByID(r.Context(), updateID)
				if gerr != nil {
					logg.Warn().
						Int64("update_id", updateID).
						Err(gerr).
						Msg("webhook duplicate update skipped; state lookup failed")
				} else {
					logg.Info().
						Int64("update_id", updateID).
						Str("status", row.Status).
						Int32("attempt_count", row.AttemptCount).
						Msg("webhook duplicate update skipped")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "duplicate": true})
				return
			}
		}
		processUpdate := func() (jobErr error) {
			markFailed := func(errText string) {
				if updateID <= 0 {
					return
				}
				failCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				rows, err := queries.MarkUpdateFailed(failCtx, db.MarkUpdateFailedParams{
					UpdateID:  updateID,
					LastError: toText(errText),
				})
				if err != nil {
					logg.Error().Err(err).Int64("update_id", updateID).Msg("failed to mark update failed")
					return
				}
				if rows == 0 {
					row, gerr := queries.GetTelegramUpdateByID(failCtx, updateID)
					if gerr != nil {
						logg.Warn().
							Int64("update_id", updateID).
							Str("last_error", errText).
							Err(gerr).
							Msg("mark update failed affected 0 rows; state lookup failed")
						return
					}
					logg.Warn().
						Int64("update_id", updateID).
						Str("status", row.Status).
						Int32("attempt_count", row.AttemptCount).
						Str("last_error", errText).
						Msg("mark update failed affected 0 rows")
				}
			}
			defer func() {
				if rcv := recover(); rcv != nil {
					markFailed(fmt.Sprintf("panic: %v", rcv))
					jobErr = fmt.Errorf("panic while handling update_id=%d: %v", updateID, rcv)
				}
			}()
			updateCtx, updateCancel := context.WithTimeout(ctx, webhookUpdateTimeout)
			defer updateCancel()
			if err := router.HandleUpdate(updateCtx, *upd); err != nil {
				jobErr = fmt.Errorf("handle update failed: %w", err)
				markFailed(jobErr.Error())
				return
			}
			if updateID > 0 {
				doneCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				doneRows, err := queries.MarkUpdateDone(doneCtx, updateID)
				if err != nil {
					cancel()
					jobErr = fmt.Errorf("mark update done failed: %w", err)
					markFailed(jobErr.Error())
					return
				}
				if doneRows == 0 {
					jobErr = fmt.Errorf("mark update done affected 0 rows")
					cancel()
					diagCtx, diagCancel := context.WithTimeout(context.Background(), 3*time.Second)
					row, gerr := queries.GetTelegramUpdateByID(diagCtx, updateID)
					diagCancel()
					if gerr != nil {
						logg.Warn().
							Int64("update_id", updateID).
							Err(gerr).
							Msg("mark update done affected 0 rows; state lookup failed")
					} else {
						logg.Warn().
							Int64("update_id", updateID).
							Str("status", row.Status).
							Int32("attempt_count", row.AttemptCount).
							Str("last_error", row.LastError.String).
							Msg("mark update done affected 0 rows")
					}
					markFailed(jobErr.Error())
					return
				}
				cancel()
			}
			return nil
		}
		dispatchResult, dispatchErr := dispatchTelegramUpdate(upd, processUpdate, updatePool.TrySubmit)
		if dispatchErr != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": dispatchErr.Error()})
			return
		}
		if !dispatchResult.handled && !dispatchResult.queued {
			if updateID > 0 {
				failCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_, _ = queries.MarkUpdateFailed(failCtx, db.MarkUpdateFailedParams{
					UpdateID:  updateID,
					LastError: toText("queue_full"),
				})
				cancel()
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "queue is full, retry later"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	if err := healthSrv.Start(ctx); err != nil {
		logg.Fatal().Err(err).Msg("health server start")
	}

	if err := bot.SetWebhook(webhookURLStr, webhookSecret); err != nil {
		logg.Fatal().Err(err).Msg("set webhook")
	}

	// graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	<-stop
	logg.Info().Msg("shutdown start")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutdownCancel()

	if err := healthSrv.Shutdown(shutdownCtx); err != nil {
		logg.Error().Err(err).Msg("health server shutdown")
	}
	cancel()
	if err := updatePool.StopAndWait(shutdownCtx); err != nil {
		logg.Error().Err(err).Msg("update pool stop")
	}
	if err := genSvc.Wait(shutdownCtx); err != nil {
		logg.Error().Err(err).Msg("generation service wait")
	}
	if err := notifierSvc.Wait(shutdownCtx); err != nil {
		logg.Error().Err(err).Msg("notifier service wait")
	}
	if retentionSvc != nil {
		if err := retentionSvc.Wait(shutdownCtx); err != nil {
			logg.Error().Err(err).Msg("retention service wait")
		}
	}
	healthSrv.Wait()
	logg.Info().Msg("shutdown complete")

	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

func toText(v string) pgtype.Text {
	return pgtype.Text{String: v, Valid: v != ""}
}

func authorizedMetricsRequest(r *http.Request, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" || r == nil {
		return false
	}
	got := strings.TrimSpace(r.Header.Get("Authorization"))
	want := "Bearer " + token
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
