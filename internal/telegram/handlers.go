package telegram

import (
	"context"
	"fmt"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/yourname/ai-telegram-bot/internal/payments"
)

type Router struct {
	Bot *Bot
	Pay *payments.Service
}

func NewRouter(b *Bot, p *payments.Service) *Router { return &Router{Bot: b, Pay: p} }

func (r *Router) HandleUpdate(ctx context.Context, upd tgbotapi.Update) {
	if upd.PreCheckoutQuery != nil { r.Pay.HandlePreCheckout(upd.PreCheckoutQuery); return }
	if m := upd.Message; m != nil {
		if m.SuccessfulPayment != nil { r.Pay.HandleSuccessfulPayment(m); return }
		if m.IsCommand() { r.handleCommand(ctx, m); return }
	}
}

func (r *Router) handleCommand(ctx context.Context, m *tgbotapi.Message) {
	switch m.Command() {
	case "start":
		msg := tgbotapi.NewMessage(m.Chat.ID, "Привет! Выбери действие: /buy, /balance, /gen_text")
		r.Bot.API.Send(msg)
	case "buy":
		// Демопакет
		pkg := payments.Package{ID: 1, Title: "Стартовый пакет", PriceRub: 199, TextCredits: 50}
		_, err := r.Pay.SendInvoiceForPackage(m.Chat.ID, pkg, "")
		if err != nil { r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка оплаты: %v", err))) }
	case "balance":
		// TODO: запрос баланса из БД
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Ваш баланс: текст 10, фото 3, видео 1, поиск 10"))
	case "gen_text":
		// TODO: постановка задачи в воркер-пул
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Пришлите промпт для генерации текста"))
	default:
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Неизвестная команда"))
	}
}