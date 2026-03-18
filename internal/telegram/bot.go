package telegram

import (
	"net/http"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	API *tgbotapi.BotAPI
}

func New(token string) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}
	// Keep a conservative timeout for Telegram API calls.
	api.Client = &http.Client{Timeout: 65 * time.Second}
	return &Bot{API: api}, nil
}

func (b *Bot) SetWebhook(url, secretToken string) error {
	p := tgbotapi.Params{"url": url}
	p.AddNonEmpty("secret_token", secretToken)
	_, err := b.API.MakeRequest("setWebhook", p)
	return err
}
