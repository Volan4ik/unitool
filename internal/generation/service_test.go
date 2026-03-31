package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	db "unitool/internal/db/generated"
	"unitool/pkg/provider"
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

func TestShouldDownloadVideoFirst(t *testing.T) {
	if !shouldDownloadVideoFirst("https://api.cometapi.com/v1/videos/task_1/content") {
		t.Fatal("expected comet content URL to be pre-downloaded")
	}
	if shouldDownloadVideoFirst("https://cdn.example.com/video.mp4") {
		t.Fatal("did not expect non-comet URL to be pre-downloaded")
	}
	if shouldDownloadVideoFirst("not-a-url") {
		t.Fatal("did not expect invalid URL to be pre-downloaded")
	}
}

func stubDownloadMedia(t *testing.T, fn func(context.Context, string) ([]byte, string, error)) {
	t.Helper()
	prev := downloadMediaFn
	downloadMediaFn = fn
	t.Cleanup(func() {
		downloadMediaFn = prev
	})
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
	fake.setFailFirst("sendDocument", 1)
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
	if got := fake.callCount("sendDocument"); got != 1 {
		t.Fatalf("expected sendDocument=1 (fallback probe), got %d", got)
	}
	if got := fake.callCount("sendMessage"); got != 0 {
		t.Fatalf("expected no text fallback, got sendMessage=%d", got)
	}
}

func TestSendMediaResultVideoSentAsDocumentFallback(t *testing.T) {
	fake := newFakeTelegramAPI()
	fake.setFailFirst("sendVideo", 1)
	svc := &Service{Bot: newTestBot(t, fake)}

	svc.sendMediaResult(context.Background(), db.GenerationJob{
		ID:     16,
		UserID: 106,
		ChatID: 1006,
		Kind:   "video",
	}, "https://cdn.example.com/vid.bin")

	if got := fake.callCount("sendVideo"); got != 1 {
		t.Fatalf("expected sendVideo=1, got %d", got)
	}
	if got := fake.callCount("sendDocument"); got != 1 {
		t.Fatalf("expected sendDocument=1, got %d", got)
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
	if txt := fake.lastText(); !strings.Contains(txt, "не удалось обработать ссылку") {
		t.Fatalf("expected invalid-url fallback text, got %q", txt)
	}
}

func TestSendMediaResultInlineDataImageSuccess(t *testing.T) {
	fake := newFakeTelegramAPI()
	svc := &Service{Bot: newTestBot(t, fake)}

	svc.sendMediaResult(context.Background(), db.GenerationJob{
		ID:     14,
		UserID: 104,
		ChatID: 1004,
		Kind:   "image",
	}, "![image](data:image/png;base64,aGVsbG8=)")

	if got := fake.callCount("sendPhoto"); got != 1 {
		t.Fatalf("expected sendPhoto=1 for inline image, got %d", got)
	}
	if got := fake.callCount("sendMessage"); got != 0 {
		t.Fatalf("expected sendMessage=0 for inline image, got %d", got)
	}
}

func TestSendMediaResultInlineDataImageDecodeFallback(t *testing.T) {
	fake := newFakeTelegramAPI()
	svc := &Service{Bot: newTestBot(t, fake)}

	svc.sendMediaResult(context.Background(), db.GenerationJob{
		ID:     15,
		UserID: 105,
		ChatID: 1005,
		Kind:   "image",
	}, "data:image/png;base64,%%%")

	if got := fake.callCount("sendPhoto"); got != 0 {
		t.Fatalf("expected sendPhoto=0 for broken inline image, got %d", got)
	}
	if got := fake.callCount("sendMessage"); got != 1 {
		t.Fatalf("expected sendMessage=1 for broken inline image, got %d", got)
	}
	if txt := fake.lastText(); !strings.Contains(txt, "встроенное изображение") {
		t.Fatalf("expected inline-image fallback text, got %q", txt)
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

func TestSendMediaResultNoRawLinkLeakOnFailure(t *testing.T) {
	fake := newFakeTelegramAPI()
	fake.setFailFirst("sendVideo", 3)
	fake.setFailFirst("sendDocument", 3)
	svc := &Service{Bot: newTestBot(t, fake)}
	stubDownloadMedia(t, func(context.Context, string) ([]byte, string, error) {
		return nil, "", errors.New("download failed")
	})

	leakyURL := "https://api.cometapi.com/v1/videos/task_abc/content"
	svc.sendMediaResult(context.Background(), db.GenerationJob{
		ID:     17,
		UserID: 107,
		ChatID: 1007,
		Kind:   "video",
	}, leakyURL)

	if got := fake.callCount("sendMessage"); got != 1 {
		t.Fatalf("expected sendMessage=1, got %d", got)
	}
	if txt := fake.lastText(); strings.Contains(txt, leakyURL) {
		t.Fatalf("fallback text must not leak raw url, got %q", txt)
	}
}

func TestSendMediaResultVideoDownloadedFallbackSuccess(t *testing.T) {
	fake := newFakeTelegramAPI()
	svc := &Service{Bot: newTestBot(t, fake)}
	stubDownloadMedia(t, func(context.Context, string) ([]byte, string, error) {
		return []byte("fake-video-bytes"), "video/mp4", nil
	})

	svc.sendMediaResult(context.Background(), db.GenerationJob{
		ID:     18,
		UserID: 108,
		ChatID: 1008,
		Kind:   "video",
	}, "https://api.cometapi.com/v1/videos/task_abc/content")

	// Comet content URLs should skip URL-send attempts and go straight to
	// downloaded upload flow.
	if got := fake.callCount("sendVideo"); got != 1 {
		t.Fatalf("expected sendVideo=1 with pre-download flow, got %d", got)
	}
	if got := fake.callCount("sendDocument"); got != 0 {
		t.Fatalf("expected sendDocument=0 with pre-download flow, got %d", got)
	}
	if got := fake.callCount("sendMessage"); got != 0 {
		t.Fatalf("expected no text fallback on downloaded send success, got %d", got)
	}
}

type fakeRow struct {
	err error
}

func (r fakeRow) Scan(dest ...interface{}) error { return r.err }

type fakeGenerationDBTX struct {
	mu sync.Mutex

	requeueRows int64
	requeueErr  error

	markFailedRows int64
	markFailedErr  error

	failReqRows int64
	failReqErr  error
	refundErr   error

	finishRows int64
	finishErr  error

	markDoneErr error

	insertAssistantErr error

	requeueCalls    int
	markFailedCalls int
	failReqCalls    int
	refundCalls     int
	finishCalls     int
	markDoneCalls   int
	insertMsgCalls  int

	requeueQuery    string
	markFailedQuery string
}

func (f *fakeGenerationDBTX) Exec(_ context.Context, query string, _ ...interface{}) (pgconn.CommandTag, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.Contains(query, "name: RequeueGenerationJob"):
		f.requeueCalls++
		f.requeueQuery = query
		if f.requeueErr != nil {
			return pgconn.NewCommandTag("UPDATE 0"), f.requeueErr
		}
		return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", f.requeueRows)), nil
	case strings.Contains(query, "name: MarkGenerationJobFailed"):
		f.markFailedCalls++
		f.markFailedQuery = query
		if f.markFailedErr != nil {
			return pgconn.NewCommandTag("UPDATE 0"), f.markFailedErr
		}
		return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", f.markFailedRows)), nil
	case strings.Contains(query, "name: FinishGenerationRequest"):
		f.finishCalls++
		if f.finishErr != nil {
			return pgconn.NewCommandTag("UPDATE 0"), f.finishErr
		}
		rows := f.finishRows
		if rows == 0 {
			rows = 1
		}
		return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", rows)), nil
	case strings.Contains(query, "name: FailGenerationRequest"):
		f.failReqCalls++
		if f.failReqErr != nil {
			return pgconn.NewCommandTag("UPDATE 0"), f.failReqErr
		}
		rows := f.failReqRows
		if rows == 0 {
			rows = 1
		}
		return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", rows)), nil
	case strings.Contains(query, "name: RefundText"),
		strings.Contains(query, "name: RefundImage"),
		strings.Contains(query, "name: RefundVideo"):
		f.refundCalls++
		return pgconn.NewCommandTag("INSERT 0 1"), f.refundErr
	default:
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
}

func (f *fakeGenerationDBTX) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("unexpected query call in test")
}

func (f *fakeGenerationDBTX) QueryRow(_ context.Context, query string, _ ...interface{}) pgx.Row {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.Contains(query, "name: MarkGenerationJobDone"):
		f.markDoneCalls++
		return fakeRow{err: f.markDoneErr}
	case strings.Contains(query, "name: InsertChatMessage"):
		f.insertMsgCalls++
		return fakeRow{err: f.insertAssistantErr}
	default:
		return fakeRow{err: errors.New("unexpected queryrow call in test")}
	}
}

type fakeModelProvider struct {
	mu   sync.Mutex
	resp provider.ModelResponse
	err  error
	req  provider.ModelRequest
}

func (f *fakeModelProvider) Generate(_ context.Context, req provider.ModelRequest) (provider.ModelResponse, error) {
	f.mu.Lock()
	f.req = req
	f.mu.Unlock()
	return f.resp, f.err
}

func (f *fakeModelProvider) GenerateStream(context.Context, provider.ModelRequest, func(string) error) (provider.ModelResponse, error) {
	return provider.ModelResponse{}, errors.New("not implemented")
}

func (f *fakeModelProvider) lastRequest() provider.ModelRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.req
}

func TestHandleFailedAttemptRequeueSuccess(t *testing.T) {
	fakeTG := newFakeTelegramAPI()
	fakeDB := &fakeGenerationDBTX{requeueRows: 1}
	svc := &Service{
		Bot: newTestBot(t, fakeTG),
		Q:   db.New(fakeDB),
	}
	job := db.GenerationJob{
		ID:                  100,
		GenerationRequestID: 200,
		UserID:              300,
		ChatID:              400,
		Kind:                "image",
		Attempts:            1,
		MaxAttempts:         3,
	}
	svc.handleFailedAttempt(context.Background(), job, errors.New("provider timeout"), 100)

	if fakeDB.requeueCalls != 1 {
		t.Fatalf("expected requeue call=1, got %d", fakeDB.requeueCalls)
	}
	if fakeDB.markFailedCalls != 0 || fakeDB.failReqCalls != 0 || fakeDB.refundCalls != 0 {
		t.Fatalf("expected no finalization calls, got markFailed=%d failReq=%d refund=%d", fakeDB.markFailedCalls, fakeDB.failReqCalls, fakeDB.refundCalls)
	}
	if !strings.Contains(fakeDB.requeueQuery, "status = 'running'") {
		t.Fatalf("expected running-status guard in requeue query, got %q", fakeDB.requeueQuery)
	}
	if got := fakeTG.callCount("sendMessage"); got != 0 {
		t.Fatalf("expected no user message on requeue success, got sendMessage=%d", got)
	}
}

func TestHandleFailedAttemptRequeueSkippedStaleRunner(t *testing.T) {
	fakeTG := newFakeTelegramAPI()
	fakeDB := &fakeGenerationDBTX{requeueRows: 0}
	svc := &Service{
		Bot: newTestBot(t, fakeTG),
		Q:   db.New(fakeDB),
	}
	job := db.GenerationJob{
		ID:                  101,
		GenerationRequestID: 201,
		UserID:              301,
		ChatID:              401,
		Kind:                "video",
		Attempts:            1,
		MaxAttempts:         3,
	}
	svc.handleFailedAttempt(context.Background(), job, errors.New("provider timeout"), 100)

	if fakeDB.requeueCalls != 1 {
		t.Fatalf("expected requeue call=1, got %d", fakeDB.requeueCalls)
	}
	if fakeDB.markFailedCalls != 0 || fakeDB.failReqCalls != 0 || fakeDB.refundCalls != 0 {
		t.Fatalf("expected stale skip without finalization, got markFailed=%d failReq=%d refund=%d", fakeDB.markFailedCalls, fakeDB.failReqCalls, fakeDB.refundCalls)
	}
	if got := fakeTG.callCount("sendMessage"); got != 0 {
		t.Fatalf("expected no user message on stale-skip requeue, got sendMessage=%d", got)
	}
}

func TestHandleFailedAttemptRequeueErrorFallbackFinalize(t *testing.T) {
	fakeTG := newFakeTelegramAPI()
	fakeDB := &fakeGenerationDBTX{
		requeueErr:     errors.New("requeue failed"),
		markFailedRows: 1,
	}
	svc := &Service{
		Bot: newTestBot(t, fakeTG),
		Q:   db.New(fakeDB),
	}
	job := db.GenerationJob{
		ID:                  102,
		GenerationRequestID: 202,
		UserID:              302,
		ChatID:              402,
		Kind:                "image",
		Attempts:            1,
		MaxAttempts:         3,
	}
	svc.handleFailedAttempt(context.Background(), job, errors.New("provider timeout"), 100)

	if fakeDB.requeueCalls != 1 {
		t.Fatalf("expected requeue call=1, got %d", fakeDB.requeueCalls)
	}
	if fakeDB.markFailedCalls != 1 || fakeDB.failReqCalls != 1 || fakeDB.refundCalls != 1 {
		t.Fatalf("expected fallback finalization calls markFailed=1 failReq=1 refund=1, got markFailed=%d failReq=%d refund=%d", fakeDB.markFailedCalls, fakeDB.failReqCalls, fakeDB.refundCalls)
	}
	if !strings.Contains(fakeDB.markFailedQuery, "status = 'running'") {
		t.Fatalf("expected running-status guard in mark-failed query, got %q", fakeDB.markFailedQuery)
	}
	if got := fakeTG.callCount("sendMessage"); got != 1 {
		t.Fatalf("expected one user failure message, got sendMessage=%d", got)
	}
}

func TestHandleFailedAttemptMaxAttemptsStaleRunnerSkip(t *testing.T) {
	fakeTG := newFakeTelegramAPI()
	fakeDB := &fakeGenerationDBTX{
		markFailedRows: 0,
	}
	svc := &Service{
		Bot: newTestBot(t, fakeTG),
		Q:   db.New(fakeDB),
	}
	job := db.GenerationJob{
		ID:                  103,
		GenerationRequestID: 203,
		UserID:              303,
		ChatID:              403,
		Kind:                "video",
		Attempts:            3,
		MaxAttempts:         3,
	}
	svc.handleFailedAttempt(context.Background(), job, errors.New("provider timeout"), 100)

	if fakeDB.markFailedCalls != 1 {
		t.Fatalf("expected markFailed call=1, got %d", fakeDB.markFailedCalls)
	}
	if fakeDB.failReqCalls != 0 || fakeDB.refundCalls != 0 {
		t.Fatalf("expected stale-skip to avoid request/refund writes, got failReq=%d refund=%d", fakeDB.failReqCalls, fakeDB.refundCalls)
	}
	if !strings.Contains(fakeDB.markFailedQuery, "status = 'running'") {
		t.Fatalf("expected running-status guard in mark-failed query, got %q", fakeDB.markFailedQuery)
	}
	if got := fakeTG.callCount("sendMessage"); got != 0 {
		t.Fatalf("expected no user message on stale-skip finalization, got sendMessage=%d", got)
	}
}

func TestHandleFailedAttemptRefundFailureUsesPendingMessage(t *testing.T) {
	fakeTG := newFakeTelegramAPI()
	fakeDB := &fakeGenerationDBTX{
		markFailedRows: 1,
		refundErr:      errors.New("db timeout"),
	}
	svc := &Service{
		Bot: newTestBot(t, fakeTG),
		Q:   db.New(fakeDB),
	}
	job := db.GenerationJob{
		ID:                  104,
		GenerationRequestID: 204,
		UserID:              304,
		ChatID:              404,
		Kind:                "image",
		Attempts:            3,
		MaxAttempts:         3,
	}

	svc.handleFailedAttempt(context.Background(), job, errors.New("provider timeout"), 100)

	if got := fakeTG.callCount("sendMessage"); got != 1 {
		t.Fatalf("expected one user message, got sendMessage=%d", got)
	}
	msg := fakeTG.lastText()
	if strings.Contains(msg, "Попытка возвращена") {
		t.Fatalf("expected no definitive refund message on refund failure, got %q", msg)
	}
	if !strings.Contains(msg, "в обработке") {
		t.Fatalf("expected pending refund message, got %q", msg)
	}
}

func TestHandleFailedAttemptModerationSkipsRetryAndNotifies(t *testing.T) {
	fakeTG := newFakeTelegramAPI()
	fakeDB := &fakeGenerationDBTX{
		markFailedRows: 1,
	}
	svc := &Service{
		Bot: newTestBot(t, fakeTG),
		Q:   db.New(fakeDB),
	}
	job := db.GenerationJob{
		ID:                  107,
		GenerationRequestID: 207,
		UserID:              307,
		ChatID:              407,
		Kind:                "video",
		Attempts:            1,
		MaxAttempts:         3,
	}
	moderationErr := errors.New("comet video failed: The request is blocked by our moderation system when checking inputs. Possible reasons: sexual.")

	svc.handleFailedAttempt(context.Background(), job, moderationErr, 100)

	if fakeDB.requeueCalls != 0 {
		t.Fatalf("expected no requeue for moderation error, got requeue=%d", fakeDB.requeueCalls)
	}
	if fakeDB.markFailedCalls != 1 || fakeDB.failReqCalls != 1 || fakeDB.refundCalls != 1 {
		t.Fatalf("expected finalization calls markFailed=1 failReq=1 refund=1, got markFailed=%d failReq=%d refund=%d", fakeDB.markFailedCalls, fakeDB.failReqCalls, fakeDB.refundCalls)
	}
	if got := fakeTG.callCount("sendMessage"); got != 1 {
		t.Fatalf("expected one user message, got sendMessage=%d", got)
	}
	msg := fakeTG.lastText()
	if !strings.Contains(msg, "Промпт не соответствует правилам сервиса") {
		t.Fatalf("expected moderation notice, got %q", msg)
	}
	if !strings.Contains(msg, promptRulesURL) {
		t.Fatalf("expected rules link in moderation notice, got %q", msg)
	}
}

func TestProcessJobMarkDoneNoRowsSkipsSuccessSideEffects(t *testing.T) {
	fakeTG := newFakeTelegramAPI()
	fakeDB := &fakeGenerationDBTX{
		markDoneErr: pgx.ErrNoRows,
	}
	svc := &Service{
		Bot:        newTestBot(t, fakeTG),
		Q:          db.New(fakeDB),
		Prov:       &fakeModelProvider{resp: provider.ModelResponse{Output: "https://cdn.example.com/x.png", Tokens: 8}},
		GenTimeout: 5 * time.Second,
	}
	job := db.GenerationJob{
		ID:                  105,
		GenerationRequestID: 205,
		UserID:              305,
		ChatID:              405,
		Kind:                "image",
		Model:               "m",
		Provider:            "p",
		Prompt:              "test",
	}

	svc.processJob(context.Background(), job)

	if fakeDB.markDoneCalls != 1 {
		t.Fatalf("expected mark-done call=1, got %d", fakeDB.markDoneCalls)
	}
	if fakeDB.finishCalls != 0 || fakeDB.insertMsgCalls != 0 {
		t.Fatalf("expected no success side-effects, got finish=%d insert=%d", fakeDB.finishCalls, fakeDB.insertMsgCalls)
	}
	if got := fakeTG.callCount("sendPhoto"); got != 0 {
		t.Fatalf("expected no media send when mark-done skipped, got sendPhoto=%d", got)
	}
}

func TestProcessJobSuccessAfterGuardRunsSideEffects(t *testing.T) {
	fakeTG := newFakeTelegramAPI()
	fakeDB := &fakeGenerationDBTX{}
	svc := &Service{
		Bot:        newTestBot(t, fakeTG),
		Q:          db.New(fakeDB),
		Prov:       &fakeModelProvider{resp: provider.ModelResponse{Output: "https://cdn.example.com/y.png", Tokens: 11}},
		GenTimeout: 5 * time.Second,
	}
	job := db.GenerationJob{
		ID:                  106,
		GenerationRequestID: 206,
		UserID:              306,
		ChatID:              406,
		Kind:                "image",
		Model:               "m",
		Provider:            "p",
		Prompt:              "test",
	}

	svc.processJob(context.Background(), job)

	if fakeDB.markDoneCalls != 1 {
		t.Fatalf("expected mark-done call=1, got %d", fakeDB.markDoneCalls)
	}
	if fakeDB.finishCalls != 1 || fakeDB.insertMsgCalls != 1 {
		t.Fatalf("expected success side-effects once, got finish=%d insert=%d", fakeDB.finishCalls, fakeDB.insertMsgCalls)
	}
	if got := fakeTG.callCount("sendPhoto"); got != 1 {
		t.Fatalf("expected media send once, got sendPhoto=%d", got)
	}
}

func TestSplitVideoPromptInputReference(t *testing.T) {
	prompt, ref := splitVideoPromptInputReference("input_reference=https://cdn.example.com/ref.png\ncinematic shot")
	if ref != "https://cdn.example.com/ref.png" {
		t.Fatalf("unexpected input reference: %q", ref)
	}
	if prompt != "cinematic shot" {
		t.Fatalf("unexpected prompt: %q", prompt)
	}

	prompt, ref = splitVideoPromptInputReference("cinematic shot\ninput_reference: https://cdn.example.com/ref2.png")
	if ref != "https://cdn.example.com/ref2.png" {
		t.Fatalf("unexpected input reference for colon syntax: %q", ref)
	}
	if prompt != "cinematic shot" {
		t.Fatalf("unexpected prompt for trailing reference line: %q", prompt)
	}

	prompt, ref = splitVideoPromptInputReference("cinematic shot only")
	if ref != "" {
		t.Fatalf("did not expect input reference, got %q", ref)
	}
	if prompt != "cinematic shot only" {
		t.Fatalf("unexpected prompt without reference: %q", prompt)
	}
}

func TestProcessJobVideoPassesInputReferenceParam(t *testing.T) {
	fakeTG := newFakeTelegramAPI()
	fakeDB := &fakeGenerationDBTX{}
	fakeProv := &fakeModelProvider{resp: provider.ModelResponse{Output: "https://cdn.example.com/out.mp4", Tokens: 9}}
	svc := &Service{
		Bot:        newTestBot(t, fakeTG),
		Q:          db.New(fakeDB),
		Prov:       fakeProv,
		GenTimeout: 5 * time.Second,
	}
	job := db.GenerationJob{
		ID:                  107,
		GenerationRequestID: 207,
		UserID:              307,
		ChatID:              407,
		Kind:                "video",
		Model:               "kling",
		Provider:            "comet",
		Prompt:              "input_reference=https://cdn.example.com/ref.png\ncinematic drone shot",
	}

	svc.processJob(context.Background(), job)

	req := fakeProv.lastRequest()
	if req.Input != "cinematic drone shot" {
		t.Fatalf("expected cleaned prompt, got %q", req.Input)
	}
	if got, _ := req.Params["kind"].(string); got != "video" {
		t.Fatalf("expected kind=video, got %q", got)
	}
	if got, _ := req.Params["input_reference"].(string); got != "https://cdn.example.com/ref.png" {
		t.Fatalf("expected input_reference to be passed, got %q", got)
	}
}
