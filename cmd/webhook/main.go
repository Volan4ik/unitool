package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"unitool/internal/bot/telegram"
	"unitool/internal/infra/config"
	"unitool/internal/infra/httpserver"
	"unitool/internal/infra/logger"
	"unitool/internal/infra/pg"
	"unitool/internal/infra/yookassa"
	"unitool/internal/usecase"
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
	if cfg.MigrateOnStart {
		if err := pg.MigrateAll(ctx, db); err != nil {
			lg.Fatalf("migrate: %v", err)
		}
	}

	// repos
	usersRepo := pg.NewUserRepo(db)
	packagesRepo := pg.NewPackagesRepo(db)
	ordersRepo := pg.NewOrdersRepo(db)
	ledgerRepo := pg.NewLedgerRepo(db)

	// usecases
	botUC := usecase.NewBotUsecase(cfg.BotToken, lg)
	botUC.WithUsers(usersRepo)

	// YooKassa client + payments usecase
	yk := yookassa.New(cfg.YookassaShopID, cfg.YookassaSecret)
	payUC := usecase.NewPaymentsUC(packagesRepo, ordersRepo, ledgerRepo, usersRepo, yk, cfg.ReturnURL)

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
