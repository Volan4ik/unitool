package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"unitool/internal/infra/config"
	"unitool/internal/infra/httpserver"
	"unitool/internal/infra/logger"
	"unitool/internal/bot/telegram"
	"unitool/internal/usecase"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()
	lg := logger.New()

	botUC := usecase.NewBotUsecase(cfg.BotToken, lg)
	h := telegram.NewHandler(botUC, lg)

	srv := httpserver.New(cfg, h)

	go func() {
		lg.Printf("http listen on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			lg.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	lg.Println("shutdown complete")
}