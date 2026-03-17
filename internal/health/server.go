package health

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"unitool/internal/storage"
)

type Server struct {
	addr string
	pg   *storage.PG
}

func New(addr string, pg *storage.PG) *Server {
	return &Server{addr: addr, pg: pg}
}

func (s *Server) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.pg.Pool.Ping(ctx); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "not_ready"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	})

	go func() {
		if err := http.ListenAndServe(s.addr, mux); err != nil {
			log.Printf("health server stopped: %v", err)
		}
	}()
}
