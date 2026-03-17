package generation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	db "unitool/internal/db/generated"
)

type fakeTelegramAPI struct {
	mu        sync.Mutex
	calls     map[string]int
	failFirst map[string]int
	texts     []string
}

func newFakeTelegramAPI() *fakeTelegramAPI {
	return &fakeTelegramAPI{
		calls:     make(map[string]int),
		failFirst: make(map[string]int),
	}
}

func (f *fakeTelegramAPI) setFailFirst(method string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failFirst[method] = n
}

func (f *fakeTelegramAPI) callCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func (f *fakeTelegramAPI) lastText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.texts) == 0 {
		return ""
	}
	return f.texts[len(f.texts)-1]
}

func (f *fakeTelegramAPI) handleRequest(r *http.Request) *http.Response {
	bodyBytes, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(bodyBytes))
	method := path.Base(r.URL.Path)

	f.mu.Lock()
	f.calls[method]++
	if txt := strings.TrimSpace(form.Get("text")); txt != "" {
		f.texts = append(f.texts, txt)
	}
	remain := f.failFirst[method]
	if remain > 0 {
		f.failFirst[method] = remain - 1
		f.mu.Unlock()
		return jsonResponse(map[string]any{
			"ok":          false,
			"error_code":  500,
			"description": "forced failure",
		})
	}
	f.mu.Unlock()

	switch method {
	case "getMe":
		return jsonResponse(map[string]any{
			"ok": true,
			"result": map[string]any{
				"id":         1,
				"is_bot":     true,
				"first_name": "test",
				"username":   "test_bot",
			},
		})
	default:
		return jsonResponse(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": 1,
				"date":       1,
				"chat": map[string]any{
					"id":   1,
					"type": "private",
				},
			},
		})
	}
}

type fakeHTTPClient struct {
	fake *fakeTelegramAPI
}

func (c *fakeHTTPClient) Do(r *http.Request) (*http.Response, error) {
	return c.fake.handleRequest(r), nil
}

func jsonResponse(v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(b))),
	}
}

func newTestBot(t *testing.T, fake *fakeTelegramAPI) *tgbotapi.BotAPI {
	t.Helper()
	bot, err := tgbotapi.NewBotAPIWithClient(
		"TEST_TOKEN",
		"https://fake.telegram.local/bot%s/%s",
		&fakeHTTPClient{fake: fake},
	)
	if err != nil {
		t.Fatalf("new bot api: %v", err)
	}
	return bot
}

func TestIsHTTPURL(t *testing.T) {
	if !isHTTPURL("https://example.com/file.jpg") {
		t.Fatal("expected https url to be valid")
	}
	if !isHTTPURL("http://example.com/file.mp4") {
		t.Fatal("expected http url to be valid")
	}
	if isHTTPURL("ftp://example.com/file.jpg") {
		t.Fatal("expected ftp url to be invalid")
	}
	if isHTTPURL("not-a-url") {
		t.Fatal("expected plain text to be invalid")
	}
}

func TestSendMediaResultImageSuccess(t *testing.T) {
	fake := newFakeTelegramAPI()
	svc := &Service{Bot: newTestBot(t, fake)}

	svc.sendMediaResult(context.Background(), db.GenerationJob{
		ID:     10,
		UserID: 100,
		ChatID: 1000,
		Kind:   "image",
	}, "https://cdn.example.com/img.png")

	if got := fake.callCount("sendPhoto"); got != 1 {
		t.Fatalf("expected sendPhoto=1, got %d", got)
	}
	if got := fake.callCount("sendMessage"); got != 0 {
		t.Fatalf("expected sendMessage=0, got %d", got)
	}
}

func TestSendMediaResultRetryThenSuccess(t *testing.T) {
	fake := newFakeTelegramAPI()
	fake.setFailFirst("sendVideo", 1)
	svc := &Service{Bot: newTestBot(t, fake)}

	svc.sendMediaResult(context.Background(), db.GenerationJob{
		ID:     11,
		UserID: 101,
		ChatID: 1001,
		Kind:   "video",
	}, "https://cdn.example.com/vid.mp4")

	if got := fake.callCount("sendVideo"); got != 2 {
		t.Fatalf("expected sendVideo=2 (retry), got %d", got)
	}
	if got := fake.callCount("sendMessage"); got != 0 {
		t.Fatalf("expected no text fallback, got sendMessage=%d", got)
	}
}

func TestSendMediaResultFallbackInvalidURL(t *testing.T) {
	fake := newFakeTelegramAPI()
	svc := &Service{Bot: newTestBot(t, fake)}

	svc.sendMediaResult(context.Background(), db.GenerationJob{
		ID:     12,
		UserID: 102,
		ChatID: 1002,
		Kind:   "image",
	}, "bad-output")

	if got := fake.callCount("sendPhoto"); got != 0 {
		t.Fatalf("expected sendPhoto=0, got %d", got)
	}
	if got := fake.callCount("sendMessage"); got != 1 {
		t.Fatalf("expected sendMessage=1, got %d", got)
	}
	if txt := fake.lastText(); !strings.Contains(txt, "некорректна") {
		t.Fatalf("expected invalid-url fallback text, got %q", txt)
	}
}

func TestSendMediaResultFallbackEmptyOutput(t *testing.T) {
	fake := newFakeTelegramAPI()
	svc := &Service{Bot: newTestBot(t, fake)}

	svc.sendMediaResult(context.Background(), db.GenerationJob{
		ID:     13,
		UserID: 103,
		ChatID: 1003,
		Kind:   "video",
	}, "")

	if got := fake.callCount("sendVideo"); got != 0 {
		t.Fatalf("expected sendVideo=0, got %d", got)
	}
	if got := fake.callCount("sendMessage"); got != 1 {
		t.Fatalf("expected sendMessage=1, got %d", got)
	}
	if txt := fake.lastText(); !strings.Contains(txt, "не вернул ссылку") {
		t.Fatalf("expected empty-output fallback text, got %q", txt)
	}
}
