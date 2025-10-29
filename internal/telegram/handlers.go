package telegram

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"
    "sync"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgtype"
    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
    db "unitool/internal/db/generated"
    "unitool/internal/payments"
)

type Router struct {
    Bot    *Bot
    Pay    *payments.Service
    Q      *db.Queries
    mu     sync.RWMutex
    States map[int64]UserState
    State  *ConversationState
}

type UserState struct {
    Mode  string // text|search|image|video
    Model string
}

type ConversationState struct {
    mu   sync.RWMutex
    Conv map[string]uuid.UUID // key: fmt.Sprintf("%d:%s", userID, kind)
}

func NewRouter(b *Bot, p *payments.Service, q *db.Queries) *Router {
    return &Router{Bot: b, Pay: p, Q: q, States: make(map[int64]UserState), State: &ConversationState{Conv: make(map[string]uuid.UUID)}}
}

func (r *Router) HandleUpdate(ctx context.Context, upd tgbotapi.Update) {
    if upd.PreCheckoutQuery != nil { r.Pay.HandlePreCheckout(upd.PreCheckoutQuery); return }
    if upd.CallbackQuery != nil { r.handleCallback(ctx, upd.CallbackQuery); return }
    if m := upd.Message; m != nil {
        if m.SuccessfulPayment != nil { r.Pay.HandleSuccessfulPayment(m); return }
        // Ensure user exists before anything
        nctx, userID, err := EnsureUser(ctx, r.Q, m)
        if err != nil {
            r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка авторизации пользователя: %v", err)))
            return
        }
        if m.IsCommand() {
            r.handleCommand(nctx, m, userID)
            return
        }
        r.handleMessage(nctx, m, userID)
        return
    }
}

func (r *Router) handleCommand(ctx context.Context, m *tgbotapi.Message, userID int64) {
    switch m.Command() {
    case "start":
        greet := tgbotapi.NewMessage(m.Chat.ID, "Привет! Выберите действие")
        kb := MainReplyKeyboard()
        greet.ReplyMarkup = kb
        r.Bot.API.Send(greet)
    default:
        // For other commands just show main menu
        msg := tgbotapi.NewMessage(m.Chat.ID, "Выберите действие")
        msg.ReplyMarkup = MainReplyKeyboard()
        r.Bot.API.Send(msg)
    }
}

func (r *Router) handleMessage(ctx context.Context, m *tgbotapi.Message, userID int64) {
    txt := strings.TrimSpace(m.Text)
    switch txt {
    case "Сгенерировать текст":
        r.setState(userID, UserState{Mode: "text", Model: "GPT-5"})
        r.sendModelsMenu(m.Chat.ID, "text", "GPT-5")
        return
    case "Интернет-поиск":
        r.setState(userID, UserState{Mode: "search", Model: "Perplexity"})
        r.sendModelsMenu(m.Chat.ID, "search", "Perplexity")
        return
    case "Создать картинку":
        r.setState(userID, UserState{Mode: "image", Model: "Flux"})
        r.sendModelsMenu(m.Chat.ID, "image", "Flux")
        return
    case "Создать видео":
        r.setState(userID, UserState{Mode: "video", Model: "Sora"})
        r.sendModelsMenu(m.Chat.ID, "video", "Sora")
        return
    case "Создать песню":
        msg := tgbotapi.NewMessage(m.Chat.ID, "Функция пока в разработке")
        msg.ReplyMarkup = MainReplyKeyboard()
        r.Bot.API.Send(msg)
        return
    case "Мой профиль":
        bal, err := r.Q.GetBalancesByUserID(ctx, userID)
        if err != nil {
            r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка баланса: %v", err)))
            return
        }
        text := fmt.Sprintf("Ваш баланс:\n • Текст: %d\n • Фото: %d\n • Видео: %d\n • Поиск: %d", bal.TextBalance, bal.ImageBalance, bal.VideoBalance, bal.SearchBalance)
        msg := tgbotapi.NewMessage(m.Chat.ID, text)
        msg.ReplyMarkup = MainReplyKeyboard()
        buyBtn := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Купить", "buy:menu")))
        r.Bot.API.Send(msg)
        // Attach inline below with a separate message to preserve reply keyboard
        msg2 := tgbotapi.NewMessage(m.Chat.ID, "Вы можете приобрести пакеты:")
        msg2.ReplyMarkup = buyBtn
        r.Bot.API.Send(msg2)
        return
    case "Купить":
        r.showPackages(m.Chat.ID)
        return
    }

    // Treat as prompt if mode/model selected
    st, ok := r.getState(userID)
    if ok && st.Mode != "" && st.Model != "" && txt != "" {
        kind := st.Mode
        provider := "mock"
        model := st.Model
        // Check balance per kind
        bal, err := r.Q.GetBalancesByUserID(ctx, userID)
        if err != nil {
            r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка баланса: %v", err)))
            return
        }
        lack := false
        var kindRusShort string
        switch kind {
        case "text":
            lack = bal.TextBalance <= 0
            kindRusShort = "текста"
        case "search":
            lack = bal.SearchBalance <= 0
            kindRusShort = "поиска"
        case "image":
            lack = bal.ImageBalance <= 0
            kindRusShort = "картинок"
        case "video":
            lack = bal.VideoBalance <= 0
            kindRusShort = "видео"
        }
        if lack {
            r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Недостаточно попыток для %s", kindRusShort)))
            return
        }

        convID := r.ensureConvID(userID, kind)
        // Load history, limit 10
        hist, _ := r.Q.GetLastChatHistory(ctx, db.GetLastChatHistoryParams{
            UserID:        userID,
            ConversationID: pgtype.UUID{Bytes: convID, Valid: true},
            Kind:           kind,
            Column4:        10,
        })
        // reverse to old->new
        for i, j := 0, len(hist)-1; i < j; i, j = i+1, j-1 {
            hist[i], hist[j] = hist[j], hist[i]
        }

        // Create generation request running
        gr, _ := r.Q.InsertGenerationRequest(ctx, db.InsertGenerationRequestParams{
            UserID:      userID,
            Kind:        kind,
            Provider:    provider,
            Model:       model,
            Status:      "running",
            RequestIDExt: pgtype.Text{},
            PromptHash:   pgtype.Text{},
            InputTokens:  pgtype.Int4{},
        })
        t0 := time.Now()
        // Log user message
        _, _ = r.Q.InsertChatMessage(ctx, db.InsertChatMessageParams{
            UserID:             userID,
            ConversationID:     pgtype.UUID{Bytes: convID, Valid: true},
            Kind:               kind,
            Role:               "user",
            ContentText:        pgtype.Text{String: txt, Valid: true},
            AttachmentUrl:      pgtype.Text{},
            Provider:           pgtype.Text{String: provider, Valid: true},
            Model:              pgtype.Text{String: model, Valid: true},
            InputTokens:        pgtype.Int4{},
            OutputTokens:       pgtype.Int4{},
            GenerationRequestID: pgtype.Int8{Int64: gr.ID, Valid: true},
        })

        // Mock provider
        var out string
        switch kind {
        case "text", "search":
            out = "Эхо: " + txt
        case "image", "video":
            out = "Сгенерировано (заглушка) для " + model + ": " + txt
        }
        latency := time.Since(t0).Milliseconds()

        // Success: finish + insert assistant message + spend 1 credit
        _ = r.Q.FinishGenerationRequest(ctx, db.FinishGenerationRequestParams{
            ID:                gr.ID,
            OutputTokens:      pgtype.Int4{Int32: 0, Valid: true},
            LatencyMs:         pgtype.Int4{Int32: int32(latency), Valid: true},
            CostCreditsText:   func() pgtype.Int4 { if kind=="text" {return pgtype.Int4{Int32:1,Valid:true}}; return pgtype.Int4{} }(),
            CostCreditsImage:  func() pgtype.Int4 { if kind=="image" {return pgtype.Int4{Int32:1,Valid:true}}; return pgtype.Int4{} }(),
            CostCreditsVideo:  func() pgtype.Int4 { if kind=="video" {return pgtype.Int4{Int32:1,Valid:true}}; return pgtype.Int4{} }(),
            CostCreditsSearch: func() pgtype.Int4 { if kind=="search" {return pgtype.Int4{Int32:1,Valid:true}}; return pgtype.Int4{} }(),
        })
        _, _ = r.Q.InsertChatMessage(ctx, db.InsertChatMessageParams{
            UserID:             userID,
            ConversationID:     pgtype.UUID{Bytes: convID, Valid: true},
            Kind:               kind,
            Role:               "assistant",
            ContentText:        pgtype.Text{String: out, Valid: true},
            AttachmentUrl:      pgtype.Text{},
            Provider:           pgtype.Text{String: provider, Valid: true},
            Model:              pgtype.Text{String: model, Valid: true},
            InputTokens:        pgtype.Int4{},
            OutputTokens:       pgtype.Int4{},
            GenerationRequestID: pgtype.Int8{Int64: gr.ID, Valid: true},
        })
        meta, _ := json.Marshal(map[string]any{"gen_id": gr.ID})
        switch kind {
        case "text":
            _ = r.Q.SpendText(ctx, db.SpendTextParams{UserID: userID, Meta: meta})
        case "search":
            _ = r.Q.SpendSearch(ctx, db.SpendSearchParams{UserID: userID, Meta: meta})
        case "image":
            _ = r.Q.SpendImage(ctx, db.SpendImageParams{UserID: userID, Meta: meta})
        case "video":
            _ = r.Q.SpendVideo(ctx, db.SpendVideoParams{UserID: userID, Meta: meta})
        }

        // Respond to user
        msg := tgbotapi.NewMessage(m.Chat.ID, out)
        msg.ReplyMarkup = MainReplyKeyboard()
        r.Bot.API.Send(msg)
        return
    }

    // Fallback
    msg := tgbotapi.NewMessage(m.Chat.ID, "Выберите действие")
    msg.ReplyMarkup = MainReplyKeyboard()
    r.Bot.API.Send(msg)
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

func (r *Router) handleCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) {
    // Ensure user
    // Upsert directly from callback user
    if cq.From != nil {
        pseudoMsg := &tgbotapi.Message{From: &tgbotapi.User{ID: cq.From.ID, UserName: cq.From.UserName, FirstName: cq.From.FirstName, LastName: cq.From.LastName, LanguageCode: cq.From.LanguageCode}, Chat: &tgbotapi.Chat{ID: cq.Message.Chat.ID}}
        var err error
        ctx, _, err = EnsureUser(ctx, r.Q, pseudoMsg)
        if err != nil {
            r.Bot.API.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, fmt.Sprintf("Ошибка авторизации пользователя: %v", err)))
            return
        }
    }

    data := cq.Data
    parts := strings.Split(data, ":")
    if len(parts) == 0 { return }
    scope := parts[0]
    chatID := cq.Message.Chat.ID
    userTGID := cq.From.ID
    // fetch internal user id
    u, err := r.Q.GetUserByTGID(ctx, userTGID)
    if err != nil {
        r.Bot.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка пользователя: %v", err)))
        return
    }

    switch scope {
    case "mode":
        if len(parts) < 2 { return }
        mode := parts[1]
        def := map[string]string{"text": "GPT-5", "search": "Perplexity", "image": "Flux", "video": "Sora"}[mode]
        r.setState(u.ID, UserState{Mode: mode, Model: def})
        // edit inline keyboard on the same message
        kb := ModelsInlineKeyboard(mode, def)
        edit := tgbotapi.NewEditMessageReplyMarkup(chatID, cq.Message.MessageID, kb)
        r.Bot.API.Request(edit)
    case "model":
        if len(parts) < 3 { return }
        mode := parts[1]
        model := parts[2]
        r.setState(u.ID, UserState{Mode: mode, Model: model})
        kb := ModelsInlineKeyboard(mode, model)
        edit := tgbotapi.NewEditMessageReplyMarkup(chatID, cq.Message.MessageID, kb)
        r.Bot.API.Request(edit)
        kindRus := map[string]string{"text": "текста", "search": "поиска", "image": "картинки", "video": "видео"}
        note := tgbotapi.NewMessage(chatID, fmt.Sprintf("Напишите промпт для %s — я отвечу в этом чате.", kindRus[mode]))
        note.ReplyMarkup = MainReplyKeyboard()
        r.Bot.API.Send(note)
    case "buy":
        if len(parts) < 2 { return }
        code := parts[1]
        if code == "menu" {
            r.showPackages(chatID)
            return
        }
        // create invoice
        pkg, err := r.Q.GetPackageByCode(ctx, code)
        if err != nil {
            r.Bot.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Пакет не найден: %s", code)))
            return
        }
        p := payments.Package{ID: pkg.ID, Title: pkg.Title, PriceRub: int(pkg.PriceRub)}
        if _, err := r.Pay.SendInvoiceForPackage(chatID, u.ID, p, ""); err != nil {
            r.Bot.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка оплаты: %v", err)))
            return
        }
    }
    // Answer callback to remove loading indicator
    r.Bot.API.Request(tgbotapi.NewCallback(cq.ID, ""))
}

func (r *Router) sendModelsMenu(chatID int64, mode, selected string) {
    kb := ModelsInlineKeyboard(mode, selected)
    msg := tgbotapi.NewMessage(chatID, "Выберите модель")
    msg.ReplyMarkup = MainReplyKeyboard()
    // we send inline keyboard in a separate message without overriding reply keyboard
    r.Bot.API.Send(msg)
    msg2 := tgbotapi.NewMessage(chatID, "Модели:")
    msg2.ReplyMarkup = kb
    r.Bot.API.Send(msg2)
}

func (r *Router) showPackages(chatID int64) {
    text := "Выберите пакет:\n• starter — Стартовый пакет\n• media — Фото+Видео\n• protxt — Текст PRO"
    kb := PackagesInlineKeyboard([]string{"starter", "media", "protxt"})
    msg := tgbotapi.NewMessage(chatID, text)
    msg.ReplyMarkup = MainReplyKeyboard()
    r.Bot.API.Send(msg)
    msg2 := tgbotapi.NewMessage(chatID, "Пакеты:")
    msg2.ReplyMarkup = kb
    r.Bot.API.Send(msg2)
}

func (r *Router) ensureConvID(userID int64, kind string) uuid.UUID {
    key := fmt.Sprintf("%d:%s", userID, kind)
    r.State.mu.RLock()
    id, ok := r.State.Conv[key]
    r.State.mu.RUnlock()
    if ok { return id }
    nid := uuid.New()
    r.State.mu.Lock()
    r.State.Conv[key] = nid
    r.State.mu.Unlock()
    return nid
}

func (r *Router) setState(userID int64, st UserState) {
    r.mu.Lock(); defer r.mu.Unlock()
    r.States[userID] = st
}

func (r *Router) getState(userID int64) (UserState, bool) {
    r.mu.RLock(); defer r.mu.RUnlock()
    st, ok := r.States[userID]
    return st, ok
}
