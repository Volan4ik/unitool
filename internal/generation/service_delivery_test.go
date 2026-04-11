package generation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	db "unitool/internal/db/generated"
)

type tgAPIMock struct {
	mu    sync.Mutex
	calls map[string]int
	fail  map[string]bool
}

func newTGAPIMock(t *testing.T) (*tgbotapi.BotAPI, *tgAPIMock) {
	t.Helper()

	mock := &tgAPIMock{
		calls: make(map[string]int),
		fail:  make(map[string]bool),
	}
	bot, err := tgbotapi.NewBotAPIWithClient("TEST", "https://api.telegram.test/bot%s/%s", mock)
	if err != nil {
		t.Fatalf("new test bot api: %v", err)
	}
	return bot, mock
}

func (m *tgAPIMock) Do(r *http.Request) (*http.Response, error) {
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

func (m *tgAPIMock) setFail(method string, fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail[method] = fail
}

func (m *tgAPIMock) callCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[method]
}

func TestDownloadMediaBytes(t *testing.T) {
	t.Run("invalid URL", func(t *testing.T) {
		_, _, err := downloadMediaBytes(context.Background(), "://bad-url")
		if err == nil {
			t.Fatal("expected URL parse error")
		}
	})

	t.Run("request error", func(t *testing.T) {
		_, _, err := downloadMediaBytes(context.Background(), "http://127.0.0.1:1/no-server")
		if err == nil {
			t.Fatal("expected request error")
		}
	})
}

func TestSendDownloadedVideo(t *testing.T) {
	orig := downloadMediaFn
	t.Cleanup(func() { downloadMediaFn = orig })

	t.Run("sendVideo success", func(t *testing.T) {
		bot, mock := newTGAPIMock(t)
		downloadMediaFn = func(ctx context.Context, rawURL string) ([]byte, string, error) {
			return []byte("video"), "video/webm", nil
		}
		s := &Service{Bot: bot}
		job := db.GenerationJob{ID: 1, UserID: 2, ChatID: 3, Kind: "video"}
		if err := s.sendDownloadedVideo(context.Background(), job, "https://example.com/video.mp4"); err != nil {
			t.Fatalf("unexpected err=%v", err)
		}
		if mock.callCount("sendVideo") != 1 {
			t.Fatalf("sendVideo calls=%d want=1", mock.callCount("sendVideo"))
		}
	})

	t.Run("sendVideo fallback to sendDocument", func(t *testing.T) {
		bot, mock := newTGAPIMock(t)
		mock.setFail("sendVideo", true)
		downloadMediaFn = func(ctx context.Context, rawURL string) ([]byte, string, error) {
			return []byte("video"), "video/quicktime", nil
		}
		s := &Service{Bot: bot}
		job := db.GenerationJob{ID: 10, UserID: 20, ChatID: 30, Kind: "video"}
		if err := s.sendDownloadedVideo(context.Background(), job, "https://example.com/video.mov"); err != nil {
			t.Fatalf("unexpected err=%v", err)
		}
		if mock.callCount("sendVideo") != 1 || mock.callCount("sendDocument") != 1 {
			t.Fatalf("calls sendVideo=%d sendDocument=%d", mock.callCount("sendVideo"), mock.callCount("sendDocument"))
		}
	})

	t.Run("both video and document fail", func(t *testing.T) {
		bot, mock := newTGAPIMock(t)
		mock.setFail("sendVideo", true)
		mock.setFail("sendDocument", true)
		downloadMediaFn = func(ctx context.Context, rawURL string) ([]byte, string, error) {
			return []byte("video"), "video/mp4", nil
		}
		s := &Service{Bot: bot}
		job := db.GenerationJob{ID: 10, UserID: 20, ChatID: 30, Kind: "video"}
		err := s.sendDownloadedVideo(context.Background(), job, "https://example.com/video.mp4")
		if err == nil || !strings.Contains(err.Error(), "send downloaded video failed") {
			t.Fatalf("expected fallback error, got %v", err)
		}
	})

	t.Run("invalid output url", func(t *testing.T) {
		bot, _ := newTGAPIMock(t)
		s := &Service{Bot: bot}
		job := db.GenerationJob{ID: 10, UserID: 20, ChatID: 30, Kind: "video"}
		err := s.sendDownloadedVideo(context.Background(), job, "not-a-url")
		if err == nil || !strings.Contains(err.Error(), "not http url") {
			t.Fatalf("expected invalid url error, got %v", err)
		}
	})

	t.Run("download fails", func(t *testing.T) {
		bot, _ := newTGAPIMock(t)
		downloadMediaFn = func(ctx context.Context, rawURL string) ([]byte, string, error) {
			return nil, "", errors.New("download failed")
		}
		s := &Service{Bot: bot}
		job := db.GenerationJob{ID: 10, UserID: 20, ChatID: 30, Kind: "video"}
		err := s.sendDownloadedVideo(context.Background(), job, "https://example.com/video.mp4")
		if err == nil || !strings.Contains(err.Error(), "download failed") {
			t.Fatalf("expected download error, got %v", err)
		}
	})
}

func TestSendMediaResultAndInlineImage(t *testing.T) {
	orig := downloadMediaFn
	t.Cleanup(func() { downloadMediaFn = orig })

	t.Run("empty output fallback text", func(t *testing.T) {
		bot, mock := newTGAPIMock(t)
		s := &Service{Bot: bot}
		s.sendMediaResult(context.Background(), db.GenerationJob{ChatID: 1, Kind: "image"}, "")
		if mock.callCount("sendMessage") != 1 {
			t.Fatalf("sendMessage calls=%d want=1", mock.callCount("sendMessage"))
		}
	})

	t.Run("invalid output fallback text", func(t *testing.T) {
		bot, mock := newTGAPIMock(t)
		s := &Service{Bot: bot}
		s.sendMediaResult(context.Background(), db.GenerationJob{ChatID: 1, Kind: "image"}, "not-a-url")
		if mock.callCount("sendMessage") != 1 {
			t.Fatalf("sendMessage calls=%d want=1", mock.callCount("sendMessage"))
		}
	})

	t.Run("inline image success", func(t *testing.T) {
		bot, mock := newTGAPIMock(t)
		s := &Service{Bot: bot}
		s.sendMediaResult(context.Background(), db.GenerationJob{ChatID: 1, Kind: "image"}, "data:image/png;base64,aGVsbG8=")
		if mock.callCount("sendPhoto") != 1 {
			t.Fatalf("sendPhoto calls=%d want=1", mock.callCount("sendPhoto"))
		}
	})

	t.Run("inline image invalid fallback", func(t *testing.T) {
		bot, mock := newTGAPIMock(t)
		s := &Service{Bot: bot}
		s.sendInlineImageResult(context.Background(), db.GenerationJob{ChatID: 1, Kind: "image"}, "data:image/png;base64,@@@")
		if mock.callCount("sendMessage") != 1 {
			t.Fatalf("sendMessage calls=%d want=1", mock.callCount("sendMessage"))
		}
	})

	t.Run("video predownload path", func(t *testing.T) {
		bot, mock := newTGAPIMock(t)
		downloadMediaFn = func(ctx context.Context, rawURL string) ([]byte, string, error) {
			return []byte("video"), "video/mp4", nil
		}
		s := &Service{Bot: bot}
		s.sendMediaResult(context.Background(), db.GenerationJob{ChatID: 1, Kind: "video"}, "https://api.cometapi.com/v1/videos/abc/content")
		if mock.callCount("sendVideo") != 1 {
			t.Fatalf("sendVideo calls=%d want=1", mock.callCount("sendVideo"))
		}
	})
}
