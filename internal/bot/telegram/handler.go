package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"unitool/internal/repo"
)

type paymentsUC interface {
	CreateCheckout(ctx context.Context, tgUserID int64, packageCode string) (uuid.UUID, string, error)
	HandleWebhookSucceeded(ctx context.Context, providerPaymentID string, paidAt time.Time) error
}

type botUC interface {
	SendMessage(chatID int64, text string) error
	SendMessageWithKeyboard(chatID int64, text string, buttons [][]map[string]string) error
	AnswerCallbackQuery(callbackID string, text string) error
	EnsureUser(ctx context.Context, tgID int64, username *string) (*repo.User, error)
}

type Handler struct {
	uc  botUC
	pay paymentsUC
	lg  *log.Logger
}

func NewHandler(uc botUC, pay paymentsUC, lg *log.Logger) *Handler {
	return &Handler{uc: uc, pay: pay, lg: lg}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var upd Update
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		h.lg.Printf("bad update: %v", err)
		w.WriteHeader(400)
		return
	}

	if upd.CallbackQuery != nil && upd.CallbackQuery.Data != "" {
		h.handleCallback(r.Context(), w, upd)
		return
	}

	if upd.Message != nil && strings.TrimSpace(upd.Message.Text) != "" {
		h.handleMessage(r.Context(), w, upd)
		return
	}

	w.WriteHeader(200)
}

func (h *Handler) handleCallback(ctx context.Context, w http.ResponseWriter, upd Update) {
	data := upd.CallbackQuery.Data
	cbID := upd.CallbackQuery.ID
	tgID := upd.CallbackQuery.From.ID
	chatID := upd.CallbackQuery.Message.Chat.ID

	var username *string
	if upd.CallbackQuery.From != nil && upd.CallbackQuery.From.Username != "" {
		name := upd.CallbackQuery.From.Username
		username = &name
	}

	if strings.HasPrefix(data, "BUY:") {
		product := strings.TrimPrefix(data, "BUY:")
		if _, err := h.uc.EnsureUser(ctx, tgID, username); err != nil {
			h.lg.Printf("ensure user: %v", err)
			_ = h.uc.AnswerCallbackQuery(cbID, "Ошибка обработки запроса")
			w.WriteHeader(200)
			return
		}
		_, payURL, err := h.pay.CreateCheckout(ctx, tgID, product)
		if err != nil {
			h.lg.Printf("create checkout: %v", err)
			_ = h.uc.AnswerCallbackQuery(cbID, "Ошибка создания платежа")
			w.WriteHeader(200)
			return
		}
		_ = h.uc.AnswerCallbackQuery(cbID, "Счёт создан")
		_ = h.uc.SendMessageWithKeyboard(chatID,
			"Оплатите заказ по кнопке ниже. После оплаты вернитесь в чат.",
			[][]map[string]string{
				{{"text": "Оплатить", "url": payURL}},
			})
		w.WriteHeader(200)
		return
	}

	w.WriteHeader(200)
}

func (h *Handler) handleMessage(ctx context.Context, w http.ResponseWriter, upd Update) {
	tgID := upd.Message.From.ID
	chatID := upd.Message.Chat.ID
	txt := strings.TrimSpace(upd.Message.Text)

	var username *string
	if upd.Message.From != nil && upd.Message.From.Username != "" {
		name := upd.Message.From.Username
		username = &name
	}

	if txt == "/buy" {
		_ = h.uc.SendMessageWithKeyboard(chatID,
			"Выберите пакет:",
			[][]map[string]string{
				{{"text": "10 запросов — 99 ₽", "callback_data": "BUY:credits_10"}},
				{{"text": "100 запросов — 499 ₽", "callback_data": "BUY:credits_100"}},
			})
		w.WriteHeader(200)
		return
	}

	if txt == "/buy10" || txt == "/buy100" {
		product := map[string]string{"/buy10": "credits_10", "/buy100": "credits_100"}[txt]
		if _, err := h.uc.EnsureUser(ctx, tgID, username); err != nil {
			h.lg.Printf("ensure user: %v", err)
			_ = h.uc.SendMessage(chatID, "Не удалось подготовить пользователя, попробуйте позже.")
			w.WriteHeader(200)
			return
		}
		_, payURL, err := h.pay.CreateCheckout(ctx, tgID, product)
		if err != nil {
			h.lg.Printf("create checkout: %v", err)
			_ = h.uc.SendMessage(chatID, "Не удалось создать платёж. Попробуйте позже.")
		} else {
			_ = h.uc.SendMessageWithKeyboard(chatID, "Оплатите заказ:", [][]map[string]string{
				{{"text": "Оплатить", "url": payURL}},
			})
		}
		w.WriteHeader(200)
		return
	}

	if _, err := h.uc.EnsureUser(ctx, tgID, username); err != nil {
		h.lg.Printf("ensure user: %v", err)
		_ = h.uc.SendMessage(chatID, "Не удалось обработать запрос. Попробуйте позже.")
		w.WriteHeader(200)
		return
	}

	if txt == "/start" {
		_ = h.uc.SendMessage(chatID, "Привет! Я готов помочь. Для покупки пакета нажмите /buy.")
		w.WriteHeader(200)
		return
	}

	_ = h.uc.SendMessage(chatID, fmt.Sprintf("echo: %s", txt))
	w.WriteHeader(200)
}

// --- Telegram Update минимальная модель + callback
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Date      int64  `json:"date"`
	Text      string `json:"text"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Chat struct {
	ID int64 `json:"id"`
}
