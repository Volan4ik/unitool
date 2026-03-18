package telegram

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestCallbackChatIDFromMessage(t *testing.T) {
	cq := &tgbotapi.CallbackQuery{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 12345},
		},
		From: &tgbotapi.User{ID: 999},
	}
	got, ok := callbackChatID(cq)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got != 12345 {
		t.Fatalf("expected chat id 12345, got %d", got)
	}
}

func TestCallbackChatIDFallsBackToFrom(t *testing.T) {
	cq := &tgbotapi.CallbackQuery{
		From: &tgbotapi.User{ID: 777},
	}
	got, ok := callbackChatID(cq)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got != 777 {
		t.Fatalf("expected fallback id 777, got %d", got)
	}
}

func TestCallbackChatIDNil(t *testing.T) {
	got, ok := callbackChatID(nil)
	if ok {
		t.Fatalf("expected ok=false, got true with id=%d", got)
	}
}
