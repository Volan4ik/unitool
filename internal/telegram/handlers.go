package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "unitool/internal/db/generated"
	"unitool/internal/metrics"
	"unitool/internal/moderation"
	"unitool/internal/payments"
	"unitool/internal/rate"
	"unitool/pkg/provider"
)

type Router struct {
	Bot           *Bot
	Pay           *payments.Service
	Q             *db.Queries
	Prov          provider.ModelProvider
	RL            *rate.Limiter
	Moderation    moderation.Client
	EditThrottle  time.Duration
	EditMaxPerMin int
}

type UserState struct {
	Mode  string // text|image|video
	Model string
}

func NewRouter(b *Bot, p *payments.Service, q *db.Queries, prov provider.ModelProvider, rl *rate.Limiter, mod moderation.Client, editThrottle time.Duration, editMaxPerMin int) *Router {
	return &Router{
		Bot:           b,
		Pay:           p,
		Q:             q,
		Prov:          prov,
		RL:            rl,
		Moderation:    mod,
		EditThrottle:  editThrottle,
		EditMaxPerMin: editMaxPerMin,
	}
}

func (r *Router) HandleUpdate(ctx context.Context, upd tgbotapi.Update) {
	if upd.PreCheckoutQuery != nil {
		r.Pay.HandlePreCheckout(upd.PreCheckoutQuery)
		return
	}
	if upd.CallbackQuery != nil {
		r.handleCallback(ctx, upd.CallbackQuery)
		return
	}
	if m := upd.Message; m != nil {
		if m.SuccessfulPayment != nil {
			r.Pay.HandleSuccessfulPayment(m)
			return
		}
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
	}
}

func (r *Router) handleCommand(ctx context.Context, m *tgbotapi.Message, userID int64) {
	switch m.Command() {
	case "start":
		greet := tgbotapi.NewMessage(m.Chat.ID, "Привет! Выберите действие")
		greet.ReplyMarkup = MainReplyKeyboard()
		r.Bot.API.Send(greet)
	default:
		msg := tgbotapi.NewMessage(m.Chat.ID, "Выберите действие")
		msg.ReplyMarkup = MainReplyKeyboard()
		r.Bot.API.Send(msg)
	}
}

func (r *Router) handleMessage(ctx context.Context, m *tgbotapi.Message, userID int64) {
	txt := strings.TrimSpace(m.Text)
	switch txt {
	case "Сгенерировать текст":
		r.selectMode(ctx, m.Chat.ID, userID, "text")
		return
	case "Создать картинку":
		r.selectMode(ctx, m.Chat.ID, userID, "image")
		return
	case "Создать видео":
		r.selectMode(ctx, m.Chat.ID, userID, "video")
		return
	case "Мой профиль":
		bal, err := r.Q.GetBalancesByUserID(ctx, userID)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка профиля: %v", err)))
			return
		}
		text := fmt.Sprintf("Доступно генераций:\n • Текст: %d\n • Фото: %d\n • Видео: %d\n\nБонус: +100 текстовых генераций каждую неделю.", bal.TextBalance, bal.ImageBalance, bal.VideoBalance)
		msg := tgbotapi.NewMessage(m.Chat.ID, text)
		msg.ReplyMarkup = MainReplyKeyboard()
		buyBtn := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Купить", "buy:menu")))
		r.Bot.API.Send(msg)
		msg2 := tgbotapi.NewMessage(m.Chat.ID, "Вы можете приобрести пакеты:")
		msg2.ReplyMarkup = buyBtn
		r.Bot.API.Send(msg2)
		return
	case "Купить":
		r.showPackages(ctx, m.Chat.ID)
		return
	}

	st, ok, err := r.getState(ctx, userID)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось загрузить состояние пользователя. Попробуйте снова."))
		return
	}
	if !ok || st.Mode == "" || st.Model == "" || txt == "" {
		msg := tgbotapi.NewMessage(m.Chat.ID, "Выберите действие")
		msg.ReplyMarkup = MainReplyKeyboard()
		r.Bot.API.Send(msg)
		return
	}

	kind := st.Mode
	modelID, ok := ResolveModel(kind, st.Model)
	if !ok || modelID == "" {
		msg := tgbotapi.NewMessage(m.Chat.ID, "Выбранная модель не поддерживается. Пожалуйста, выберите другую.")
		msg.ReplyMarkup = MainReplyKeyboard()
		r.Bot.API.Send(msg)
		return
	}

	if kind == "image" || kind == "video" {
		r.handleAsyncMediaPrompt(ctx, m, userID, kind, modelID, txt)
		return
	}
	r.handleSyncPrompt(ctx, m, userID, kind, modelID, txt)
}

func (r *Router) selectMode(ctx context.Context, chatID, userID int64, mode string) {
	if !isSupportedMode(mode) {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Этот режим недоступен."))
		return
	}
	opts := ModelUIList(mode)
	if len(opts) == 0 {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Для этого режима пока нет доступных моделей."))
		return
	}
	def := opts[0]
	if err := r.setState(ctx, userID, UserState{Mode: mode, Model: def}); err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Не удалось сохранить выбор режима. Попробуйте снова."))
		return
	}
	r.sendModelsMenu(chatID, mode, def)
}

func (r *Router) handleSyncPrompt(ctx context.Context, m *tgbotapi.Message, userID int64, kind, modelID, txt string) {
	providerName := "comet"
	if r.RL != nil && !r.RL.Allow() {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Сервис перегружен, попробуйте позже."))
		return
	}

	convID, err := r.ensureConvID(ctx, userID, kind)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось открыть контекст диалога. Попробуйте снова."))
		return
	}

	hist, _ := r.Q.GetLastChatHistory(ctx, db.GetLastChatHistoryParams{
		UserID:         userID,
		ConversationID: pgtype.UUID{Bytes: convID, Valid: true},
		Kind:           kind,
		Column4:        10,
	})
	for i, j := 0, len(hist)-1; i < j; i, j = i+1, j-1 {
		hist[i], hist[j] = hist[j], hist[i]
	}

	gr, err := r.Q.InsertGenerationRequest(ctx, db.InsertGenerationRequestParams{
		UserID:       userID,
		Column2:      kind,
		Provider:     providerName,
		Model:        modelID,
		Status:       "running",
		RequestIDExt: pgtype.Text{},
		PromptHash:   pgtype.Text{},
		InputTokens:  pgtype.Int4{},
	})
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Внутренняя ошибка. Попробуйте позже."))
		return
	}

	if _, err := r.Q.InsertChatMessage(ctx, db.InsertChatMessageParams{
		UserID:              userID,
		ConversationID:      pgtype.UUID{Bytes: convID, Valid: true},
		Kind:                kind,
		Role:                "user",
		ContentText:         pgtype.Text{String: txt, Valid: true},
		AttachmentUrl:       pgtype.Text{},
		Provider:            pgtype.Text{String: providerName, Valid: true},
		Model:               pgtype.Text{String: modelID, Valid: true},
		InputTokens:         pgtype.Int4{},
		OutputTokens:        pgtype.Int4{},
		GenerationRequestID: pgtype.Int8{Int64: gr.ID, Valid: true},
	}); err != nil {
		_ = r.Q.FailGenerationRequest(ctx, db.FailGenerationRequestParams{
			ID:           gr.ID,
			Status:       "failed",
			ErrorMessage: pgtype.Text{String: "insert user message failed", Valid: true},
			LatencyMs:    pgtype.Int4{},
		})
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось сохранить историю. Попробуйте позже."))
		return
	}

	chargeMeta, _ := json.Marshal(map[string]any{"gen_id": gr.ID, "source": "sync_generation"})
	if err := r.chargeOneCredit(ctx, userID, kind, chargeMeta, fmt.Sprintf("spend:gen:%d:%s", gr.ID, kind)); err != nil {
		_ = r.Q.FailGenerationRequest(ctx, db.FailGenerationRequestParams{
			ID:           gr.ID,
			Status:       "failed_balance",
			ErrorMessage: pgtype.Text{String: err.Error(), Valid: true},
			LatencyMs:    pgtype.Int4{},
		})
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Недостаточно генераций для %s", kindToRus(kind))))
		return
	}

	history := make([]provider.Message, 0, len(hist))
	for _, h := range hist {
		if !h.ContentText.Valid {
			continue
		}
		role := h.Role
		if role == "" {
			role = "user"
		}
		history = append(history, provider.Message{Role: role, Content: h.ContentText.String})
	}

	t0 := time.Now()
	ctxGen, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp provider.ModelResponse
	pendingMsgID := 0
	pendingChatID := m.Chat.ID
	if kind == "text" {
		pending, _ := r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "⌛ Генерирую…"))
		pendingMsgID = pending.MessageID
		lastEdit := time.Now()
		var acc strings.Builder
		throttle := r.EditThrottle
		if throttle <= 0 {
			throttle = 900 * time.Millisecond
		}
		edits := 0
		windowStart := time.Now()
		onDelta := func(piece string) error {
			acc.WriteString(piece)
			if time.Since(lastEdit) >= throttle {
				if time.Since(windowStart) >= time.Minute {
					windowStart = time.Now()
					edits = 0
				}
				if r.EditMaxPerMin <= 0 || edits < r.EditMaxPerMin {
					edit := tgbotapi.NewEditMessageText(m.Chat.ID, pending.MessageID, acc.String())
					r.Bot.API.Request(edit)
					lastEdit = time.Now()
					edits++
					metrics.StreamEdits.WithLabelValues(kind).Inc()
				}
			}
			return nil
		}
		resp, err = r.Prov.GenerateStream(ctxGen, provider.ModelRequest{
			UserID:  userID,
			Input:   txt,
			Model:   modelID,
			History: history,
			Params:  map[string]any{"kind": kind},
		}, onDelta)
		if acc.Len() > 0 {
			finalText := acc.String()
			if finalText != "" {
				edit := tgbotapi.NewEditMessageText(m.Chat.ID, pending.MessageID, finalText)
				r.Bot.API.Request(edit)
			}
		}
	} else {
		resp, err = r.Prov.Generate(ctxGen, provider.ModelRequest{
			UserID:  userID,
			Input:   txt,
			Model:   modelID,
			History: history,
			Params:  map[string]any{"kind": kind},
		})
	}

	latency := time.Since(t0).Milliseconds()
	if err != nil {
		_ = r.Q.FailGenerationRequest(ctx, db.FailGenerationRequestParams{
			ID:           gr.ID,
			Status:       "failed",
			ErrorMessage: pgtype.Text{String: err.Error(), Valid: true},
			LatencyMs:    pgtype.Int4{Int32: int32(latency), Valid: true},
		})
		refundMeta, _ := json.Marshal(map[string]any{"gen_id": gr.ID, "source": "sync_generation_failed"})
		_ = r.refundOneCredit(ctx, userID, kind, refundMeta, fmt.Sprintf("refund:gen:%d:%s", gr.ID, kind))
		if pendingMsgID != 0 {
			edit := tgbotapi.NewEditMessageText(pendingChatID, pendingMsgID, "Ошибка генерации. Попытка возвращена.")
			r.Bot.API.Request(edit)
		} else {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Сервис перегружен, попробуйте позже. Попытка возвращена."))
		}
		return
	}

	var cText, cImg, cVid, cSearch pgtype.Int4
	switch kind {
	case "text":
		cText = pgtype.Int4{Int32: 1, Valid: true}
	case "image":
		cImg = pgtype.Int4{Int32: 1, Valid: true}
	case "video":
		cVid = pgtype.Int4{Int32: 1, Valid: true}
	}
	_ = r.Q.FinishGenerationRequest(ctx, db.FinishGenerationRequestParams{
		ID:                gr.ID,
		OutputTokens:      pgtype.Int4{Int32: int32(resp.Tokens), Valid: resp.Tokens > 0},
		LatencyMs:         pgtype.Int4{Int32: int32(latency), Valid: true},
		CostCreditsText:   cText,
		CostCreditsImage:  cImg,
		CostCreditsVideo:  cVid,
		CostCreditsSearch: cSearch,
	})
	if _, err := r.Q.InsertChatMessage(ctx, db.InsertChatMessageParams{
		UserID:              userID,
		ConversationID:      pgtype.UUID{Bytes: convID, Valid: true},
		Kind:                kind,
		Role:                "assistant",
		ContentText:         pgtype.Text{String: resp.Output, Valid: true},
		AttachmentUrl:       pgtype.Text{},
		Provider:            pgtype.Text{String: providerName, Valid: true},
		Model:               pgtype.Text{String: modelID, Valid: true},
		InputTokens:         pgtype.Int4{},
		OutputTokens:        pgtype.Int4{Int32: int32(resp.Tokens), Valid: resp.Tokens > 0},
		GenerationRequestID: pgtype.Int8{Int64: gr.ID, Valid: true},
	}); err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось сохранить ответ в историю."))
	}

	if kind != "text" {
		msg := tgbotapi.NewMessage(m.Chat.ID, resp.Output)
		msg.ReplyMarkup = MainReplyKeyboard()
		r.Bot.API.Send(msg)
	}
}

func (r *Router) handleAsyncMediaPrompt(ctx context.Context, m *tgbotapi.Message, userID int64, kind, modelID, txt string) {
	providerName := "comet"
	if r.Moderation != nil {
		mr, err := r.Moderation.CheckPrompt(ctx, txt)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось проверить промпт модерацией. Попробуйте позже."))
			return
		}
		if !mr.Allowed {
			msg := "Промпт отклонен модерацией. Измените описание и попробуйте снова."
			if len(mr.Reasons) > 0 {
				msg = fmt.Sprintf("Промпт отклонен модерацией (%s). Измените описание и попробуйте снова.", strings.Join(mr.Reasons, ", "))
			}
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, msg))
			return
		}
	}

	convID, err := r.ensureConvID(ctx, userID, kind)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось открыть контекст диалога. Попробуйте снова."))
		return
	}

	gr, err := r.Q.InsertGenerationRequest(ctx, db.InsertGenerationRequestParams{
		UserID:       userID,
		Column2:      kind,
		Provider:     providerName,
		Model:        modelID,
		Status:       "queued",
		RequestIDExt: pgtype.Text{},
		PromptHash:   pgtype.Text{},
		InputTokens:  pgtype.Int4{},
	})
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Внутренняя ошибка. Попробуйте позже."))
		return
	}

	if _, err := r.Q.InsertChatMessage(ctx, db.InsertChatMessageParams{
		UserID:              userID,
		ConversationID:      pgtype.UUID{Bytes: convID, Valid: true},
		Kind:                kind,
		Role:                "user",
		ContentText:         pgtype.Text{String: txt, Valid: true},
		AttachmentUrl:       pgtype.Text{},
		Provider:            pgtype.Text{String: providerName, Valid: true},
		Model:               pgtype.Text{String: modelID, Valid: true},
		InputTokens:         pgtype.Int4{},
		OutputTokens:        pgtype.Int4{},
		GenerationRequestID: pgtype.Int8{Int64: gr.ID, Valid: true},
	}); err != nil {
		_ = r.Q.FailGenerationRequest(ctx, db.FailGenerationRequestParams{
			ID:           gr.ID,
			Status:       "failed",
			ErrorMessage: pgtype.Text{String: "insert user message failed", Valid: true},
			LatencyMs:    pgtype.Int4{},
		})
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось сохранить промпт. Попробуйте позже."))
		return
	}

	chargeMeta, _ := json.Marshal(map[string]any{"gen_id": gr.ID, "source": "async_generation"})
	if err := r.chargeOneCredit(ctx, userID, kind, chargeMeta, fmt.Sprintf("spend:gen:%d:%s", gr.ID, kind)); err != nil {
		_ = r.Q.FailGenerationRequest(ctx, db.FailGenerationRequestParams{
			ID:           gr.ID,
			Status:       "failed_balance",
			ErrorMessage: pgtype.Text{String: err.Error(), Valid: true},
			LatencyMs:    pgtype.Int4{},
		})
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Недостаточно генераций для %s", kindToRus(kind))))
		return
	}

	_, err = r.Q.EnqueueGenerationJob(ctx, db.EnqueueGenerationJobParams{
		GenerationRequestID: gr.ID,
		UserID:              userID,
		ChatID:              m.Chat.ID,
		ConversationID:      pgtype.UUID{Bytes: convID, Valid: true},
		Kind:                kind,
		Provider:            providerName,
		Model:               modelID,
		Prompt:              txt,
		Column9:             nil,
	})
	if err != nil {
		_ = r.Q.FailGenerationRequest(ctx, db.FailGenerationRequestParams{
			ID:           gr.ID,
			Status:       "failed_queue",
			ErrorMessage: pgtype.Text{String: err.Error(), Valid: true},
			LatencyMs:    pgtype.Int4{},
		})
		refundMeta, _ := json.Marshal(map[string]any{"gen_id": gr.ID, "source": "queue_failed"})
		_ = r.refundOneCredit(ctx, userID, kind, refundMeta, fmt.Sprintf("refund:gen:%d:%s", gr.ID, kind))
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось поставить задачу в очередь. Попытка возвращена."))
		return
	}

	r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Задача принята. Начал генерацию, пришлю результат сюда по готовности."))
}

func (r *Router) handleCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) {
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
	if len(parts) == 0 {
		return
	}
	scope := parts[0]
	chatID := cq.Message.Chat.ID
	userTGID := cq.From.ID
	u, err := r.Q.GetUserByTGID(ctx, userTGID)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка пользователя: %v", err)))
		return
	}

	switch scope {
	case "mode":
		if len(parts) < 2 {
			return
		}
		mode := parts[1]
		if !isSupportedMode(mode) {
			return
		}
		list := ModelUIList(mode)
		def := ""
		if len(list) > 0 {
			def = list[0]
		}
		if def != "" {
			_ = r.setState(ctx, u.ID, UserState{Mode: mode, Model: def})
		}
		kb := ModelsInlineKeyboard(mode, def)
		edit := tgbotapi.NewEditMessageReplyMarkup(chatID, cq.Message.MessageID, kb)
		r.Bot.API.Request(edit)
	case "model":
		if len(parts) < 3 {
			return
		}
		mode := parts[1]
		if !isSupportedMode(mode) {
			return
		}
		model := parts[2]
		if _, ok := ResolveModel(mode, model); ok {
			_ = r.setState(ctx, u.ID, UserState{Mode: mode, Model: model})
		}
		kb := ModelsInlineKeyboard(mode, model)
		edit := tgbotapi.NewEditMessageReplyMarkup(chatID, cq.Message.MessageID, kb)
		r.Bot.API.Request(edit)
		note := tgbotapi.NewMessage(chatID, fmt.Sprintf("Напишите промпт для %s — я отвечу в этом чате.", kindToRus(mode)))
		note.ReplyMarkup = MainReplyKeyboard()
		r.Bot.API.Send(note)
	case "buy":
		if len(parts) < 2 {
			return
		}
		code := parts[1]
		if code == "menu" {
			r.showPackages(ctx, chatID)
			return
		}
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
	r.Bot.API.Request(tgbotapi.NewCallback(cq.ID, ""))
}

func (r *Router) sendModelsMenu(chatID int64, mode, selected string) {
	kb := ModelsInlineKeyboard(mode, selected)
	msg := tgbotapi.NewMessage(chatID, "Выберите модель")
	msg.ReplyMarkup = MainReplyKeyboard()
	r.Bot.API.Send(msg)
	msg2 := tgbotapi.NewMessage(chatID, "Модели:")
	msg2.ReplyMarkup = kb
	r.Bot.API.Send(msg2)
}

func (r *Router) showPackages(ctx context.Context, chatID int64) {
	pkgs, err := r.Q.ListActivePackages(ctx)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Не удалось загрузить пакеты. Попробуйте позже."))
		return
	}
	if len(pkgs) == 0 {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Сейчас нет доступных пакетов."))
		return
	}

	lines := make([]string, 0, len(pkgs))
	codes := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		lines = append(lines, fmt.Sprintf("• %s — %s (%d ₽)", p.Code, p.Title, p.PriceRub))
		codes = append(codes, p.Code)
	}

	text := "Выберите пакет:\n" + strings.Join(lines, "\n")
	kb := PackagesInlineKeyboard(codes)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = MainReplyKeyboard()
	r.Bot.API.Send(msg)
	msg2 := tgbotapi.NewMessage(chatID, "Пакеты:")
	msg2.ReplyMarkup = kb
	r.Bot.API.Send(msg2)
}

func (r *Router) ensureConvID(ctx context.Context, userID int64, kind string) (uuid.UUID, error) {
	candidate := uuid.New()
	conv, err := r.Q.UpsertConversation(ctx, db.UpsertConversationParams{
		UserID: userID,
		Kind:   kind,
		ConversationID: pgtype.UUID{
			Bytes: candidate,
			Valid: true,
		},
	})
	if err != nil {
		return uuid.Nil, err
	}
	if !conv.Valid {
		return uuid.Nil, errors.New("conversation id is invalid")
	}
	return conv.Bytes, nil
}

func (r *Router) setState(ctx context.Context, userID int64, st UserState) error {
	return r.Q.UpsertUserSession(ctx, db.UpsertUserSessionParams{
		UserID: userID,
		Mode:   st.Mode,
		Model:  st.Model,
	})
}

func (r *Router) getState(ctx context.Context, userID int64) (UserState, bool, error) {
	row, err := r.Q.GetUserSession(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserState{}, false, nil
		}
		return UserState{}, false, err
	}
	return UserState{Mode: row.Mode, Model: row.Model}, true, nil
}

func (r *Router) chargeOneCredit(ctx context.Context, userID int64, kind string, meta []byte, opKey string) error {
	switch kind {
	case "text":
		return r.Q.SpendText(ctx, db.SpendTextParams{UserID: userID, Meta: meta, OpKey: pgtype.Text{String: opKey, Valid: true}})
	case "image":
		return r.Q.SpendImage(ctx, db.SpendImageParams{UserID: userID, Meta: meta, OpKey: pgtype.Text{String: opKey, Valid: true}})
	case "video":
		return r.Q.SpendVideo(ctx, db.SpendVideoParams{UserID: userID, Meta: meta, OpKey: pgtype.Text{String: opKey, Valid: true}})
	default:
		return fmt.Errorf("unknown charge kind: %s", kind)
	}
}

func (r *Router) refundOneCredit(ctx context.Context, userID int64, kind string, meta []byte, opKey string) error {
	switch kind {
	case "text":
		return r.Q.RefundText(ctx, db.RefundTextParams{UserID: userID, Meta: meta, OpKey: pgtype.Text{String: opKey, Valid: true}})
	case "image":
		return r.Q.RefundImage(ctx, db.RefundImageParams{UserID: userID, Meta: meta, OpKey: pgtype.Text{String: opKey, Valid: true}})
	case "video":
		return r.Q.RefundVideo(ctx, db.RefundVideoParams{UserID: userID, Meta: meta, OpKey: pgtype.Text{String: opKey, Valid: true}})
	default:
		return fmt.Errorf("unknown refund kind: %s", kind)
	}
}

func kindToRus(kind string) string {
	switch kind {
	case "text":
		return "текста"
	case "image":
		return "картинки"
	case "video":
		return "видео"
	default:
		return "контента"
	}
}

func isSupportedMode(mode string) bool {
	return mode == "text" || mode == "image" || mode == "video"
}
