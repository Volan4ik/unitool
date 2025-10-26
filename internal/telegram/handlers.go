package telegram

import (
    "context"
    "fmt"
    "time"

    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
    db "unitool/internal/db/generated"
    "unitool/internal/payments"
)

type Router struct {
    Bot *Bot
    Pay *payments.Service
    Q   *db.Queries
}

func NewRouter(b *Bot, p *payments.Service, q *db.Queries) *Router { return &Router{Bot: b, Pay: p, Q: q} }

func (r *Router) HandleUpdate(ctx context.Context, upd tgbotapi.Update) {
    if upd.PreCheckoutQuery != nil { r.Pay.HandlePreCheckout(upd.PreCheckoutQuery); return }
    if m := upd.Message; m != nil {
        if m.SuccessfulPayment != nil { r.Pay.HandleSuccessfulPayment(m); return }
        if m.IsCommand() {
            // Ensure user exists in DB before handling commands
            nctx, userID, err := EnsureUser(ctx, r.Q, m)
            if err != nil {
                r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка авторизации пользователя: %v", err)))
                return
            }
            r.handleCommand(nctx, m, userID)
            return
        }
    }
}

func (r *Router) handleCommand(ctx context.Context, m *tgbotapi.Message, userID int64) {
    switch m.Command() {
    case "start":
        msg := tgbotapi.NewMessage(m.Chat.ID, "Выберите пакет: /buy_starter, /buy_media, /buy_protxt")
        r.Bot.API.Send(msg)
    case "buy_starter":
        r.handleBuy(ctx, m, userID, "starter")
    case "buy_media":
        r.handleBuy(ctx, m, userID, "media")
    case "buy_protxt":
        r.handleBuy(ctx, m, userID, "protxt")
    case "balance":
        // Read userID from context and fetch real balance from DB
        uid, ok := UserIDFromContext(ctx)
        if !ok {
            r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось определить пользователя"))
            return
        }
        bal, err := r.Q.GetBalancesByUserID(ctx, uid)
        if err != nil {
            r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка баланса: %v", err)))
            return
        }
        r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID,
            fmt.Sprintf("Ваш баланс:\n • Текст: %d\n • Фото: %d\n • Видео: %d\n • Поиск: %d",
                bal.TextBalance, bal.ImageBalance, bal.VideoBalance, bal.SearchBalance)))
    default:
        r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Неизвестная команда"))
    }
}

func (r *Router) handleBuy(ctx context.Context, m *tgbotapi.Message, userID int64, code string) {
    pkg, err := r.Q.GetPackageByCode(ctx, code)
    if err != nil {
        r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Пакет не найден: %s", code)))
        return
    }
    p := payments.Package{ID: pkg.ID, Title: pkg.Title, PriceRub: int(pkg.PriceRub)}
    if _, err := r.Pay.SendInvoiceForPackage(m.Chat.ID, userID, p, ""); err != nil {
        r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка оплаты: %v", err)))
        return
    }
}
