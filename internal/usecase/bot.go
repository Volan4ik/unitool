package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/yourorg/ai-telebot/internal/repo"
)

type Users interface {
	GetByTgID(ctx context.Context, tgID int64) (*repo.User, error)
	Create(ctx context.Context, u *repo.User) error
	DecrementFreeTry(ctx context.Context, tgID int64) (int, error)
}

type BotUsecase struct {
	botToken         string
	lg               *log.Logger
	users            Users
	defaultFreeTries int
}

func NewBotUsecase(token string, lg *log.Logger) *BotUsecase {
	return &BotUsecase{botToken: token, lg: lg, defaultFreeTries: 3}
}

func (u *BotUsecase) WithUsers(users Users, defaultFree int) {
	u.users = users
	u.defaultFreeTries = defaultFree
}

func (u *BotUsecase) EnsureUser(ctx context.Context, tgID int64) error {
	if u.users == nil {
		return nil
	}
	usr, err := u.users.GetByTgID(ctx, tgID)
	if err != nil {
		return err
	}
	if usr == nil {
		return u.users.Create(ctx, &repo.User{
			TgID:      tgID,
			FreeTries: u.defaultFreeTries,
			Credits:   0,
		})
	}
	return nil
}

func (u *BotUsecase) TryConsumeFree(ctx context.Context, tgID int64) (int, error) {
	if u.users == nil {
		return -1, nil
	}
	return u.users.DecrementFreeTry(ctx, tgID)
}

// SendMessage — простая отправка сообщения пользователю через Telegram Bot API
func (u *BotUsecase) SendMessage(chatID int64, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", u.botToken)
	body := map[string]interface{}{"chat_id": chatID, "text": text}
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
	body := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
		"reply_markup": map[string]interface{}{
			"inline_keyboard": buttons,
		},
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 { return fmt.Errorf("tg sendMessage status=%d", resp.StatusCode) }
	return nil
}

func (u *BotUsecase) AnswerCallbackQuery(callbackID string, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", u.botToken)
	body := map[string]interface{}{"callback_query_id": callbackID, "text": text}
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 { return fmt.Errorf("tg answerCallbackQuery status=%d", resp.StatusCode) }
	return nil
}