package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "unitool/internal/config"
    "unitool/internal/logger"
    "unitool/internal/metrics"
    "unitool/internal/payments"
    "unitool/internal/monthly"
    "unitool/internal/storage"
    "unitool/internal/telegram"
    db "unitool/internal/db/generated"
    migr "unitool"
    cometprov "unitool/pkg/provider/comet"
    "unitool/internal/rate"

    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
    cfg, err := config.Load()
    if err != nil { log.Fatal(err) }
    logg := logger.New(cfg.AppEnv)
    metrics.Serve(cfg.MetricsAddr)
    if cfg.CometKey == "" {
        logg.Fatal().Msg("COMET_API_KEY is required")
    }

	ctx := context.Background()
	pg, err := storage.New(ctx, cfg.DBURL)
	if err != nil { logg.Fatal().Err(err).Msg("pg connect") }
	defer pg.Close()

    bot, err := telegram.New(cfg.TelegramToken)
    if err != nil { logg.Fatal().Err(err).Msg("tg bot") }

    // Apply schema if enabled
    if cfg.AutoMigrate {
        if err := migr.Apply(ctx, pg.Pool); err != nil {
            logg.Fatal().Err(err).Msg("apply migrations")
        }
    }

    queries := db.New(pg.Pool)
    pay := payments.NewService(bot.API, cfg.ProviderToken, queries)
    // Comet provider (OpenAI-compatible endpoints; minimal wiring)
    comet := cometprov.New(cfg.CometBase, cfg.CometKey, cfg.CometTimeout)
    // Global provider rate limit
    rl := rate.New(cfg.CometBurst, cfg.CometRPS, time.Second)
    editThrottle := time.Duration(cfg.TGEditThrottleMs) * time.Millisecond
    router := telegram.NewRouter(bot, pay, queries, comet, rl, editThrottle, cfg.TGEditMaxPerMin)

	// Monthly free credits service
	monthlySvc := monthly.NewService(pg, cfg.MonthlyCronAtUTC)
	monthlySvc.Start(ctx)

	// Webhook-less: Long Polling (для старта просто)
	bot.DeleteWebhook()
    u := tgbotapi.NewUpdate(0)
    // Telegram recommends ~50s long-poll timeout
    u.Timeout = 50
	updates := bot.API.GetUpdatesChan(u)

	// graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		for upd := range updates {
			router.HandleUpdate(ctx, upd)
		}
	}()

	<-stop
	logg.Info().Msg("shutdown")
	time.Sleep(300 * time.Millisecond)
    if tr, ok := http.DefaultTransport.(*http.Transport); ok {
        tr.CloseIdleConnections()
    }
}
