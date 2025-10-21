package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yourname/ai-telegram-bot/internal/config"
	"github.com/yourname/ai-telegram-bot/internal/logger"
	"github.com/yourname/ai-telegram-bot/internal/metrics"
	"github.com/yourname/ai-telegram-bot/internal/payments"
	"github.com/yourname/ai-telegram-bot/internal/storage"
	"github.com/yourname/ai-telegram-bot/internal/telegram"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	cfg, err := config.Load()
	if err != nil { log.Fatal(err) }
	logg := logger.New(cfg.AppEnv)
	metrics.Serve(cfg.MetricsAddr)

	ctx := context.Background()
	pg, err := storage.New(ctx, cfg.DBURL)
	if err != nil { logg.Fatal().Err(err).Msg("pg connect") }
	defer pg.Close()

	bot, err := telegram.New(cfg.TelegramToken)
	if err != nil { logg.Fatal().Err(err).Msg("tg bot") }

	pay := payments.NewService(bot.API, cfg.ProviderToken)
	router := telegram.NewRouter(bot, pay)

	// Webhook-less: Long Polling (для старта просто)
	bot.DeleteWebhook()
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 10
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
	_ = http.DefaultClient.CloseIdleConnections
}