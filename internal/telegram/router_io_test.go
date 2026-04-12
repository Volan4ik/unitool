package telegram

import (
	"context"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"unitool/internal/rate"
)

type telegramBotMock struct {
	mu    sync.Mutex
	calls map[string]int
	fail  map[string]bool
}

func newTelegramBotMock(t *testing.T) (*tgbotapi.BotAPI, *telegramBotMock) {
	t.Helper()

	mock := &telegramBotMock{
		calls: make(map[string]int),
		fail:  make(map[string]bool),
	}
	bot, err := tgbotapi.NewBotAPIWithClient("TEST", "https://api.telegram.test/bot%s/%s", mock)
	if err != nil {
		t.Fatalf("new test bot api: %v", err)
	}
	return bot, mock
}

func (m *telegramBotMock) Do(r *http.Request) (*http.Response, error) {
	method := path.Base(r.URL.Path)
	m.mu.Lock()
	m.calls[method]++
	fail := m.fail[method]
	m.mu.Unlock()

	var body string
	if method == "getMe" {
		body = `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"test","username":"bot"}}`
	} else if fail {
		body = `{"ok":false,"error_code":400,"description":"forced failure"}`
	} else if method == "editMessageText" {
		body = `{"ok":true,"result":true}`
	} else {
		body = `{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":1,"type":"private"}}}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

func (m *telegramBotMock) callCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[method]
}

func (m *telegramBotMock) setFail(method string, fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail[method] = fail
}

func TestStartGreetingAndHelpTexts(t *testing.T) {
	txt := startGreetingText(&tgbotapi.Message{From: &tgbotapi.User{FirstName: "<Vladimir>"}})
	if !strings.Contains(txt, "&lt;Vladimir&gt;") {
		t.Fatalf("name must be escaped, got=%q", txt)
	}
	if !strings.Contains(txt, "соглашение") {
		t.Fatalf("missing expected greeting section, got=%q", txt)
	}

	txtNoName := startGreetingText(&tgbotapi.Message{})
	if !strings.Contains(txtNoName, "Добро пожаловать!") {
		t.Fatalf("unexpected text without name: %q", txtNoName)
	}

	help := buildHelpText()
	if !strings.Contains(help, channelURL) {
		t.Fatalf("help text must reference channel URL, got=%q", help)
	}
}

func TestMessagePromptText(t *testing.T) {
	if got := messagePromptText(nil); got != "" {
		t.Fatalf("nil message got=%q want empty", got)
	}

	if got := messagePromptText(&tgbotapi.Message{Text: "  hello  ", Caption: "caption"}); got != "hello" {
		t.Fatalf("text must have priority, got=%q", got)
	}

	if got := messagePromptText(&tgbotapi.Message{Caption: "  caption prompt  "}); got != "caption prompt" {
		t.Fatalf("caption fallback got=%q", got)
	}

	if got := messagePromptText(&tgbotapi.Message{Text: "   ", Caption: "   "}); got != "" {
		t.Fatalf("whitespace only got=%q want empty", got)
	}
}

func TestRepliedMediaFileIDText(t *testing.T) {
	if txt, ok := repliedMediaFileIDText(nil); ok || txt != "" {
		t.Fatalf("nil message must be empty, got ok=%v txt=%q", ok, txt)
	}

	t.Run("animation", func(t *testing.T) {
		msg := &tgbotapi.Message{
			ReplyToMessage: &tgbotapi.Message{
				Animation: &tgbotapi.Animation{
					FileID:       "anim-file-id",
					FileUniqueID: "anim-uniq-id",
				},
			},
		}
		txt, ok := repliedMediaFileIDText(msg)
		if !ok {
			t.Fatal("expected ok")
		}
		if !strings.Contains(txt, "anim-file-id") {
			t.Fatalf("missing file id in text=%q", txt)
		}
		if !strings.Contains(txt, "START_GUIDE_ANIMATION=anim-file-id") {
			t.Fatalf("missing env hint in text=%q", txt)
		}
	})

	t.Run("document", func(t *testing.T) {
		msg := &tgbotapi.Message{
			ReplyToMessage: &tgbotapi.Message{
				Document: &tgbotapi.Document{
					FileID:       "doc-file-id",
					FileUniqueID: "doc-uniq-id",
					MimeType:     "video/mp4",
				},
			},
		}
		txt, ok := repliedMediaFileIDText(msg)
		if !ok {
			t.Fatal("expected ok")
		}
		if !strings.Contains(txt, "doc-file-id") || !strings.Contains(txt, "video/mp4") {
			t.Fatalf("unexpected text=%q", txt)
		}
	})
}

func TestSendStartGreeting(t *testing.T) {
	t.Run("without animation", func(t *testing.T) {
		api, mock := newTelegramBotMock(t)
		r := &Router{Bot: &Bot{API: api}}
		msg := &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 42},
			From: &tgbotapi.User{FirstName: "Vladimir"},
		}
		if err := r.sendStartGreeting(msg); err != nil {
			t.Fatalf("unexpected err=%v", err)
		}
		if mock.callCount("sendAnimation") != 0 {
			t.Fatalf("sendAnimation calls=%d want=0", mock.callCount("sendAnimation"))
		}
		if mock.callCount("sendMessage") != 1 {
			t.Fatalf("sendMessage calls=%d want=1", mock.callCount("sendMessage"))
		}
	})

	t.Run("with animation", func(t *testing.T) {
		api, mock := newTelegramBotMock(t)
		r := &Router{
			Bot:                 &Bot{API: api},
			StartGuideAnimation: "CgACAgIAAxkBAAIBQ2Y-file-id",
		}
		msg := &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 42},
			From: &tgbotapi.User{FirstName: "Vladimir"},
		}
		if err := r.sendStartGreeting(msg); err != nil {
			t.Fatalf("unexpected err=%v", err)
		}
		if mock.callCount("sendAnimation") != 1 {
			t.Fatalf("sendAnimation calls=%d want=1", mock.callCount("sendAnimation"))
		}
		if mock.callCount("sendMessage") != 1 {
			t.Fatalf("sendMessage calls=%d want=1", mock.callCount("sendMessage"))
		}
	})

	t.Run("animation failure does not block greeting text", func(t *testing.T) {
		api, mock := newTelegramBotMock(t)
		mock.setFail("sendAnimation", true)
		r := &Router{
			Bot:                 &Bot{API: api},
			StartGuideAnimation: "https://example.com/how-to-use.gif",
		}
		msg := &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 42},
			From: &tgbotapi.User{FirstName: "Vladimir"},
		}
		if err := r.sendStartGreeting(msg); err != nil {
			t.Fatalf("unexpected err=%v", err)
		}
		if mock.callCount("sendAnimation") != 1 {
			t.Fatalf("sendAnimation calls=%d want=1", mock.callCount("sendAnimation"))
		}
		if mock.callCount("sendMessage") != 1 {
			t.Fatalf("sendMessage calls=%d want=1", mock.callCount("sendMessage"))
		}
	})
}

func TestRouterStateMapsAndAdminCheck(t *testing.T) {
	r := &Router{
		adminIDs:    map[int64]struct{}{10: {}},
		pendingRefs: make(map[int64][]string),
	}
	if !r.isAdminTGID(10) {
		t.Fatal("expected admin")
	}
	if r.isAdminTGID(99) {
		t.Fatal("unexpected admin")
	}

	r.appendPendingReferences(5, []string{"a", "b"})
	r.appendPendingReferences(5, []string{"c"})
	refs, ok := r.peekPendingReferences(5)
	if !ok || len(refs) != 3 {
		t.Fatalf("unexpected refs=%v ok=%v", refs, ok)
	}
	r.clearPendingReferences(5)
	if refs, ok = r.peekPendingReferences(5); ok || len(refs) != 0 {
		t.Fatalf("expected empty refs after clear, refs=%v ok=%v", refs, ok)
	}
}

func TestRouterAllowGenerationRequest(t *testing.T) {
	r := &Router{}
	if !r.allowGenerationRequest(1, "image") {
		t.Fatal("expected allow when limiter is nil")
	}

	// limiter allows one request
	r.RL = rate.NewByKind(map[string]rate.Config{
		"image": {Max: 1, Refill: 0, Interval: time.Second},
	})
	if !r.allowGenerationRequest(1, "image") {
		t.Fatal("expected first request to pass")
	}
}

func TestRouterTextDeliveryHelpers(t *testing.T) {
	api, mock := newTelegramBotMock(t)
	r := &Router{Bot: &Bot{API: api}}

	if err := r.editMessageText(1, 0, "x"); err == nil {
		t.Fatal("expected error for zero message id")
	}

	if err := r.sendTextChunks(1, "abcdef"); err != nil {
		t.Fatalf("unexpected err=%v", err)
	}
	if mock.callCount("sendMessage") != 1 {
		t.Fatalf("sendMessage calls=%d want=1", mock.callCount("sendMessage"))
	}

	// One chunk edit + one additional send.
	if err := r.sendFinalStreamText(1, 10, strings.Repeat("a", tgMessageLimit+10)); err != nil {
		t.Fatalf("unexpected err=%v", err)
	}
	if mock.callCount("editMessageText") != 1 {
		t.Fatalf("editMessageText calls=%d want=1", mock.callCount("editMessageText"))
	}
	if mock.callCount("sendMessage") < 2 {
		t.Fatalf("expected extra sendMessage for overflow chunk, calls=%d", mock.callCount("sendMessage"))
	}

	if err := r.sendFinalStreamText(1, 10, ""); err != nil {
		t.Fatalf("empty text must be no-op, err=%v", err)
	}
}

func TestAllowGenerationRequestRateLimitedPath(t *testing.T) {
	api, mock := newTelegramBotMock(t)
	r := &Router{
		Bot: &Bot{API: api},
		RL: rate.NewByKind(map[string]rate.Config{
			"video": {Max: 0, Refill: 0, Interval: time.Second},
		}),
	}
	if r.allowGenerationRequest(100, "video") {
		t.Fatal("expected rate-limited request")
	}
	if mock.callCount("sendMessage") != 1 {
		t.Fatalf("sendMessage calls=%d want=1", mock.callCount("sendMessage"))
	}
}

func TestAnswerCallback(t *testing.T) {
	api, mock := newTelegramBotMock(t)
	r := &Router{Bot: &Bot{API: api}}
	if err := r.answerCallback("cb-id", "ok"); err != nil {
		t.Fatalf("unexpected err=%v", err)
	}
	if mock.callCount("answerCallbackQuery") != 1 {
		t.Fatalf("answerCallbackQuery calls=%d want=1", mock.callCount("answerCallbackQuery"))
	}
}

func TestCallbackChatIDWithMessageAndUser(t *testing.T) {
	id, ok := callbackChatID(&tgbotapi.CallbackQuery{
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 42}},
	})
	if !ok || id != 42 {
		t.Fatalf("got id=%d ok=%v", id, ok)
	}
	id, ok = callbackChatID(&tgbotapi.CallbackQuery{
		From: &tgbotapi.User{ID: 99},
	})
	if !ok || id != 99 {
		t.Fatalf("got id=%d ok=%v", id, ok)
	}
}

func TestWithUpdateIDNilContext(t *testing.T) {
	if got := withUpdateID(nil, 12); got != nil {
		t.Fatalf("expected nil context, got=%v", got)
	}
	if got := updateIDFromContext(context.Background()); got != 0 {
		t.Fatalf("unexpected id=%d", got)
	}
}
