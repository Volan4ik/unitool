package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"unitool/internal/admin"
	db "unitool/internal/db/generated"
	"unitool/internal/metrics"
	"unitool/internal/moderation"
	"unitool/internal/payments"
	"unitool/internal/rate"
	"unitool/pkg/provider"
)

type Router struct {
	Bot           *Bot
	Admin         *admin.Service
	Pay           *payments.Service
	Q             *db.Queries
	Notifier      BroadcastScheduler
	Prov          provider.ModelProvider
	RL            *rate.Limiter
	Moderation    moderation.Client
	EditThrottle  time.Duration
	EditMaxPerMin int

	adminIDsMu sync.RWMutex
	adminIDs   map[int64]struct{}

	adminFlowMu sync.Mutex
	adminFlow   map[int64]adminFlowState
}

type UserState struct {
	Mode  string // image|video
	Model string
}

type adminFlowState struct {
	Action string
}

type BroadcastScheduler interface {
	EnqueueBroadcast(ctx context.Context, createdByTGID int64, text string) (int64, error)
}

const (
	tgMessageLimit     = 4096
	tgEditPreviewLimit = 4000
	defaultSourceTag   = "organic"
	maxSourceTagLen    = 64
	supportContact     = "@helpper"
)

var errEmptyModelResponse = errors.New("empty model response")

func NewRouter(
	b *Bot,
	adminSvc *admin.Service,
	adminIDs []int64,
	p *payments.Service,
	q *db.Queries,
	prov provider.ModelProvider,
	rl *rate.Limiter,
	mod moderation.Client,
	editThrottle time.Duration,
	editMaxPerMin int,
) *Router {
	idMap := make(map[int64]struct{}, len(adminIDs))
	for _, id := range adminIDs {
		idMap[id] = struct{}{}
	}
	return &Router{
		Bot:           b,
		Admin:         adminSvc,
		Pay:           p,
		Q:             q,
		Prov:          prov,
		RL:            rl,
		Moderation:    mod,
		EditThrottle:  editThrottle,
		EditMaxPerMin: editMaxPerMin,
		adminIDs:      idMap,
		adminFlow:     make(map[int64]adminFlowState),
	}
}

func (r *Router) HandleUpdate(ctx context.Context, upd tgbotapi.Update) error {
	if upd.UpdateID > 0 {
		ctx = withUpdateID(ctx, int64(upd.UpdateID))
	}
	if upd.PreCheckoutQuery != nil {
		return r.Pay.HandlePreCheckout(ctx, upd.PreCheckoutQuery)
	}
	if upd.CallbackQuery != nil {
		return r.handleCallback(ctx, upd.CallbackQuery)
	}
	if m := upd.Message; m != nil {
		userID, err := EnsureUser(ctx, r.Q, m)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Ошибка авторизации пользователя: %v", err)))
			return err
		}
		if m.SuccessfulPayment != nil {
			return r.Pay.HandleSuccessfulPayment(ctx, m)
		}
		if m.From != nil && !r.isAdminTGID(m.From.ID) {
			banned, berr := r.isUserBanned(ctx, m.From.ID)
			if berr != nil {
				log.Printf("check banned failed tg_id=%d err=%v", m.From.ID, berr)
				return berr
			}
			if banned {
				r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Ваш аккаунт заблокирован. Обратитесь к администратору."))
				return nil
			}
		}
		if m.IsCommand() {
			return r.handleCommand(ctx, m, userID)
		}
		return r.handleMessage(ctx, m, userID)
	}
	return nil
}

func (r *Router) handleCommand(ctx context.Context, m *tgbotapi.Message, userID int64) error {
	switch m.Command() {
	case "start":
		if err := r.trackStartSource(ctx, userID, m); err != nil {
			tgID := int64(0)
			if m.From != nil {
				tgID = m.From.ID
			}
			log.Printf("track start source failed user_id=%d tg_id=%d err=%v", userID, tgID, err)
		}
		greet := tgbotapi.NewMessage(m.Chat.ID, startGreetingText(m))
		greet.ParseMode = tgbotapi.ModeHTML
		greet.DisableWebPagePreview = true
		greet.ReplyMarkup = MainReplyKeyboard()
		if _, err := r.Bot.API.Send(greet); err != nil {
			return err
		}
	case "help":
		return r.sendHelpMessage(m.Chat.ID)
	case "admin":
		return r.handleAdminCommand(ctx, m)
	default:
		msg := tgbotapi.NewMessage(m.Chat.ID, "Выберите действие")
		msg.ReplyMarkup = MainReplyKeyboard()
		if _, err := r.Bot.API.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

func startGreetingText(m *tgbotapi.Message) string {
	name := ""
	if m != nil && m.From != nil {
		name = strings.TrimSpace(m.From.FirstName)
	}
	if name != "" {
		name = html.EscapeString(name) + ", добро пожаловать!"
	} else {
		name = "Добро пожаловать!"
	}

	return fmt.Sprintf(
		"%s\n\n"+
			"Мы сделали этого бота чтобы любой пользователь интернета мог получить доступ к современным моделям для генерации фото и видео БЕЗ впн'а, сложных регистраций, поисков иностранных карт и траты кучи денег на подписки.\n\n"+
			"Что он умеет?\n"+
			"- Генерация изображений любой сложности с помощью Nano Banana / Chat GPT / Midjourney\n"+
			"- Cоздание анимаций и видео с помощью Veo3 / Sora / Kling\n\n"+
			"Продолжая использование Вы принимаете пользовательское <a href=\"https://example.com\">соглашение</a>.\n\n"+
			"Так как Вы пришли от наших друзей, мы дарим Вам тестовые 5 генераций. Приступим?",
		name,
	)
}

func buildHelpText() string {
	imageModels := strings.Join(ModelUIList("image"), ", ")
	videoModels := strings.Join(ModelUIList("video"), ", ")

	return fmt.Sprintf(
		"Как пользоваться ботом:\n\n"+
			"1. Нажмите «Фото» или «Видео».\n"+
			"2. Выберите подходящую модель.\n"+
			"3. Напишите обычным сообщением, что хотите получить.\n\n"+
			"Если делаете видео по картинке, отправьте так:\n"+
			"input_reference=https://example.com/ref.png\n"+
			"и на следующей строке опишите, что должно получиться.\n\n"+
			"Доступные модели:\n"+
			"Фото: %s\n"+
			"Видео: %s\n\n"+
			"Где что находится:\n"+
			"«Профиль» — ваш баланс и поддержка.\n"+
			"«Купить» — пакеты генераций.\n\n"+
			"Поддержка: %s",
		imageModels,
		videoModels,
		supportContact,
	)
}

func (r *Router) sendHelpMessage(chatID int64) error {
	help := tgbotapi.NewMessage(chatID, buildHelpText())
	help.ReplyMarkup = MainReplyKeyboard()
	if _, err := r.Bot.API.Send(help); err != nil {
		return err
	}
	return nil
}

func (r *Router) trackStartSource(ctx context.Context, userID int64, m *tgbotapi.Message) error {
	if r == nil || r.Q == nil || m == nil || m.From == nil || userID == 0 {
		return nil
	}
	sourceTag := normalizeSourceTag(m.CommandArguments())
	username := strings.TrimSpace(m.From.UserName)

	_, err := r.Q.TrackUserStartAttribution(ctx, db.TrackUserStartAttributionParams{
		UserID:    userID,
		TgID:      m.From.ID,
		Username:  pgtype.Text{String: username, Valid: username != ""},
		SourceTag: sourceTag,
	})
	return err
}

func normalizeSourceTag(raw string) string {
	tag := strings.ToLower(strings.TrimSpace(raw))
	if tag == "" {
		return defaultSourceTag
	}

	// Keep tags safe and predictable for reporting keys.
	var b strings.Builder
	b.Grow(len(tag))
	for _, r := range tag {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	clean := b.String()
	if clean == "" {
		return defaultSourceTag
	}
	if len(clean) > maxSourceTagLen {
		clean = clean[:maxSourceTagLen]
	}
	return clean
}

func (r *Router) handleMessage(ctx context.Context, m *tgbotapi.Message, userID int64) error {
	txt := strings.TrimSpace(m.Text)
	if m.From != nil {
		handled, err := r.handleAdminTextInput(ctx, m, m.From.ID, txt)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
	}
	switch txt {
	case "Фото", "Создать картинку":
		return r.selectMode(ctx, m.Chat.ID, userID, "image")
	case "Видео", "Создать видео":
		return r.selectMode(ctx, m.Chat.ID, userID, "video")
	case "Профиль", "Мой профиль":
		return r.showProfile(ctx, m.Chat.ID, userID)
	case "Купить":
		return r.showPackages(ctx, m.Chat.ID)
	}

	st, ok, err := r.getState(ctx, userID)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось загрузить состояние пользователя. Попробуйте снова."))
		return err
	}
	if !ok || st.Mode == "" || st.Model == "" || txt == "" {
		msg := tgbotapi.NewMessage(m.Chat.ID, "Выберите действие")
		msg.ReplyMarkup = MainReplyKeyboard()
		r.Bot.API.Send(msg)
		return nil
	}

	kind := st.Mode
	if !isSupportedMode(kind) {
		// Guard old sessions that still have deprecated "text" mode selected.
		msg := tgbotapi.NewMessage(m.Chat.ID, "Текстовая генерация сейчас отключена. Выберите фото или видео.")
		msg.ReplyMarkup = MainReplyKeyboard()
		r.Bot.API.Send(msg)
		return nil
	}
	modelID, ok := ResolveModel(kind, st.Model)
	if !ok || modelID == "" {
		msg := tgbotapi.NewMessage(m.Chat.ID, "Выбранная модель не поддерживается. Пожалуйста, выберите другую.")
		msg.ReplyMarkup = MainReplyKeyboard()
		r.Bot.API.Send(msg)
		return nil
	}

	if kind == "image" || kind == "video" {
		return r.handleAsyncMediaPrompt(ctx, m, userID, kind, modelID, txt)
	}
	return r.handleSyncPrompt(ctx, m, userID, modelID, txt)
}

func (r *Router) selectMode(ctx context.Context, chatID, userID int64, mode string) error {
	if !isSupportedMode(mode) {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Этот режим недоступен."))
		return nil
	}
	opts := ModelUIList(mode)
	if len(opts) == 0 {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Для этого режима пока нет доступных моделей."))
		return nil
	}
	def, ok := DefaultModelUI(mode)
	if !ok || def == "" {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Для этого режима пока нет доступных моделей."))
		return nil
	}
	if err := r.setState(ctx, userID, UserState{Mode: mode, Model: def}); err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Не удалось сохранить выбор режима. Попробуйте снова."))
		return err
	}
	return r.sendModelsMenu(chatID, mode, def)
}

func (r *Router) handleSyncPrompt(ctx context.Context, m *tgbotapi.Message, userID int64, modelID, txt string) error {
	kind := "text"
	providerName := "comet"
	if !r.allowGenerationRequest(m.Chat.ID, kind) {
		return nil
	}

	convID, err := r.ensureConvID(ctx, userID, kind)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось открыть контекст диалога. Попробуйте снова."))
		return err
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

	updateID := updateIDFromContext(ctx)
	gr, created, err := r.getOrCreateGenerationRequest(ctx, db.InsertGenerationRequestParams{
		UserID:   userID,
		UpdateID: toInt8(updateID),
		Column3:  kind,
		Provider: providerName,
		Model:    modelID,
		Column6:  "running",
	}, updateID)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Внутренняя ошибка. Попробуйте позже."))
		return err
	}
	if !created && isTerminalGenerationRequestStatus(generationRequestStatus(gr.Status)) {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Этот запрос уже обработан. Отправьте новый промпт."))
		return nil
	}

	if created {
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
			r.failGenerationRequestWithLog(ctx, "sync-generation", gr.ID, "failed", "insert user message failed", pgtype.Int4{})
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось сохранить историю. Попробуйте позже."))
			return nil
		}
	}

	chargeMeta, _ := json.Marshal(map[string]any{"gen_id": gr.ID, "source": "sync_generation"})
	if err := r.chargeOneCredit(ctx, userID, kind, chargeMeta, fmt.Sprintf("spend:gen:%d:%s", gr.ID, kind)); err != nil {
		r.failGenerationRequestWithLog(ctx, "sync-generation", gr.ID, "failed_balance", err.Error(), pgtype.Int4{})
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Недостаточно генераций для %s", kindToRus(kind))))
		return nil
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
	streamEditFailed := false
	pending, sendErr := r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "⌛ Генерирую…"))
	if sendErr == nil {
		pendingMsgID = pending.MessageID
	} else {
		log.Printf("stream: send pending message failed chat_id=%d err=%v", m.Chat.ID, sendErr)
	}
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
		if pendingMsgID == 0 {
			return nil
		}
		if time.Since(lastEdit) >= throttle {
			if time.Since(windowStart) >= time.Minute {
				windowStart = time.Now()
				edits = 0
			}
			if r.EditMaxPerMin <= 0 || edits < r.EditMaxPerMin {
				editText := firstTelegramChunk(acc.String(), tgEditPreviewLimit)
				if err := r.editMessageText(m.Chat.ID, pendingMsgID, editText); err != nil {
					streamEditFailed = true
					log.Printf("stream: edit delta failed chat_id=%d msg_id=%d err=%v", m.Chat.ID, pendingMsgID, err)
				}
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
	finalText := resp.Output
	if strings.TrimSpace(finalText) == "" && strings.TrimSpace(acc.String()) != "" {
		finalText = acc.String()
	}
	if err == nil && strings.TrimSpace(finalText) == "" {
		err = errEmptyModelResponse
	}
	if err == nil && finalText != "" {
		if pendingMsgID != 0 && !streamEditFailed {
			if err := r.sendFinalStreamText(m.Chat.ID, pendingMsgID, finalText); err != nil {
				streamEditFailed = true
				log.Printf("stream: finalize edit failed chat_id=%d msg_id=%d err=%v", m.Chat.ID, pendingMsgID, err)
			}
		}
		if pendingMsgID == 0 || streamEditFailed {
			if err := r.sendTextChunks(m.Chat.ID, finalText); err != nil {
				log.Printf("stream: fallback chunk send failed chat_id=%d err=%v", m.Chat.ID, err)
				_, _ = r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Ответ готов, но не удалось отправить его полностью."))
			}
		}
	}

	latency := time.Since(t0).Milliseconds()
	if err != nil {
		r.failGenerationRequestWithLog(
			ctx,
			"sync-generation",
			gr.ID,
			"failed",
			err.Error(),
			pgtype.Int4{Int32: int32(latency), Valid: true},
		)
		refundMeta, _ := json.Marshal(map[string]any{"gen_id": gr.ID, "source": "sync_generation_failed"})
		refundErr := r.refundOneCredit(ctx, userID, kind, refundMeta, fmt.Sprintf("refund:gen:%d:%s", gr.ID, kind))
		errMsg := "Ошибка генерации. Попытка возвращена."
		if errors.Is(err, errEmptyModelResponse) {
			errMsg = "Сервис вернул пустой ответ. Попытка возвращена."
		}
		if refundErr != nil {
			log.Printf("sync-generation: refund failed gen_id=%d user_id=%d kind=%s err=%v reconcile_required=true", gr.ID, userID, kind, refundErr)
			errMsg = "Ошибка генерации. Возврат попытки в обработке, попробуйте позже."
		}
		if pendingMsgID != 0 {
			if e := r.editMessageText(m.Chat.ID, pendingMsgID, errMsg); e != nil {
				_, _ = r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, errMsg))
			}
		} else {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, errMsg))
		}
		return nil
	}

	cText := int32(1)
	if rows, finishErr := r.Q.FinishGenerationRequest(ctx, db.FinishGenerationRequestParams{
		ID:               gr.ID,
		OutputTokens:     pgtype.Int4{Int32: int32(resp.Tokens), Valid: resp.Tokens > 0},
		LatencyMs:        pgtype.Int4{Int32: int32(latency), Valid: true},
		CostCreditsText:  cText,
		CostCreditsImage: 0,
		CostCreditsVideo: 0,
	}); finishErr != nil {
		log.Printf("sync-generation: finish request failed gen_id=%d err=%v", gr.ID, finishErr)
	} else if rows == 0 {
		log.Printf("sync-generation: finish request skipped terminal-state gen_id=%d", gr.ID)
	}
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

	return nil
}

func (r *Router) handleAsyncMediaPrompt(ctx context.Context, m *tgbotapi.Message, userID int64, kind, modelID, txt string) error {
	providerName := "comet"
	if !r.allowGenerationRequest(m.Chat.ID, kind) {
		return nil
	}
	if r.Moderation != nil {
		mr, err := r.Moderation.CheckPrompt(ctx, txt)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось проверить промпт модерацией. Попробуйте позже."))
			return err
		}
		if !mr.Allowed {
			msg := "Промпт отклонен модерацией. Измените описание и попробуйте снова."
			if len(mr.Reasons) > 0 {
				msg = fmt.Sprintf("Промпт отклонен модерацией (%s). Измените описание и попробуйте снова.", strings.Join(mr.Reasons, ", "))
			}
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, msg))
			return nil
		}
	}

	convID, err := r.ensureConvID(ctx, userID, kind)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось открыть контекст диалога. Попробуйте снова."))
		return err
	}

	updateID := updateIDFromContext(ctx)
	gr, created, err := r.getOrCreateGenerationRequest(ctx, db.InsertGenerationRequestParams{
		UserID:   userID,
		UpdateID: toInt8(updateID),
		Column3:  kind,
		Provider: providerName,
		Model:    modelID,
		Column6:  "queued",
	}, updateID)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Внутренняя ошибка. Попробуйте позже."))
		return err
	}
	if !created && isTerminalGenerationRequestStatus(generationRequestStatus(gr.Status)) {
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Этот запрос уже обработан. Отправьте новый промпт."))
		return nil
	}

	if created {
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
			r.failGenerationRequestWithLog(ctx, "async-generation", gr.ID, "failed", "insert user message failed", pgtype.Int4{})
			r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, "Не удалось сохранить промпт. Попробуйте позже."))
			return nil
		}
	}

	chargeMeta, _ := json.Marshal(map[string]any{"gen_id": gr.ID, "source": "async_generation"})
	if err := r.chargeOneCredit(ctx, userID, kind, chargeMeta, fmt.Sprintf("spend:gen:%d:%s", gr.ID, kind)); err != nil {
		r.failGenerationRequestWithLog(ctx, "async-generation", gr.ID, "failed_balance", err.Error(), pgtype.Int4{})
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, fmt.Sprintf("Недостаточно генераций для %s", kindToRus(kind))))
		return nil
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
		r.failGenerationRequestWithLog(ctx, "async-generation", gr.ID, "failed_queue", err.Error(), pgtype.Int4{})
		refundMeta, _ := json.Marshal(map[string]any{"gen_id": gr.ID, "source": "queue_failed"})
		refundErr := r.refundOneCredit(ctx, userID, kind, refundMeta, fmt.Sprintf("refund:gen:%d:%s", gr.ID, kind))
		msg := "Не удалось поставить задачу в очередь. Попытка возвращена."
		if refundErr != nil {
			log.Printf("async-generation: refund failed gen_id=%d user_id=%d kind=%s source=queue_failed err=%v reconcile_required=true", gr.ID, userID, kind, refundErr)
			msg = "Не удалось поставить задачу в очередь. Возврат попытки в обработке, попробуйте позже."
		}
		r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, msg))
		return nil
	}

	ack := "Анализирую ваш запрос..."
	if !created {
		ack = "Запрос уже был принят ранее. Продолжаю обработку, результат пришлю сюда."
	}
	r.Bot.API.Send(tgbotapi.NewMessage(m.Chat.ID, ack))
	return nil
}

func (r *Router) handleCallback(ctx context.Context, cq *tgbotapi.CallbackQuery) error {
	if cq == nil {
		return nil
	}
	chatID, hasChat := callbackChatID(cq)
	if cq.From != nil {
		pseudoMsg := &tgbotapi.Message{
			From: &tgbotapi.User{
				ID:           cq.From.ID,
				UserName:     cq.From.UserName,
				FirstName:    cq.From.FirstName,
				LastName:     cq.From.LastName,
				LanguageCode: cq.From.LanguageCode,
			},
			Chat: &tgbotapi.Chat{ID: chatID},
		}
		var err error
		_, err = EnsureUser(ctx, r.Q, pseudoMsg)
		if err != nil {
			if hasChat {
				r.Bot.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка авторизации пользователя: %v", err)))
			}
			r.Bot.API.Request(tgbotapi.NewCallback(cq.ID, "auth error"))
			return err
		}
	}

	data := cq.Data
	parts := strings.Split(data, ":")
	if len(parts) == 0 {
		return nil
	}
	scope := parts[0]
	if scope == "admin" {
		if err := r.handleAdminCallback(ctx, cq, parts); err != nil {
			return err
		}
		r.Bot.API.Request(tgbotapi.NewCallback(cq.ID, ""))
		return nil
	}
	if cq.From == nil {
		r.Bot.API.Request(tgbotapi.NewCallback(cq.ID, "forbidden"))
		return nil
	}
	if cq.Message == nil {
		r.Bot.API.Request(tgbotapi.NewCallback(cq.ID, "unsupported callback"))
		return nil
	}
	chatID = cq.Message.Chat.ID
	userTGID := cq.From.ID
	u, err := r.Q.GetUserByTGID(ctx, userTGID)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка пользователя: %v", err)))
		return err
	}
	if u.IsBanned && !r.isAdminTGID(userTGID) {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Ваш аккаунт заблокирован. Обратитесь к администратору."))
		r.Bot.API.Request(tgbotapi.NewCallback(cq.ID, "forbidden"))
		return nil
	}

	switch scope {
	case "mode":
		if len(parts) < 2 {
			return nil
		}
		mode := parts[1]
		if !isSupportedMode(mode) {
			return nil
		}
		selected := ""
		st, ok, err := r.getState(ctx, u.ID)
		if err != nil {
			return err
		}
		if ok && st.Mode == mode {
			selected = st.Model
		}
		kb := ModelsInlineKeyboard(mode, selected)
		edit := tgbotapi.NewEditMessageReplyMarkup(chatID, cq.Message.MessageID, kb)
		if _, err := r.Bot.API.Request(edit); err != nil {
			return err
		}
		note := tgbotapi.NewMessage(chatID, "Выберите модель, чтобы активировать режим.")
		note.ReplyMarkup = MainReplyKeyboard()
		if _, err := r.Bot.API.Send(note); err != nil {
			return err
		}
	case "model":
		if len(parts) < 3 {
			return nil
		}
		mode := parts[1]
		if !isSupportedMode(mode) {
			return nil
		}
		model := parts[2]
		if _, ok := ResolveModel(mode, model); ok {
			if err := r.setState(ctx, u.ID, UserState{Mode: mode, Model: model}); err != nil {
				return err
			}
		}
		kb := ModelsInlineKeyboard(mode, model)
		edit := tgbotapi.NewEditMessageReplyMarkup(chatID, cq.Message.MessageID, kb)
		if _, err := r.Bot.API.Request(edit); err != nil {
			return err
		}
		if mode == "video" {
			break
		}
		_, _ = r.Bot.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Режим %s включён. Отправьте описание.", kindToRus(mode))))
	case "buy":
		if len(parts) < 2 {
			return nil
		}
		code := parts[1]
		if code == "menu" {
			if err := r.showPackages(ctx, chatID); err != nil {
				return err
			}
			return nil
		}
		pkg, err := r.Q.GetPackageByCode(ctx, code)
		if err != nil {
			r.Bot.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Пакет не найден: %s", code)))
			return err
		}
		p := payments.Package{ID: pkg.ID, Title: pkg.Title, PriceRub: int(pkg.PriceRub)}
		if _, err := r.Pay.SendInvoiceForPackage(ctx, chatID, u.ID, p, ""); err != nil {
			if errors.Is(err, payments.ErrPaymentUnavailableForBanned) {
				r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Оплата недоступна для заблокированного аккаунта. Обратитесь к администратору."))
				return nil
			}
			r.Bot.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка оплаты: %v", err)))
			return err
		}
	case "support":
		if len(parts) < 2 {
			return nil
		}
		if parts[1] == "help" {
			if err := r.sendHelpMessage(chatID); err != nil {
				return err
			}
		}
	}
	r.Bot.API.Request(tgbotapi.NewCallback(cq.ID, ""))
	return nil
}

func (r *Router) sendModelsMenu(chatID int64, mode, selected string) error {
	kb := ModelsInlineKeyboard(mode, selected)
	msg := tgbotapi.NewMessage(chatID, "Выберите модель:")
	msg.ReplyMarkup = kb
	if _, err := r.Bot.API.Send(msg); err != nil {
		return err
	}
	return nil
}

func (r *Router) showProfile(ctx context.Context, chatID, userID int64) error {
	bal, err := r.Q.GetBalancesByUserID(ctx, userID)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка профиля: %v", err)))
		return err
	}

	text := fmt.Sprintf("Баланс:\n• Фото: %d\n• Видео: %d", bal.ImageBalance, bal.VideoBalance)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = ProfileInlineKeyboard()
	if _, err := r.Bot.API.Send(msg); err != nil {
		return err
	}
	return nil
}

func (r *Router) showPackages(ctx context.Context, chatID int64) error {
	pkgs, err := r.Q.ListActivePackages(ctx)
	if err != nil {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Не удалось загрузить пакеты. Попробуйте позже."))
		return err
	}
	if len(pkgs) == 0 {
		r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Сейчас нет доступных пакетов."))
		return nil
	}

	photoLines := make([]string, 0, len(pkgs))
	videoLines := make([]string, 0, len(pkgs))
	comboLines := make([]string, 0, len(pkgs))
	buttons := make([]PackageButton, 0, len(pkgs))
	for _, p := range pkgs {
		switch {
		case p.ImageCredits > 0 && p.VideoCredits == 0:
			photoLines = append(photoLines, formatPackageOfferLine(p))
		case p.VideoCredits > 0 && p.ImageCredits == 0:
			videoLines = append(videoLines, formatPackageOfferLine(p))
		case p.ImageCredits > 0 && p.VideoCredits > 0:
			comboLines = append(comboLines, formatPackageOfferLine(p))
		default:
			comboLines = append(comboLines, formatPackageOfferLine(p))
		}
		buttons = append(buttons, PackageButton{
			Code:  p.Code,
			Label: packageButtonLabel(p),
		})
	}

	text := buildPackagesMenuText(photoLines, videoLines, comboLines)
	kb := PackagesInlineKeyboard(buttons)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = kb
	if _, err := r.Bot.API.Send(msg); err != nil {
		return err
	}
	return nil
}

func formatPackageOfferLine(p db.Package) string {
	switch {
	case p.ImageCredits > 0 && p.VideoCredits == 0:
		unit := float64(p.PriceRub) / float64(p.ImageCredits)
		return fmt.Sprintf("• %s — %d фото · %d ₽ (%.2f ₽/фото)", p.Title, p.ImageCredits, p.PriceRub, unit)
	case p.VideoCredits > 0 && p.ImageCredits == 0:
		unit := float64(p.PriceRub) / float64(p.VideoCredits)
		return fmt.Sprintf("• %s — %d видео · %d ₽ (%.2f ₽/видео)", p.Title, p.VideoCredits, p.PriceRub, unit)
	case p.ImageCredits > 0 && p.VideoCredits > 0:
		title := p.Title
		if isMostPopularTitle(title) {
			title = "🔥 " + title
		}
		return fmt.Sprintf("• %s — %d фото + %d видео · %d ₽", title, p.ImageCredits, p.VideoCredits, p.PriceRub)
	default:
		return fmt.Sprintf("• %s — %d ₽", p.Title, p.PriceRub)
	}
}

func packageButtonLabel(p db.Package) string {
	switch {
	case p.ImageCredits > 0 && p.VideoCredits == 0:
		return fmt.Sprintf("📷 %s · %d фото · %d ₽", p.Title, p.ImageCredits, p.PriceRub)
	case p.VideoCredits > 0 && p.ImageCredits == 0:
		return fmt.Sprintf("🎬 %s · %d видео · %d ₽", p.Title, p.VideoCredits, p.PriceRub)
	case p.ImageCredits > 0 && p.VideoCredits > 0:
		title := p.Title
		if isMostPopularTitle(title) {
			title = "🔥 " + strings.TrimSpace(strings.ReplaceAll(title, "(Most Popular)", ""))
		}
		return fmt.Sprintf("✨ %s · %d+%d · %d ₽", title, p.ImageCredits, p.VideoCredits, p.PriceRub)
	default:
		return p.Title
	}
}

func buildPackagesMenuText(photoLines, videoLines, comboLines []string) string {
	parts := []string{"Тарифы"}
	if len(photoLines) > 0 {
		parts = append(parts, "", "📷 Фото", strings.Join(photoLines, "\n"))
	}
	if len(videoLines) > 0 {
		parts = append(parts, "", "🎬 Видео", strings.Join(videoLines, "\n"))
	}
	if len(comboLines) > 0 {
		parts = append(parts, "", "✨ Комбо", strings.Join(comboLines, "\n"))
	}
	parts = append(parts, "", "Чем больше пакет, тем выгоднее цена за попытку.")
	return strings.Join(parts, "\n")
}

func isMostPopularTitle(title string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(title)), "most popular")
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

func (r *Router) failGenerationRequestWithLog(
	ctx context.Context,
	flow string,
	genID int64,
	status string,
	errMessage string,
	latency pgtype.Int4,
) {
	rows, err := r.Q.FailGenerationRequest(ctx, db.FailGenerationRequestParams{
		ID:           genID,
		Column2:      status,
		ErrorMessage: pgtype.Text{String: errMessage, Valid: errMessage != ""},
		LatencyMs:    latency,
	})
	if err != nil {
		log.Printf("%s: fail request write failed gen_id=%d status=%s err=%v", flow, genID, status, err)
		return
	}
	if rows == 0 {
		log.Printf("%s: fail request skipped terminal-state gen_id=%d status=%s", flow, genID, status)
	}
}

func (r *Router) getOrCreateGenerationRequest(
	ctx context.Context,
	params db.InsertGenerationRequestParams,
	updateID int64,
) (db.GenerationRequest, bool, error) {
	if updateID > 0 {
		existing, err := r.Q.GetGenerationRequestByUpdateID(ctx, toInt8(updateID))
		if err == nil {
			return generationRequestFromExisting(existing), false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return db.GenerationRequest{}, false, err
		}
	}

	gr, err := r.Q.InsertGenerationRequest(ctx, params)
	if err == nil {
		return generationRequestFromInsert(gr), true, nil
	}
	if updateID > 0 && isUniqueViolation(err) {
		existing, gerr := r.Q.GetGenerationRequestByUpdateID(ctx, toInt8(updateID))
		if gerr == nil {
			return generationRequestFromExisting(existing), false, nil
		}
		if !errors.Is(gerr, pgx.ErrNoRows) {
			return db.GenerationRequest{}, false, gerr
		}
	}
	return db.GenerationRequest{}, false, err
}

func generationRequestFromExisting(row db.GetGenerationRequestByUpdateIDRow) db.GenerationRequest {
	return db.GenerationRequest{
		ID:               row.ID,
		UserID:           row.UserID,
		UpdateID:         row.UpdateID,
		Kind:             row.Kind,
		Provider:         row.Provider,
		Model:            row.Model,
		OutputTokens:     row.OutputTokens,
		CostCreditsText:  row.CostCreditsText,
		CostCreditsImage: row.CostCreditsImage,
		CostCreditsVideo: row.CostCreditsVideo,
		Status:           row.Status,
		ErrorMessage:     row.ErrorMessage,
		LatencyMs:        row.LatencyMs,
		CreatedAt:        row.CreatedAt,
		FinishedAt:       row.FinishedAt,
	}
}

func generationRequestFromInsert(row db.InsertGenerationRequestRow) db.GenerationRequest {
	return db.GenerationRequest{
		ID:               row.ID,
		UserID:           row.UserID,
		UpdateID:         row.UpdateID,
		Kind:             row.Kind,
		Provider:         row.Provider,
		Model:            row.Model,
		OutputTokens:     row.OutputTokens,
		CostCreditsText:  row.CostCreditsText,
		CostCreditsImage: row.CostCreditsImage,
		CostCreditsVideo: row.CostCreditsVideo,
		Status:           row.Status,
		ErrorMessage:     row.ErrorMessage,
		LatencyMs:        row.LatencyMs,
		CreatedAt:        row.CreatedAt,
		FinishedAt:       row.FinishedAt,
	}
}

func generationRequestStatus(status interface{}) string {
	if s, ok := status.(string); ok {
		return s
	}
	return fmt.Sprint(status)
}

func isTerminalGenerationRequestStatus(status string) bool {
	switch status {
	case "ok", "failed", "failed_balance", "failed_queue":
		return true
	default:
		return false
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505"
}

func toInt8(v int64) pgtype.Int8 {
	return pgtype.Int8{Int64: v, Valid: v > 0}
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
	return mode == "image" || mode == "video"
}

func (r *Router) allowGenerationRequest(chatID int64, kind string) bool {
	if r.RL == nil || r.RL.AllowKind(kind) {
		return true
	}
	_, _ = r.Bot.API.Send(tgbotapi.NewMessage(chatID, "Сервис перегружен, попробуйте позже."))
	return false
}

func (r *Router) isUserBanned(ctx context.Context, tgID int64) (bool, error) {
	u, err := r.Q.GetUserByTGID(ctx, tgID)
	if err != nil {
		return false, err
	}
	return u.IsBanned, nil
}

func (r *Router) isAdminTGID(tgID int64) bool {
	r.adminIDsMu.RLock()
	defer r.adminIDsMu.RUnlock()
	_, ok := r.adminIDs[tgID]
	return ok
}

func callbackChatID(cq *tgbotapi.CallbackQuery) (int64, bool) {
	if cq == nil {
		return 0, false
	}
	if cq.Message != nil {
		return cq.Message.Chat.ID, true
	}
	if cq.From != nil {
		return cq.From.ID, true
	}
	return 0, false
}

func (r *Router) editMessageText(chatID int64, msgID int, text string) error {
	if msgID == 0 {
		return errors.New("message id is zero")
	}
	_, err := r.Bot.API.Request(tgbotapi.NewEditMessageText(chatID, msgID, text))
	return err
}

func (r *Router) sendFinalStreamText(chatID int64, msgID int, text string) error {
	chunks := splitTelegramText(text, tgMessageLimit)
	if len(chunks) == 0 {
		return nil
	}
	if err := r.editMessageText(chatID, msgID, chunks[0]); err != nil {
		return err
	}
	for i := 1; i < len(chunks); i++ {
		if _, err := r.Bot.API.Send(tgbotapi.NewMessage(chatID, chunks[i])); err != nil {
			return err
		}
	}
	return nil
}

func (r *Router) sendTextChunks(chatID int64, text string) error {
	chunks := splitTelegramText(text, tgMessageLimit)
	if len(chunks) == 0 {
		return nil
	}
	for _, ch := range chunks {
		if _, err := r.Bot.API.Send(tgbotapi.NewMessage(chatID, ch)); err != nil {
			return err
		}
	}
	return nil
}

func firstTelegramChunk(text string, limit int) string {
	chunks := splitTelegramText(text, limit)
	if len(chunks) == 0 {
		return ""
	}
	return chunks[0]
}

func splitTelegramText(text string, limit int) []string {
	if limit <= 0 {
		limit = tgMessageLimit
	}
	if text == "" {
		return nil
	}
	rs := []rune(text)
	chunks := make([]string, 0, (len(rs)/limit)+1)
	for len(rs) > 0 {
		if len(rs) <= limit {
			chunks = append(chunks, string(rs))
			break
		}
		chunks = append(chunks, string(rs[:limit]))
		rs = rs[limit:]
	}
	return chunks
}
