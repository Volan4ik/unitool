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
    if err != nil { return nil, err }
    // Use a long timeout to match Telegram long-polling (~50s)
    api.Client = &http.Client{ Timeout: 65 * time.Second }
    return &Bot{API: api}, nil
}

func (b *Bot) SetWebhook(url string) error {
    cfg, err := tgbotapi.NewWebhook(url)
    if err != nil { return err }
    _, err = b.API.Request(cfg)
    return err
}

func (b *Bot) DeleteWebhook() error {
	_, err := b.API.Request(tgbotapi.DeleteWebhookConfig{DropPendingUpdates: true})
	return err
}
