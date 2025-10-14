package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"unitool/internal/infra/config"
)

type Handler interface{ http.Handler }

func New(cfg config.Config, bot Handler) *http.Server {
	r := chi.NewRouter()

	// health
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); w.Write([]byte("ok")) })

	// Telegram webhook endpoint
	r.Mount("/tg", bot)

	return &http.Server{ Addr: ":"+cfg.Port, Handler: r }
}