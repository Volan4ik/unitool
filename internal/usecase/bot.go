package usecase

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

type BotUsecase struct {
	botToken string
	lg       *log.Logger
}

func NewBotUsecase(token string, lg *log.Logger) *BotUsecase {
	return &BotUsecase{botToken: token, lg: lg}
}

// SendMessage — простая отправка сообщения пользователю через Telegram Bot API
func (u *BotUsecase) SendMessage(chatID int64, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", u.botToken)
	body := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 { return fmt.Errorf("tg sendMessage status=%d", resp.StatusCode) }
	return nil
}