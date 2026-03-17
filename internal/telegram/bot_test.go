package telegram

import (
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type fakeTGClient struct {
	mu         sync.Mutex
	lastMethod string
	lastForm   url.Values
}

func (f *fakeTGClient) Do(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	method := path.Base(req.URL.Path)
	form, _ := url.ParseQuery(string(body))

	f.mu.Lock()
	f.lastMethod = method
	f.lastForm = form
	f.mu.Unlock()

	var resp string
	if method == "getMe" {
		resp = `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"test","username":"test_bot"}}`
	} else {
		resp = `{"ok":true,"result":true}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(resp)),
	}, nil
}

func TestSetWebhookSendsSecretToken(t *testing.T) {
	fc := &fakeTGClient{}
	api, err := tgbotapi.NewBotAPIWithClient(
		"TEST_TOKEN",
		"https://fake.telegram.local/bot%s/%s",
		fc,
	)
	if err != nil {
		t.Fatalf("new bot api: %v", err)
	}
	b := &Bot{API: api}

	if err := b.SetWebhook("https://bot.example.com/tg/webhook", "super-secret"); err != nil {
		t.Fatalf("set webhook: %v", err)
	}

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.lastMethod != "setWebhook" {
		t.Fatalf("expected method setWebhook, got %s", fc.lastMethod)
	}
	if got := fc.lastForm.Get("url"); got != "https://bot.example.com/tg/webhook" {
		t.Fatalf("unexpected webhook url: %q", got)
	}
	if got := fc.lastForm.Get("secret_token"); got != "super-secret" {
		t.Fatalf("unexpected secret token: %q", got)
	}
}

func TestDeleteWebhookSendsDropPending(t *testing.T) {
	fc := &fakeTGClient{}
	api, err := tgbotapi.NewBotAPIWithClient(
		"TEST_TOKEN",
		"https://fake.telegram.local/bot%s/%s",
		fc,
	)
	if err != nil {
		t.Fatalf("new bot api: %v", err)
	}
	b := &Bot{API: api}

	if err := b.DeleteWebhook(); err != nil {
		t.Fatalf("delete webhook: %v", err)
	}

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.lastMethod != "deleteWebhook" {
		t.Fatalf("expected method deleteWebhook, got %s", fc.lastMethod)
	}
	if got := fc.lastForm.Get("drop_pending_updates"); got != "true" {
		t.Fatalf("expected drop_pending_updates=true, got %q", got)
	}
}
