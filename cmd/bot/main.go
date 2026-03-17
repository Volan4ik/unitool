package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	migr "unitool"
	"unitool/internal/config"
	db "unitool/internal/db/generated"
	"unitool/internal/generation"
	"unitool/internal/health"
	"unitool/internal/logger"
	"unitool/internal/metrics"
	"unitool/internal/moderation"
	"unitool/internal/payments"
	"unitool/internal/rate"
	"unitool/internal/retention"
	"unitool/internal/storage"
	"unitool/internal/telegram"
	"unitool/internal/weekly"
	"unitool/internal/workers"
	cometprov "unitool/pkg/provider/comet"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	logg := logger.New(cfg.AppEnv)
	metrics.Serve(cfg.MetricsAddr)
	if cfg.CometKey == "" {
		logg.Fatal().Msg("COMET_API_KEY is required")
	}

	ctx := context.Background()
	pg, err := storage.New(ctx, cfg.DBURL, cfg.DBMaxConns, cfg.DBMinConns)
	if err != nil {
		logg.Fatal().Err(err).Msg("pg connect")
	}
	defer pg.Close()

	healthSrv := health.New(cfg.HTTPAddr, pg)

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
	pay := payments.NewService(bot.API, cfg.ProviderToken, pg.Pool, queries)
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
	router := telegram.NewRouter(bot, pay, queries, comet, rl, modClient, editThrottle, cfg.TGEditMaxPerMin)
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

	// Weekly free text generations
	weeklySvc := weekly.NewService(pg, cfg.WeeklyCronAtUTC, cfg.WeeklyTextGenerations)
	weeklySvc.Start(ctx)
	if cfg.RetentionEnabled {
		ret := retention.NewService(
			pg,
			cfg.RetentionDailyAtUTC,
			cfg.RetentionKeepChatDays,
			cfg.RetentionKeepCreditLedgerDays,
			cfg.RetentionKeepRequestDays,
		)
		ret.Start(ctx)
	}

	webhookURLStr := strings.TrimSpace(cfg.WebhookURL)
	webhookURL, err := url.Parse(webhookURLStr)
	if err != nil || webhookURL.Scheme == "" || webhookURL.Host == "" {
		logg.Fatal().Msg("WEBHOOK_URL must be a valid absolute URL")
	}
	if webhookURL.Path == "" || webhookURL.Path == "/" {
		logg.Fatal().Msg("WEBHOOK_URL must include a non-root path")
	}

	healthSrv.HandleFunc(webhookURL.Path, func(w http.ResponseWriter, r *http.Request) {
		upd, err := bot.API.HandleUpdate(r)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if !updatePool.TrySubmit(func(context.Context) error {
			router.HandleUpdate(ctx, *upd)
			return nil
		}) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "queue is full, retry later"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	healthSrv.Start()

	if err := bot.SetWebhook(webhookURLStr); err != nil {
		logg.Fatal().Err(err).Msg("set webhook")
	}

	// graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	<-stop
	logg.Info().Msg("shutdown")
	time.Sleep(300 * time.Millisecond)
	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}
