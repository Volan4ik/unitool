package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "unitool/internal/db/generated"
	"unitool/internal/rate"
	"unitool/pkg/provider"
)

type recordingTGRequest struct {
	method string
	form   url.Values
}

type recordingTGClient struct {
	mu        sync.Mutex
	requests  []recordingTGRequest
	messageID int
}

func (f *recordingTGClient) Do(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	method := path.Base(req.URL.Path)
	form, _ := url.ParseQuery(string(body))

	f.mu.Lock()
	f.requests = append(f.requests, recordingTGRequest{method: method, form: form})
	if method == "sendMessage" {
		f.messageID++
	}
	messageID := f.messageID
	f.mu.Unlock()

	var resp string
	switch method {
	case "getMe":
		resp = `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"test","username":"test_bot"}}`
	case "sendMessage":
		chatID, _ := strconv.ParseInt(form.Get("chat_id"), 10, 64)
		text := form.Get("text")
		resp = fmt.Sprintf(`{"ok":true,"result":{"message_id":%d,"date":0,"chat":{"id":%d,"type":"private"},"text":%q}}`, messageID, chatID, text)
	default:
		resp = `{"ok":true,"result":true}`
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(resp)),
	}, nil
}

func (f *recordingTGClient) textsForMethod(method string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var texts []string
	for _, req := range f.requests {
		if req.method == method {
			texts = append(texts, req.form.Get("text"))
		}
	}
	return texts
}

type fakeProvider struct {
	mu           sync.Mutex
	requests     []provider.ModelRequest
	streamOutput string
	streamTokens int
	streamChunks []string
	streamErr    error
	customStream bool
}

func (f *fakeProvider) Generate(context.Context, provider.ModelRequest) (provider.ModelResponse, error) {
	return provider.ModelResponse{}, fmt.Errorf("unexpected Generate call")
}

func (f *fakeProvider) GenerateStream(_ context.Context, req provider.ModelRequest, onDelta func(string) error) (provider.ModelResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	chunks := append([]string(nil), f.streamChunks...)
	output := f.streamOutput
	tokens := f.streamTokens
	streamErr := f.streamErr
	customStream := f.customStream
	f.mu.Unlock()

	if !customStream && len(chunks) == 0 && output == "" && streamErr == nil {
		chunks = []string{"text-response"}
		output = "text-response"
		tokens = 12
	}
	if onDelta != nil {
		for _, chunk := range chunks {
			if err := onDelta(chunk); err != nil {
				return provider.ModelResponse{}, err
			}
		}
	}
	if streamErr != nil {
		return provider.ModelResponse{}, streamErr
	}
	return provider.ModelResponse{Output: output, Tokens: tokens}, nil
}

func (f *fakeProvider) setStreamResult(output string, tokens int, chunks []string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streamOutput = output
	f.streamTokens = tokens
	f.streamChunks = append([]string(nil), chunks...)
	f.streamErr = err
	f.customStream = true
}

func (f *fakeProvider) lastRequest() (provider.ModelRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return provider.ModelRequest{}, false
	}
	return f.requests[len(f.requests)-1], true
}

type fakeDBTX struct {
	user                        db.User
	session                     db.GetUserSessionRow
	conversationID              pgtype.UUID
	generationRequest           db.GenerationRequest
	generationJob               db.GenerationJob
	upsertUserSessionCalls      int
	insertChatMessageCalls      int
	finishGenerationCalls       int
	failGenerationCalls         int
	refundTextCalls             int
	spendImageCalls             int
	spendVideoCalls             int
	enqueueGenerationJobCalls   int
	lastFailedGenerationStatus  string
	lastFailedGenerationMessage string
}

func newFakeDBTX() *fakeDBTX {
	now := pgtype.Timestamptz{}
	return &fakeDBTX{
		user: db.User{
			ID:           1,
			TgID:         42,
			Username:     pgtype.Text{String: "tester", Valid: true},
			FirstName:    pgtype.Text{String: "Test", Valid: true},
			LangCode:     pgtype.Text{String: "ru", Valid: true},
			TextBalance:  10,
			ImageBalance: 10,
			VideoBalance: 10,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		session: db.GetUserSessionRow{
			Mode:  "text",
			Model: "GPT-5 Nano",
		},
		conversationID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		generationRequest: db.GenerationRequest{
			ID:        77,
			UserID:    1,
			Provider:  "comet",
			Model:     "gpt-5-nano",
			Kind:      "text",
			Status:    "running",
			CreatedAt: now,
		},
		generationJob: db.GenerationJob{
			ID:                  88,
			GenerationRequestID: 77,
			UserID:              1,
			ChatID:              100,
			ConversationID:      pgtype.UUID{Bytes: uuid.New(), Valid: true},
			Kind:                "image",
			Provider:            "comet",
			Model:               "gpt-4o-image",
			Prompt:              "prompt",
			Status:              "queued",
			MaxAttempts:         3,
			CreatedAt:           now,
			UpdatedAt:           now,
		},
	}
}

func (f *fakeDBTX) Exec(_ context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(query, "name: UpsertUserSession"):
		f.upsertUserSessionCalls++
		f.session = db.GetUserSessionRow{
			Mode:  args[1].(string),
			Model: args[2].(string),
		}
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(query, "name: SpendText"):
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(query, "name: SpendImage"):
		f.spendImageCalls++
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(query, "name: SpendVideo"):
		f.spendVideoCalls++
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(query, "name: FinishGenerationRequest"):
		f.finishGenerationCalls++
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case strings.Contains(query, "name: FailGenerationRequest"):
		f.failGenerationCalls++
		f.lastFailedGenerationStatus = fmt.Sprint(args[1])
		if msg, ok := args[2].(pgtype.Text); ok && msg.Valid {
			f.lastFailedGenerationMessage = msg.String
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case strings.Contains(query, "name: RefundText"):
		f.refundTextCalls++
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected exec query: %s", firstLine(query))
	}
}

func (f *fakeDBTX) Query(_ context.Context, query string, _ ...interface{}) (pgx.Rows, error) {
	switch {
	case strings.Contains(query, "name: GetLastChatHistory"):
		return &fakeRows{}, nil
	default:
		return nil, fmt.Errorf("unexpected query: %s", firstLine(query))
	}
}

func (f *fakeDBTX) QueryRow(_ context.Context, query string, _ ...interface{}) pgx.Row {
	switch {
	case strings.Contains(query, "name: UpsertUserByTGID"):
		return rowFromUser(f.user)
	case strings.Contains(query, "name: GetUserByTGID"):
		return rowFromUser(f.user)
	case strings.Contains(query, "name: GetUserSession"):
		return &fakeRow{values: []any{f.session.Mode, f.session.Model}}
	case strings.Contains(query, "name: UpsertConversation"):
		return &fakeRow{values: []any{f.conversationID}}
	case strings.Contains(query, "name: InsertGenerationRequest"):
		return rowFromGenerationRequest(f.generationRequest)
	case strings.Contains(query, "name: EnqueueGenerationJob"):
		f.enqueueGenerationJobCalls++
		return rowFromGenerationJob(f.generationJob)
	case strings.Contains(query, "name: InsertChatMessage"):
		f.insertChatMessageCalls++
		return rowFromChatMessage(f.insertChatMessageCalls, f.user.ID, f.conversationID)
	default:
		return &fakeRow{err: fmt.Errorf("unexpected query row: %s", firstLine(query))}
	}
}

type fakeRow struct {
	values []any
	err    error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan mismatch: got %d dests want %d", len(dest), len(r.values))
	}
	for i, d := range dest {
		if d == nil {
			continue
		}
		if err := assignScanValue(d, r.values[i]); err != nil {
			return err
		}
	}
	return nil
}

type fakeRows struct {
	rows   [][]any
	idx    int
	closed bool
}

func (r *fakeRows) Close() {
	r.closed = true
}

func (r *fakeRows) Err() error {
	return nil
}

func (r *fakeRows) CommandTag() pgconn.CommandTag {
	return pgconn.CommandTag{}
}

func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}

func (r *fakeRows) Next() bool {
	if r.idx >= len(r.rows) {
		r.closed = true
		return false
	}
	r.idx++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.rows) {
		return fmt.Errorf("scan called without current row")
	}
	row := r.rows[r.idx-1]
	if len(dest) != len(row) {
		return fmt.Errorf("scan mismatch: got %d dests want %d", len(dest), len(row))
	}
	for i, d := range dest {
		if d == nil {
			continue
		}
		if err := assignScanValue(d, row[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *fakeRows) Values() ([]any, error) {
	if r.idx == 0 || r.idx > len(r.rows) {
		return nil, fmt.Errorf("values called without current row")
	}
	return r.rows[r.idx-1], nil
}

func (r *fakeRows) RawValues() [][]byte {
	return nil
}

func (r *fakeRows) Conn() *pgx.Conn {
	return nil
}

func assignScanValue(dest any, value any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("destination must be a non-nil pointer")
	}
	if value == nil {
		dv.Elem().Set(reflect.Zero(dv.Elem().Type()))
		return nil
	}
	vv := reflect.ValueOf(value)
	if vv.Type().AssignableTo(dv.Elem().Type()) {
		dv.Elem().Set(vv)
		return nil
	}
	if vv.Type().ConvertibleTo(dv.Elem().Type()) {
		dv.Elem().Set(vv.Convert(dv.Elem().Type()))
		return nil
	}
	return fmt.Errorf("cannot assign %T to %T", value, dest)
}

func rowFromUser(user db.User) *fakeRow {
	return &fakeRow{values: []any{
		user.ID,
		user.TgID,
		user.Username,
		user.FirstName,
		user.LastName,
		user.LangCode,
		user.IsBanned,
		user.BannedAt,
		user.BannedReason,
		user.TextBalance,
		user.ImageBalance,
		user.VideoBalance,
		user.CreatedAt,
		user.UpdatedAt,
	}}
}

func rowFromGenerationRequest(gr db.GenerationRequest) *fakeRow {
	return &fakeRow{values: []any{
		gr.ID,
		gr.UserID,
		gr.UpdateID,
		gr.Kind,
		gr.Provider,
		gr.Model,
		gr.OutputTokens,
		gr.CostCreditsText,
		gr.CostCreditsImage,
		gr.CostCreditsVideo,
		gr.Status,
		gr.ErrorMessage,
		gr.LatencyMs,
		gr.CreatedAt,
		gr.FinishedAt,
	}}
}

func rowFromChatMessage(id int, userID int64, convID pgtype.UUID) *fakeRow {
	return &fakeRow{values: []any{
		int64(id),
		userID,
		convID,
		"text",
		"user",
		pgtype.Text{String: "payload", Valid: true},
		pgtype.Text{},
		pgtype.Text{String: "comet", Valid: true},
		pgtype.Text{String: "gpt-5-nano", Valid: true},
		pgtype.Int4{},
		pgtype.Int4{},
		pgtype.Int8{Int64: 77, Valid: true},
		pgtype.Timestamptz{},
	}}
}

func rowFromGenerationJob(job db.GenerationJob) *fakeRow {
	return &fakeRow{values: []any{
		job.ID,
		job.GenerationRequestID,
		job.UserID,
		job.ChatID,
		job.ConversationID,
		job.Kind,
		job.Provider,
		job.Model,
		job.Prompt,
		job.Status,
		job.ResultText,
		job.ErrorMessage,
		job.Attempts,
		job.MaxAttempts,
		job.NextAttemptAt,
		job.CreatedAt,
		job.UpdatedAt,
		job.FinishedAt,
	}}
}

func firstLine(query string) string {
	query = strings.TrimSpace(query)
	if idx := strings.IndexByte(query, '\n'); idx >= 0 {
		return query[:idx]
	}
	return query
}

func newTestRouter(t *testing.T, q *db.Queries, prov provider.ModelProvider) (*Router, *recordingTGClient) {
	t.Helper()

	client := &recordingTGClient{}
	api, err := tgbotapi.NewBotAPIWithClient(
		"TEST_TOKEN",
		"https://fake.telegram.local/bot%s/%s",
		client,
	)
	if err != nil {
		t.Fatalf("new bot api: %v", err)
	}

	router := NewRouter(&Bot{API: api}, nil, nil, nil, q, prov, nil, nil, 0, 0)
	return router, client
}

func TestHelpCommandSendsUsageGuide(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	if err := router.handleCommand(ctx, &tgbotapi.Message{
		Text: "/help",
		Entities: []tgbotapi.MessageEntity{
			{
				Type:   "bot_command",
				Offset: 0,
				Length: len("/help"),
			},
		},
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
	}, dbtx.user.ID); err != nil {
		t.Fatalf("handleCommand: %v", err)
	}

	texts := client.textsForMethod("sendMessage")
	if len(texts) == 0 {
		t.Fatal("expected /help to send a message")
	}
	got := texts[len(texts)-1]

	if !strings.Contains(got, "Как пользоваться ботом:") {
		t.Fatalf("expected usage section, got: %q", got)
	}
	if !strings.Contains(got, "input_reference=https://example.com/ref.png") {
		t.Fatalf("expected input_reference hint, got: %q", got)
	}
	if !strings.Contains(got, "Видео: Sora 2, Kling, Veo 3") {
		t.Fatalf("expected current video model list, got: %q", got)
	}
}

func TestModePreviewDoesNotActivateStateOrSwitchPromptHandling(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	err := router.handleCallback(ctx, &tgbotapi.CallbackQuery{
		ID:   "callback-1",
		Data: "mode:image",
		From: &tgbotapi.User{ID: dbtx.user.TgID, UserName: "tester", FirstName: "Test", LanguageCode: "ru"},
		Message: &tgbotapi.Message{
			MessageID: 55,
			Chat:      &tgbotapi.Chat{ID: 100, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleCallback: %v", err)
	}

	if dbtx.upsertUserSessionCalls != 0 {
		t.Fatalf("mode preview must not persist state, got %d upsert calls", dbtx.upsertUserSessionCalls)
	}
	if dbtx.session.Mode != "text" || dbtx.session.Model != "GPT-5 Nano" {
		t.Fatalf("mode preview changed stored session to mode=%q model=%q", dbtx.session.Mode, dbtx.session.Model)
	}

	foundHint := false
	for _, text := range client.textsForMethod("sendMessage") {
		if text == "Выберите модель, чтобы активировать режим." {
			foundHint = true
			break
		}
	}
	if !foundHint {
		t.Fatal("expected mode preview hint to be sent")
	}

	err = router.handleMessage(ctx, &tgbotapi.Message{
		Text: "Напиши короткий ответ",
		From: &tgbotapi.User{ID: dbtx.user.TgID},
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
	}, dbtx.user.ID)
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	req, ok := prov.lastRequest()
	if ok {
		t.Fatalf("did not expect provider request for deprecated text mode, got %+v", req)
	}

	foundDisabled := false
	for _, text := range client.textsForMethod("sendMessage") {
		if text == "Текстовая генерация сейчас отключена. Выберите фото или видео." {
			foundDisabled = true
			break
		}
	}
	if !foundDisabled {
		t.Fatal("expected disabled-text-mode message")
	}
}

func TestDeprecatedTextStateDoesNotStartSyncGeneration(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	err := router.handleMessage(ctx, &tgbotapi.Message{
		Text: "Сделай ответ",
		From: &tgbotapi.User{ID: dbtx.user.TgID},
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
	}, dbtx.user.ID)
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	if _, ok := prov.lastRequest(); ok {
		t.Fatal("deprecated text state must not call provider")
	}
	if dbtx.finishGenerationCalls != 0 {
		t.Fatalf("deprecated text state must not finish generation, got %d", dbtx.finishGenerationCalls)
	}
	if dbtx.failGenerationCalls != 0 {
		t.Fatalf("deprecated text state must not fail generation flow, got %d", dbtx.failGenerationCalls)
	}
	if dbtx.refundTextCalls != 0 {
		t.Fatalf("deprecated text state must not refund text credits, got %d", dbtx.refundTextCalls)
	}

	foundDisabled := false
	for _, text := range client.textsForMethod("sendMessage") {
		if text == "Текстовая генерация сейчас отключена. Выберите фото или видео." {
			foundDisabled = true
			break
		}
	}
	if !foundDisabled {
		t.Fatal("expected disabled-text-mode message")
	}
}

func TestMediaGenerationIsRejectedByLimiterBeforeChargeAndEnqueue(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	dbtx.session = db.GetUserSessionRow{
		Mode:  "image",
		Model: "GPT 4o Image",
	}
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)
	router.RL = rate.NewByKind(map[string]rate.Config{
		"image": {Max: 0, Refill: 0, Interval: time.Second},
		"text":  {Max: 1, Refill: 1, Interval: time.Second},
	})

	err := router.handleMessage(ctx, &tgbotapi.Message{
		Text: "Нарисуй дом",
		From: &tgbotapi.User{ID: dbtx.user.TgID},
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
	}, dbtx.user.ID)
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	if dbtx.spendImageCalls != 0 {
		t.Fatalf("limiter rejection must not spend image credits, got %d", dbtx.spendImageCalls)
	}
	if dbtx.enqueueGenerationJobCalls != 0 {
		t.Fatalf("limiter rejection must not enqueue media job, got %d", dbtx.enqueueGenerationJobCalls)
	}
	if _, ok := prov.lastRequest(); ok {
		t.Fatal("media limiter rejection must not reach provider")
	}

	foundBusy := false
	for _, text := range client.textsForMethod("sendMessage") {
		if text == "Сервис перегружен, попробуйте позже." {
			foundBusy = true
			break
		}
	}
	if !foundBusy {
		t.Fatal("expected overload message for media limiter rejection")
	}
}
