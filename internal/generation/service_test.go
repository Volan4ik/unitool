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
	resp provider.ModelResponse
	err  error
}

func (f *fakeModelProvider) Generate(context.Context, provider.ModelRequest) (provider.ModelResponse, error) {
	return f.resp, f.err
}

func (f *fakeModelProvider) GenerateStream(context.Context, provider.ModelRequest, func(string) error) (provider.ModelResponse, error) {
	return provider.ModelResponse{}, errors.New("not implemented")
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
