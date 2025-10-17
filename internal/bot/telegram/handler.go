package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

type paymentsUC interface {
	CreateSBPPayment(ctx context.Context, userID int64, productID string) (paymentID int64, payURL string, err error)
	HandleWebhookSucceeded(ctx context.Context, providerPaymentID string, paidAt time.Time) error
}

type botUC interface {
	SendMessage(chatID int64, text string) error
	SendMessageWithKeyboard(chatID int64, text string, buttons [][]map[string]string) error
	AnswerCallbackQuery(callbackID string, text string) error
	EnsureUser(ctx context.Context, tgID int64) error
	TryConsumeFree(ctx context.Context, tgID int64) (int, error)
}

type Handler struct {
	uc  botUC
	pay paymentsUC
	lg  *log.Logger
}

func NewHandler(uc botUC, pay paymentsUC, lg *log.Logger) *Handler { return &Handler{uc: uc, pay: pay, lg: lg} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { w.WriteHeader(405); return }
	var upd Update
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		h.lg.Printf("bad update: %v", err)
		w.WriteHeader(400)
		return
	}

	// Callback: покупка пакета
	if upd.CallbackQuery != nil && upd.CallbackQuery.Data != "" {
		data := upd.CallbackQuery.Data
		cbID := upd.CallbackQuery.ID
		tgID := upd.CallbackQuery.From.ID
		chatID := upd.CallbackQuery.Message.Chat.ID

		if strings.HasPrefix(data, "BUY:") {
			product := strings.TrimPrefix(data, "BUY:")
			_, payURL, err := h.pay.CreateSBPPayment(r.Context(), tgID, product)
			if err != nil {
				_ = h.uc.AnswerCallbackQuery(cbID, "Ошибка создания платежа")
			} else {
				_ = h.uc.AnswerCallbackQuery(cbID, "Счёт создан")
				_ = h.uc.SendMessageWithKeyboard(chatID,
					"Оплатите заказ по кнопке ниже. После оплаты вернитесь в чат.",
					[][]map[string]string{
						{{"text":"Оплатить","url":payURL}},
					})
			}
			w.WriteHeader(200); return
		}
	}

	if upd.Message != nil && upd.Message.Text != "" {
		tgID := upd.Message.From.ID
		chatID := upd.Message.Chat.ID
		txt := strings.TrimSpace(upd.Message.Text)

		if txt == "/buy" {
			_ = h.uc.SendMessageWithKeyboard(chatID,
				"Выберите пакет:",
				[][]map[string]string{
					{{"text":"10 запросов — 99 ₽","callback_data":"BUY:credits_10"}},
					{{"text":"100 запросов — 499 ₽","callback_data":"BUY:credits_100"}},
				})
			w.WriteHeader(200); return
		}
		// Дополнительно: короткие команды /buy10 /buy100
		if txt == "/buy10" || txt == "/buy100" {
			product := map[string]string{"/buy10":"credits_10","/buy100":"credits_100"}[txt]
			_, payURL, err := h.pay.CreateSBPPayment(r.Context(), tgID, product)
			if err != nil {
				_ = h.uc.SendMessage(chatID, "Не удалось создать платёж. Попробуйте позже.")
			} else {
				_ = h.uc.SendMessageWithKeyboard(chatID, "Оплатите заказ:", [][]map[string]string{
					{{"text":"Оплатить","url":payURL}},
				})
			}
			w.WriteHeader(200); return
		}

		// обычная логика free tries
		_ = h.uc.EnsureUser(r.Context(), tgID)
		left, _ := h.uc.TryConsumeFree(r.Context(), tgID)
		if left == 0 {
			_ = h.uc.SendMessage(chatID, "Бесплатные попытки закончились. Наберите /buy")
		} else if left > 0 {
			_ = h.uc.SendMessage(chatID, fmt.Sprintf("echo: %s\nОсталось бесплатных попыток: %d", txt, left))
		} else {
			_ = h.uc.SendMessage(chatID, "echo: "+txt)
		}
	}

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
	ID      string  `json:"id"`
	From    *User   `json:"from"`
	Message *Message `json:"message"`
	Data    string  `json:"data"`
}
type User struct { ID int64 `json:"id"` }
type Chat struct { ID int64 `json:"id"` }