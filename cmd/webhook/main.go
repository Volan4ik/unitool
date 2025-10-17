package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/yourorg/ai-telebot/internal/bot/telegram"
	"github.com/yourorg/ai-telebot/internal/infra/config"
	"github.com/yourorg/ai-telebot/internal/infra/httpserver"
	"github.com/yourorg/ai-telebot/internal/infra/logger"
	"github.com/yourorg/ai-telebot/internal/infra/pg"
	"github.com/yourorg/ai-telebot/internal/infra/yookassa"
	"github.com/yourorg/ai-telebot/internal/usecase"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()
	lg := logger.New()

	// DB init
	ctx := context.Background()
	db, err := pg.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		lg.Fatalf("db connect: %v", err)
	}
	defer db.Close()

	// Run migrations on start if enabled (recommended for initContainer or simple deploys)
	if v, _ := strconv.Atoi(os.Getenv("MIGRATE_ON_START")); v == 1 {
		if err := pg.MigrateAll(ctx, db); err != nil {
			lg.Fatalf("migrate: %v", err)
		}
	}

	// repos
	usersRepo := pg.NewUserRepo(db)
	paymentsRepo := pg.NewPaymentsRepo(db)

	// usecases
	botUC := usecase.NewBotUsecase(cfg.BotToken, lg)
	botUC.WithUsers(usersRepo, cfg.DefaultFreeTries)

	// YooKassa client + payments usecase
	yk := yookassa.New(os.Getenv("YOOKASSA_SHOP_ID"), os.Getenv("YOOKASSA_SECRET"))
	payUC := usecase.NewPaymentsUC(paymentsRepo, usersRepo, yk, os.Getenv("RETURN_URL"))

	// transport (Telegram webhook handler с поддержкой /buy)
	h := telegram.NewHandler(botUC, payUC, lg)
	srv := httpserver.New(cfg, h)

	go func() {
		lg.Printf("http listen on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			lg.Fatalf("server error: %v", err)
		}
	}()

	// graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctxSh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctxSh)
	lg.Println("shutdown complete")
}