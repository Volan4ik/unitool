package telegram

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var mediaGroupSettleDelay = 1200 * time.Millisecond

const (
	maxSingleReferenceVideoImages = 1
	maxKlingVideoReferenceImages  = 4
)

var encodeReferenceDataURI = buildReferenceDataURIs

type pendingMediaGroup struct {
	ChatID  int64
	UserID  int64
	Prompt  string
	FileIDs []string
	Timer   *time.Timer
}

func (r *Router) handlePhotoMessage(ctx context.Context, m *tgbotapi.Message, userID int64) error {
	if m == nil || m.Chat == nil {
		return nil
	}

	fileID := largestPhotoFileID(m.Photo)
	if fileID == "" {
		msg := tgbotapi.NewMessage(m.Chat.ID, "Не удалось прочитать фото. Попробуйте отправить его ещё раз.")
		msg.ReplyMarkup = MainReplyKeyboard()
		_, _ = r.Bot.API.Send(msg)
		return nil
	}

	prompt := strings.TrimSpace(m.Caption)
	if mediaGroupID := strings.TrimSpace(m.MediaGroupID); mediaGroupID != "" {
		r.collectMediaGroupPhoto(m.Chat.ID, userID, mediaGroupID, fileID, prompt)
		return nil
	}

	if prompt == "" {
		r.appendPendingReferences(userID, []string{fileID})
		msg := tgbotapi.NewMessage(m.Chat.ID, "Референс получен. Теперь отправьте подпись одним сообщением.")
		msg.ReplyMarkup = MainReplyKeyboard()
		_, _ = r.Bot.API.Send(msg)
		return nil
	}

	return r.processReferencePrompt(ctx, m.Chat.ID, userID, prompt, []string{fileID})
}

func (r *Router) collectMediaGroupPhoto(chatID, userID int64, mediaGroupID, fileID, prompt string) {
	key := fmt.Sprintf("%d:%d:%s", chatID, userID, mediaGroupID)

	r.mediaGroupMu.Lock()
	defer r.mediaGroupMu.Unlock()

	group, ok := r.mediaGroups[key]
	if !ok {
		group = &pendingMediaGroup{
			ChatID:  chatID,
			UserID:  userID,
			FileIDs: make([]string, 0, 1),
		}
		r.mediaGroups[key] = group
	}
	group.FileIDs = append(group.FileIDs, fileID)
	if group.Prompt == "" && prompt != "" {
		group.Prompt = prompt
	}
	if group.Timer != nil {
		group.Timer.Stop()
	}
	group.Timer = time.AfterFunc(mediaGroupSettleDelay, func() {
		r.flushMediaGroup(key)
	})
}

func (r *Router) flushMediaGroup(key string) {
	r.mediaGroupMu.Lock()
	group, ok := r.mediaGroups[key]
	if !ok {
		r.mediaGroupMu.Unlock()
		return
	}
	delete(r.mediaGroups, key)
	r.mediaGroupMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if strings.TrimSpace(group.Prompt) == "" {
		r.appendPendingReferences(group.UserID, group.FileIDs)
		msg := tgbotapi.NewMessage(group.ChatID, "Референсы получены. Теперь отправьте подпись одним сообщением.")
		msg.ReplyMarkup = MainReplyKeyboard()
		_, _ = r.Bot.API.Send(msg)
		return
	}

	if err := r.processReferencePrompt(ctx, group.ChatID, group.UserID, group.Prompt, group.FileIDs); err != nil {
		return
	}
}

func (r *Router) processReferencePrompt(ctx context.Context, chatID, userID int64, prompt string, fileIDs []string) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		msg := tgbotapi.NewMessage(chatID, "Добавьте подпись с описанием того, что нужно сгенерировать.")
		msg.ReplyMarkup = MainReplyKeyboard()
		_, _ = r.Bot.API.Send(msg)
		return nil
	}

	st, ok, err := r.getState(ctx, userID)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Не удалось загрузить состояние пользователя. Попробуйте снова.")
		msg.ReplyMarkup = MainReplyKeyboard()
		_, _ = r.Bot.API.Send(msg)
		return err
	}
	if !ok || st.Mode == "" || st.Model == "" {
		r.appendPendingReferences(userID, fileIDs)
		msg := tgbotapi.NewMessage(chatID, "Референсы сохранены. Теперь выберите «Фото» или «Видео» и отправьте подпись ещё раз.")
		msg.ReplyMarkup = MainReplyKeyboard()
		_, _ = r.Bot.API.Send(msg)
		return nil
	}
	if !isSupportedMode(st.Mode) {
		msg := tgbotapi.NewMessage(chatID, "Референсы фото сейчас поддерживаются только для режимов «Фото» и «Видео».")
		msg.ReplyMarkup = MainReplyKeyboard()
		_, _ = r.Bot.API.Send(msg)
		return nil
	}
	modelID, ok := ResolveModel(st.Mode, st.Model)
	if !ok || modelID == "" {
		msg := tgbotapi.NewMessage(chatID, "Выбранная модель не поддерживается. Пожалуйста, выберите другую.")
		msg.ReplyMarkup = MainReplyKeyboard()
		_, _ = r.Bot.API.Send(msg)
		return nil
	}
	fileIDs, warning := referenceFileIDsForModeModel(st.Mode, modelID, fileIDs)
	if warning != "" {
		msg := tgbotapi.NewMessage(chatID, warning)
		msg.ReplyMarkup = MainReplyKeyboard()
		_, _ = r.Bot.API.Send(msg)
	}

	dataURIs, err := encodeReferenceDataURI(ctx, r.Bot.API, fileIDs)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Не удалось подготовить референсы. Попробуйте отправить фотографии ещё раз.")
		msg.ReplyMarkup = MainReplyKeyboard()
		_, _ = r.Bot.API.Send(msg)
		return err
	}

	enrichedPrompt := buildPromptWithReferences(prompt, dataURIs)
	return r.enqueueAsyncMediaPrompt(ctx, chatID, userID, st.Mode, modelID, enrichedPrompt)
}

func referenceFileIDsForModeModel(mode string, modelID string, fileIDs []string) ([]string, string) {
	if mode != "video" {
		return fileIDs, ""
	}
	limit := maxSingleReferenceVideoImages
	if isKlingVideoProviderModel(modelID) {
		limit = maxKlingVideoReferenceImages
	}
	if len(fileIDs) <= limit {
		return fileIDs, ""
	}
	if isKlingVideoProviderModel(modelID) {
		return fileIDs[:limit], "Kling поддерживает до 4 фото-референсов для видео. Использую первые 4 фото из отправленных."
	}
	return fileIDs[:limit], "Sora 2 и Veo 3 поддерживают только одно фото-референс для видео. Использую первое фото из отправленных."
}

func isKlingVideoProviderModel(modelID string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelID)), "kling-")
}

func (r *Router) appendPendingReferences(userID int64, fileIDs []string) {
	if userID == 0 || len(fileIDs) == 0 {
		return
	}
	r.pendingRefsMu.Lock()
	defer r.pendingRefsMu.Unlock()
	r.pendingRefs[userID] = append(r.pendingRefs[userID], fileIDs...)
}

func (r *Router) peekPendingReferences(userID int64) ([]string, bool) {
	r.pendingRefsMu.Lock()
	defer r.pendingRefsMu.Unlock()
	refs := append([]string(nil), r.pendingRefs[userID]...)
	return refs, len(refs) > 0
}

func (r *Router) clearPendingReferences(userID int64) {
	r.pendingRefsMu.Lock()
	defer r.pendingRefsMu.Unlock()
	delete(r.pendingRefs, userID)
}

func largestPhotoFileID(photos []tgbotapi.PhotoSize) string {
	if len(photos) == 0 {
		return ""
	}
	best := photos[len(photos)-1]
	bestArea := best.Width * best.Height
	for _, photo := range photos {
		area := photo.Width * photo.Height
		if area >= bestArea {
			best = photo
			bestArea = area
		}
	}
	return strings.TrimSpace(best.FileID)
}

func buildReferenceDataURIs(ctx context.Context, bot *tgbotapi.BotAPI, fileIDs []string) ([]string, error) {
	refs := make([]string, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		fileID = strings.TrimSpace(fileID)
		if fileID == "" {
			continue
		}
		dataURI, err := downloadTelegramImageDataURI(ctx, bot, fileID)
		if err != nil {
			return nil, err
		}
		refs = append(refs, dataURI)
	}
	return refs, nil
}

func downloadTelegramImageDataURI(ctx context.Context, bot *tgbotapi.BotAPI, fileID string) (string, error) {
	if bot == nil {
		return "", fmt.Errorf("telegram bot is nil")
	}
	directURL, err := bot.GetFileDirectURL(fileID)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, directURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := bot.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("telegram file download http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 15*1024*1024))
	if err != nil {
		return "", err
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		contentType = "image/jpeg"
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(body), nil
}

func buildPromptWithReferences(prompt string, refs []string) string {
	if len(refs) == 0 {
		return prompt
	}
	lines := make([]string, 0, len(refs)+1)
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		lines = append(lines, "input_reference="+ref)
	}
	if prompt = strings.TrimSpace(prompt); prompt != "" {
		lines = append(lines, prompt)
	}
	return strings.Join(lines, "\n")
}

func promptWithoutReferenceLines(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if _, ok := parseTelegramInputReferenceLine(line); ok {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func parseTelegramInputReferenceLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "input_reference") {
		return "", false
	}
	tail := strings.TrimSpace(trimmed[len("input_reference"):])
	if tail == "" {
		return "", false
	}
	if tail[0] != ':' && tail[0] != '=' {
		return "", false
	}
	value := strings.TrimSpace(tail[1:])
	value = strings.Trim(value, `"'`)
	value = strings.TrimPrefix(value, "<")
	value = strings.TrimSuffix(value, ">")
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
}
