package telegram

import (
	"encoding/json"
	"log"
	"net/http"
)

type botUC interface {
	SendMessage(chatID int64, text string) error
}

type Handler struct {
	uc botUC
	lg *log.Logger
}

func NewHandler(uc botUC, lg *log.Logger) *Handler { return &Handler{uc: uc, lg: lg} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { w.WriteHeader(405); return }
	var upd Update
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		h.lg.Printf("bad update: %v", err)
		w.WriteHeader(400)
		return
	}
	// Минимальный эхо-флоу
	if upd.Message != nil && upd.Message.Text != "" {
		_ = h.uc.SendMessage(upd.Message.Chat.ID, "echo: "+upd.Message.Text)
	}
	w.WriteHeader(200)
}

// --- Telegram Update минимальная модель

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	MessageID int64    `json:"message_id"`
	From      *User    `json:"from"`
	Chat      Chat     `json:"chat"`
	Date      int64    `json:"date"`
	Text      string   `json:"text"`
}

type User struct { ID int64 `json:"id"` }

type Chat struct { ID int64 `json:"id"` }