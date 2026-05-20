package telegram

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var mediaGroupSettleDelay = 1200 * time.Millisecond

const (
	maxSingleReferenceVideoImages = 1
	maxKlingVideoReferenceImages  = 4
	videoReferenceWidth           = 720
	videoReferenceHeight          = 1280
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
	log.Printf(
		"incoming photo reference user_id=%d chat_id=%d message_id=%d file_id=%s photo_sizes=%d has_caption=%t media_group_id=%q",
		userID,
		m.Chat.ID,
		m.MessageID,
		fileID,
		len(m.Photo),
		strings.TrimSpace(m.Caption) != "",
		strings.TrimSpace(m.MediaGroupID),
	)
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
	if st.Mode == "video" && !isKlingVideoProviderModel(modelID) {
		dataURIs, err = fitVideoReferenceDataURIs(dataURIs)
		if err != nil {
			msg := tgbotapi.NewMessage(chatID, "Не удалось подготовить референс под формат видео. Попробуйте отправить другую фотографию.")
			msg.ReplyMarkup = MainReplyKeyboard()
			_, _ = r.Bot.API.Send(msg)
			return err
		}
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
	return fileIDs[:limit], "Sora 2, Doubao Seedance 2.0 и Veo 3 поддерживают только одно фото-референс для видео. Использую первое фото из отправленных."
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

func fitVideoReferenceDataURIs(refs []string) ([]string, error) {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		fitted, err := fitVideoReferenceDataURI(ref)
		if err != nil {
			return nil, err
		}
		out = append(out, fitted)
	}
	return out, nil
}

func fitVideoReferenceDataURI(ref string) (string, error) {
	const prefix = "data:image/"
	if !strings.HasPrefix(strings.ToLower(ref), prefix) {
		return ref, nil
	}
	comma := strings.IndexByte(ref, ',')
	if comma <= 0 {
		return "", fmt.Errorf("invalid image data URI")
	}
	meta := strings.ToLower(ref[:comma])
	if !strings.Contains(meta, ";base64") {
		return "", fmt.Errorf("image data URI is not base64")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ref[comma+1:]))
	if err != nil {
		return "", err
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	fitted := resizeCenterCrop(src, videoReferenceWidth, videoReferenceHeight)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, fitted, &jpeg.Options{Quality: 92}); err != nil {
		return "", err
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func resizeCenterCrop(src image.Image, targetW, targetH int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW <= 0 || srcH <= 0 || targetW <= 0 || targetH <= 0 {
		return dst
	}

	targetAspect := float64(targetW) / float64(targetH)
	cropW := srcW
	cropH := int(float64(cropW) / targetAspect)
	if cropH > srcH {
		cropH = srcH
		cropW = int(float64(cropH) * targetAspect)
	}
	if cropW < 1 {
		cropW = 1
	}
	if cropH < 1 {
		cropH = 1
	}
	cropX := bounds.Min.X + (srcW-cropW)/2
	cropY := bounds.Min.Y + (srcH-cropH)/2

	for y := 0; y < targetH; y++ {
		sy := float64(cropY) + (float64(y)+0.5)*float64(cropH)/float64(targetH) - 0.5
		for x := 0; x < targetW; x++ {
			sx := float64(cropX) + (float64(x)+0.5)*float64(cropW)/float64(targetW) - 0.5
			dst.Set(x, y, bilinearAt(src, sx, sy, bounds))
		}
	}
	return dst
}

func bilinearAt(img image.Image, fx, fy float64, bounds image.Rectangle) color.RGBA {
	x0 := int(fx)
	y0 := int(fy)
	if fx < float64(x0) {
		x0--
	}
	if fy < float64(y0) {
		y0--
	}
	x1 := x0 + 1
	y1 := y0 + 1
	x0 = clampInt(x0, bounds.Min.X, bounds.Max.X-1)
	x1 = clampInt(x1, bounds.Min.X, bounds.Max.X-1)
	y0 = clampInt(y0, bounds.Min.Y, bounds.Max.Y-1)
	y1 = clampInt(y1, bounds.Min.Y, bounds.Max.Y-1)

	wx := fx - float64(x0)
	wy := fy - float64(y0)
	if wx < 0 {
		wx = 0
	}
	if wy < 0 {
		wy = 0
	}
	if wx > 1 {
		wx = 1
	}
	if wy > 1 {
		wy = 1
	}

	c00 := rgbaAt(img, x0, y0)
	c10 := rgbaAt(img, x1, y0)
	c01 := rgbaAt(img, x0, y1)
	c11 := rgbaAt(img, x1, y1)

	return color.RGBA{
		R: blendChannel(c00.R, c10.R, c01.R, c11.R, wx, wy),
		G: blendChannel(c00.G, c10.G, c01.G, c11.G, wx, wy),
		B: blendChannel(c00.B, c10.B, c01.B, c11.B, wx, wy),
		A: blendChannel(c00.A, c10.A, c01.A, c11.A, wx, wy),
	}
}

func rgbaAt(img image.Image, x, y int) color.RGBA {
	r, g, b, a := img.At(x, y).RGBA()
	return color.RGBA{
		R: uint8(r >> 8),
		G: uint8(g >> 8),
		B: uint8(b >> 8),
		A: uint8(a >> 8),
	}
}

func blendChannel(c00, c10, c01, c11 uint8, wx, wy float64) uint8 {
	top := float64(c00)*(1-wx) + float64(c10)*wx
	bottom := float64(c01)*(1-wx) + float64(c11)*wx
	v := top*(1-wy) + bottom*wy
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
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
