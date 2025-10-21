package telegram

import (
	"context"
	"net/http"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	API *tgbotapi.BotAPI
}

func New(token string) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil { return nil, err }
	api.Client = &http.Client{ Timeout: 10 * time.Second }
	return &Bot{API: api}, nil
}

func (b *Bot) SetWebhook(url string) error {
	_, err := b.API.Request(tgbotapi.NewWebhook(url))
	return err
}

func (b *Bot) DeleteWebhook() error {
	_, err := b.API.Request(tgbotapi.DeleteWebhookConfig{DropPendingUpdates: true})
	return err
}