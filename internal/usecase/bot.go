package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"unitool/internal/repo"
)

type Users interface {
	GetByTgUserID(ctx context.Context, tgID int64) (*repo.User, error)
	Create(ctx context.Context, u *repo.User) error
	TouchLastSeen(ctx context.Context, userID uuid.UUID, at time.Time) error
}

type BotUsecase struct {
	botToken string
	lg       *log.Logger
	users    Users
}

func NewBotUsecase(token string, lg *log.Logger) *BotUsecase {
	return &BotUsecase{botToken: token, lg: lg}
}

func (u *BotUsecase) WithUsers(users Users) {
	u.users = users
}

func (u *BotUsecase) EnsureUser(ctx context.Context, tgID int64, username *string) (*repo.User, error) {
	if u.users == nil {
		return nil, nil
	}
	usr, err := u.users.GetByTgUserID(ctx, tgID)
	if err != nil {
		return nil, err
	}
	if usr != nil {
		if err := u.users.TouchLastSeen(ctx, usr.ID, time.Now()); err != nil {
			u.lg.Printf("touch last seen: %v", err)
		}
		return usr, nil
	}
	newUser := &repo.User{
		TgUserID: tgID,
		Username: username,
	}
	if err := u.users.Create(ctx, newUser); err != nil {
		return nil, err
	}
	return newUser, nil
}

// SendMessage — простая отправка сообщения пользователю через Telegram Bot API
func (u *BotUsecase) SendMessage(chatID int64, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", u.botToken)
	body := map[string]any{"chat_id": chatID, "text": text}
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("tg sendMessage status=%d", resp.StatusCode)
	}
	return nil
}

func (u *BotUsecase) SendMessageWithKeyboard(chatID int64, text string, buttons [][]map[string]string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", u.botToken)
	body := map[string]any{
		"chat_id": chatID,
		"text":    text,
		"reply_markup": map[string]any{
			"inline_keyboard": buttons,
		},
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("tg sendMessage status=%d", resp.StatusCode)
	}
	return nil
}

func (u *BotUsecase) AnswerCallbackQuery(callbackID string, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", u.botToken)
	body := map[string]any{"callback_query_id": callbackID, "text": text}
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("tg answerCallbackQuery status=%d", resp.StatusCode)
	}
	return nil
}
