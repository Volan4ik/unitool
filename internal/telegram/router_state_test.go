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

func (f *recordingTGClient) lastFormForMethod(method string) (url.Values, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := len(f.requests) - 1; i >= 0; i-- {
		if f.requests[i].method == method {
			return f.requests[i].form, true
		}
	}
	return nil, false
}

func (f *recordingTGClient) countRequestsForMethod(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	count := 0
	for _, req := range f.requests {
		if req.method == method {
			count++
		}
	}
	return count
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
	userExists                  bool
	signupBonusCalls            int
	signupBonusGranted          bool
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
	spendImageErr               error
	spendVideoErr               error
	enqueueGenerationJobCalls   int
	lastEnqueuedPrompt          string
	lastFailedGenerationStatus  string
	lastFailedGenerationMessage string
	activePackages              []db.Package
	lastPaidPackage             db.GetLastPaidPackageByUserRow
	lastPaidPackageExists       bool
	startSourceByUser           map[int64]string
	trackStartSourceCalls       int
	lastStartSourceTag          string
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
		userExists: true,
		activePackages: []db.Package{
			{ID: 1, Code: "base_minimum", Title: "Базовый минимум", PriceRub: 690, Currency: "RUB", ImageCredits: 30, VideoCredits: 5, IsActive: true},
			{ID: 2, Code: "golden_middle", Title: "Золотая середина", PriceRub: 1490, Currency: "RUB", ImageCredits: 100, VideoCredits: 10, IsActive: true},
			{ID: 3, Code: "luxury_maximum", Title: "Роскошный максимум", PriceRub: 3190, Currency: "RUB", ImageCredits: 200, VideoCredits: 25, IsActive: true},
			{ID: 4, Code: "boost_10_2", Title: "Буст", PriceRub: 290, Currency: "RUB", ImageCredits: 10, VideoCredits: 2, IsActive: true},
		},
		lastPaidPackage: db.GetLastPaidPackageByUserRow{
			Code:     "golden_middle",
			Title:    "Золотая середина",
			PriceRub: 1490,
			Currency: "RUB",
			PaidAt:   now,
		},
		lastPaidPackageExists: true,
		startSourceByUser:     make(map[int64]string),
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
	case strings.Contains(query, "name: AddSignupBonus"):
		f.signupBonusCalls++
		if f.signupBonusGranted {
			return pgconn.NewCommandTag("INSERT 0 0"), nil
		}
		f.signupBonusGranted = true
		if deltaImage, ok := args[1].(int32); ok {
			f.user.ImageBalance += deltaImage
		}
		if deltaVideo, ok := args[2].(int32); ok {
			f.user.VideoBalance += deltaVideo
		}
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(query, "name: SpendText"):
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(query, "name: SpendImage"):
		f.spendImageCalls++
		if f.spendImageErr != nil {
			return pgconn.CommandTag{}, f.spendImageErr
		}
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(query, "name: SpendVideo"):
		f.spendVideoCalls++
		if f.spendVideoErr != nil {
			return pgconn.CommandTag{}, f.spendVideoErr
		}
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
	case strings.Contains(query, "name: TrackUserStartAttribution"):
		f.trackStartSourceCalls++
		userID, _ := args[0].(int64)
		sourceTag, _ := args[3].(string)
		f.lastStartSourceTag = sourceTag
		if _, exists := f.startSourceByUser[userID]; exists {
			return pgconn.NewCommandTag("INSERT 0 0"), nil
		}
		f.startSourceByUser[userID] = sourceTag
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected exec query: %s", firstLine(query))
	}
}

func (f *fakeDBTX) Query(_ context.Context, query string, _ ...interface{}) (pgx.Rows, error) {
	switch {
	case strings.Contains(query, "name: GetLastChatHistory"):
		return &fakeRows{}, nil
	case strings.Contains(query, "name: ListActivePackages"):
		rows := make([][]any, 0, len(f.activePackages))
		for _, p := range f.activePackages {
			rows = append(rows, []any{
				p.ID,
				p.Code,
				p.Title,
				p.PriceRub,
				p.Currency,
				p.TextCredits,
				p.ImageCredits,
				p.VideoCredits,
				p.IsActive,
				p.CreatedAt,
				p.UpdatedAt,
			})
		}
		return &fakeRows{rows: rows}, nil
	default:
		return nil, fmt.Errorf("unexpected query: %s", firstLine(query))
	}
}

func (f *fakeDBTX) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	switch {
	case strings.Contains(query, "name: UpsertUserByTGID"):
		f.userExists = true
		return rowFromUser(f.user)
	case strings.Contains(query, "name: GetUserByTGID"):
		if !f.userExists {
			return &fakeRow{err: pgx.ErrNoRows}
		}
		return rowFromUser(f.user)
	case strings.Contains(query, "name: GetBalancesByUserID"):
		return &fakeRow{values: []any{f.user.TextBalance, f.user.ImageBalance, f.user.VideoBalance}}
	case strings.Contains(query, "name: GetLastPaidPackageByUser"):
		if !f.lastPaidPackageExists {
			return &fakeRow{err: pgx.ErrNoRows}
		}
		return &fakeRow{values: []any{
			f.lastPaidPackage.Code,
			f.lastPaidPackage.Title,
			f.lastPaidPackage.PriceRub,
			f.lastPaidPackage.Currency,
			f.lastPaidPackage.PaidAt,
		}}
	case strings.Contains(query, "name: GetUserSession"):
		return &fakeRow{values: []any{f.session.Mode, f.session.Model}}
	case strings.Contains(query, "name: UpsertConversation"):
		return &fakeRow{values: []any{f.conversationID}}
	case strings.Contains(query, "name: InsertGenerationRequest"):
		return rowFromGenerationRequest(f.generationRequest)
	case strings.Contains(query, "name: EnqueueGenerationJob"):
		f.enqueueGenerationJobCalls++
		if prompt, ok := args[7].(string); ok {
			f.lastEnqueuedPrompt = prompt
		}
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

func TestStartCommandTracksOrganicSourceByDefault(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	if err := router.handleCommand(ctx, &tgbotapi.Message{
		Text: "/start",
		Entities: []tgbotapi.MessageEntity{
			{
				Type:   "bot_command",
				Offset: 0,
				Length: len("/start"),
			},
		},
		From: &tgbotapi.User{ID: dbtx.user.TgID, UserName: "tester", FirstName: "Test"},
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
	}, dbtx.user.ID); err != nil {
		t.Fatalf("handleCommand: %v", err)
	}

	if got := dbtx.startSourceByUser[dbtx.user.ID]; got != "organic" {
		t.Fatalf("expected organic source_tag, got %q", got)
	}
	texts := client.textsForMethod("sendMessage")
	if len(texts) == 0 {
		t.Fatal("expected /start to send greeting")
	}
	got := texts[len(texts)-1]
	if !strings.Contains(got, "Test, добро пожаловать!") {
		t.Fatalf("expected personalized greeting, got %q", got)
	}
	if !strings.Contains(got, "мы дарим Вам тестовые 5 генераций") {
		t.Fatalf("expected signup offer copy, got %q", got)
	}
	form, ok := client.lastFormForMethod("sendMessage")
	if !ok {
		t.Fatal("expected /start form data")
	}
	if got := form.Get("parse_mode"); got != "HTML" {
		t.Fatalf("expected HTML parse mode, got %q", got)
	}
	replyMarkup := form.Get("reply_markup")
	if !strings.Contains(replyMarkup, "Потратить бесплатные генерации") || !strings.Contains(replyMarkup, "start:mode") {
		t.Fatalf("expected free generations button, got %q", replyMarkup)
	}
	if !strings.Contains(replyMarkup, "Сразу хочу платный тариф") || !strings.Contains(replyMarkup, "buy:menu") {
		t.Fatalf("expected paid tariff button, got %q", replyMarkup)
	}
	if !strings.Contains(replyMarkup, "Расскажите про функционал") || !strings.Contains(replyMarkup, "support:help") {
		t.Fatalf("expected help button, got %q", replyMarkup)
	}
}

func TestStartCommandDoesNotOverwriteSourceTag(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, _ := newTestRouter(t, q, prov)

	first := &tgbotapi.Message{
		Text: "/start ad_tiktok",
		Entities: []tgbotapi.MessageEntity{
			{
				Type:   "bot_command",
				Offset: 0,
				Length: len("/start"),
			},
		},
		From: &tgbotapi.User{ID: dbtx.user.TgID, UserName: "tester"},
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
	}
	second := &tgbotapi.Message{
		Text: "/start ad_google",
		Entities: []tgbotapi.MessageEntity{
			{
				Type:   "bot_command",
				Offset: 0,
				Length: len("/start"),
			},
		},
		From: &tgbotapi.User{ID: dbtx.user.TgID, UserName: "tester"},
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
	}

	if err := router.handleCommand(ctx, first, dbtx.user.ID); err != nil {
		t.Fatalf("first handleCommand: %v", err)
	}
	if err := router.handleCommand(ctx, second, dbtx.user.ID); err != nil {
		t.Fatalf("second handleCommand: %v", err)
	}

	if got := dbtx.startSourceByUser[dbtx.user.ID]; got != "ad_tiktok" {
		t.Fatalf("source_tag should not be overwritten, got %q", got)
	}
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

	if !strings.Contains(got, "Главная задача нашего бота - генерация фото и видео контента") {
		t.Fatalf("expected updated help intro, got: %q", got)
	}
	if !strings.Contains(got, "Nano Banana, MidJourney и Chat GPT") {
		t.Fatalf("expected image models copy, got: %q", got)
	}
	if !strings.Contains(got, "Veo3, Sora и Kling - для видео") {
		t.Fatalf("expected video models copy, got: %q", got)
	}
	if !strings.Contains(got, "<a href=\"https://example.com\">название</a>") {
		t.Fatalf("expected channel link, got: %q", got)
	}
	form, ok := client.lastFormForMethod("sendMessage")
	if !ok {
		t.Fatal("expected /help form data")
	}
	if got := form.Get("parse_mode"); got != "HTML" {
		t.Fatalf("expected HTML parse mode, got %q", got)
	}
	replyMarkup := form.Get("reply_markup")
	if !strings.Contains(replyMarkup, "Написать в поддержку") {
		t.Fatalf("expected support button, got: %q", replyMarkup)
	}
	if !strings.Contains(replyMarkup, "https://t.me/script_train_support") {
		t.Fatalf("expected support url, got: %q", replyMarkup)
	}
}

func TestEnsureUserGrantsSignupBonusOnce(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	dbtx.userExists = false
	dbtx.user.TextBalance = 0
	dbtx.user.ImageBalance = 0
	dbtx.user.VideoBalance = 0
	q := db.New(dbtx)

	msg := &tgbotapi.Message{
		From: &tgbotapi.User{
			ID:           dbtx.user.TgID,
			UserName:     "tester",
			FirstName:    "Test",
			LanguageCode: "ru",
		},
	}

	userID, err := EnsureUser(ctx, q, msg)
	if err != nil {
		t.Fatalf("first EnsureUser: %v", err)
	}
	if userID != dbtx.user.ID {
		t.Fatalf("expected user id %d, got %d", dbtx.user.ID, userID)
	}
	if dbtx.signupBonusCalls != 1 {
		t.Fatalf("expected one signup bonus grant, got %d", dbtx.signupBonusCalls)
	}
	if dbtx.user.ImageBalance != signupBonusImageCredits {
		t.Fatalf("expected %d image credits, got %d", signupBonusImageCredits, dbtx.user.ImageBalance)
	}
	if dbtx.user.VideoBalance != signupBonusVideoCredits {
		t.Fatalf("expected %d video credits, got %d", signupBonusVideoCredits, dbtx.user.VideoBalance)
	}

	_, err = EnsureUser(ctx, q, msg)
	if err != nil {
		t.Fatalf("second EnsureUser: %v", err)
	}
	if dbtx.signupBonusCalls != 1 {
		t.Fatalf("expected signup bonus to remain single-grant, got %d calls", dbtx.signupBonusCalls)
	}
	if dbtx.user.ImageBalance != signupBonusImageCredits {
		t.Fatalf("expected image balance to remain %d, got %d", signupBonusImageCredits, dbtx.user.ImageBalance)
	}
	if dbtx.user.VideoBalance != signupBonusVideoCredits {
		t.Fatalf("expected video balance to remain %d, got %d", signupBonusVideoCredits, dbtx.user.VideoBalance)
	}
}

func TestProfileShowsTariffAndActionButtons(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	err := router.handleMessage(ctx, &tgbotapi.Message{
		Text: "Профиль",
		From: &tgbotapi.User{ID: dbtx.user.TgID, UserName: "tester"},
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
	}, dbtx.user.ID)
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	form, ok := client.lastFormForMethod("sendMessage")
	if !ok {
		t.Fatal("expected profile to send a message")
	}

	text := form.Get("text")
	if !strings.Contains(text, "<b>Профиль</b>") {
		t.Fatalf("expected bold profile title, got: %q", text)
	}
	if !strings.Contains(text, "Мой тарифный план: Золотая середина") {
		t.Fatalf("expected current tariff name, got: %q", text)
	}
	if !strings.Contains(text, "Осталось генераций фото: 10") {
		t.Fatalf("expected image balance line, got: %q", text)
	}
	if !strings.Contains(text, "Осталось генераций видео: 10") {
		t.Fatalf("expected video balance line, got: %q", text)
	}
	if !strings.Contains(text, "Ссылка на наш канал: https://example.com") {
		t.Fatalf("expected channel placeholder link, got: %q", text)
	}
	if got := form.Get("parse_mode"); got != "HTML" {
		t.Fatalf("expected HTML parse mode, got: %q", got)
	}
	replyMarkup := form.Get("reply_markup")
	if !strings.Contains(replyMarkup, "Пополнить баланс") {
		t.Fatalf("expected buy button in profile, got: %q", replyMarkup)
	}
	if !strings.Contains(replyMarkup, "buy:menu") {
		t.Fatalf("expected buy callback in profile, got: %q", replyMarkup)
	}
	if !strings.Contains(replyMarkup, "Написать в поддержку") {
		t.Fatalf("expected support button in profile, got: %q", replyMarkup)
	}
	if !strings.Contains(replyMarkup, "https://t.me/script_train_support") {
		t.Fatalf("expected support url button in profile, got: %q", replyMarkup)
	}
}

func TestShowPackagesDisplaysNewTariffsAndBoost(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	if err := router.showPackages(ctx, 100); err != nil {
		t.Fatalf("showPackages: %v", err)
	}

	form, ok := client.lastFormForMethod("sendMessage")
	if !ok {
		t.Fatal("expected packages menu to send a message")
	}
	text := form.Get("text")
	if !strings.Contains(text, "1. Базовый минимум: 690 руб") {
		t.Fatalf("expected first tariff, got: %q", text)
	}
	if !strings.Contains(text, "2. Золотая середина: 1490 руб") {
		t.Fatalf("expected second tariff, got: %q", text)
	}
	if !strings.Contains(text, "3. Роскошный максимум 3190 руб") {
		t.Fatalf("expected third tariff, got: %q", text)
	}
	if !strings.Contains(text, "Плюс в любой момент можете добавить буст вашей подписки: + 10 фото и 2 видео генераций - 290 рублей") {
		t.Fatalf("expected boost copy, got: %q", text)
	}

	replyMarkup := form.Get("reply_markup")
	expectedButtons := []string{"buy:base_minimum", "buy:golden_middle", "buy:luxury_maximum", "buy:boost_10_2"}
	for _, expected := range expectedButtons {
		if !strings.Contains(replyMarkup, expected) {
			t.Fatalf("expected button %q in reply markup: %q", expected, replyMarkup)
		}
	}
	expectedLabels := []string{"Базовый минимум", "Золотая середина", "Роскошный максимум", "Хочу буст!"}
	for _, expected := range expectedLabels {
		if !strings.Contains(replyMarkup, expected) {
			t.Fatalf("expected button label %q in reply markup: %q", expected, replyMarkup)
		}
	}
}

func TestSupportCallbackSendsHelpMessage(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	err := router.handleCallback(ctx, &tgbotapi.CallbackQuery{
		ID:   "callback-help",
		Data: "support:help",
		From: &tgbotapi.User{ID: dbtx.user.TgID, UserName: "tester"},
		Message: &tgbotapi.Message{
			MessageID: 7,
			Chat:      &tgbotapi.Chat{ID: 100, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleCallback: %v", err)
	}

	texts := client.textsForMethod("sendMessage")
	if len(texts) == 0 {
		t.Fatal("expected support callback to send help message")
	}
	got := texts[len(texts)-1]
	if !strings.Contains(got, "Главная задача нашего бота - генерация фото и видео контента") {
		t.Fatalf("expected updated help text in callback help, got: %q", got)
	}
	form, ok := client.lastFormForMethod("sendMessage")
	if !ok {
		t.Fatal("expected support callback form data")
	}
	if got := form.Get("parse_mode"); got != "HTML" {
		t.Fatalf("expected HTML parse mode, got %q", got)
	}
	replyMarkup := form.Get("reply_markup")
	if !strings.Contains(replyMarkup, "Написать в поддержку") {
		t.Fatalf("expected support button in callback help, got: %q", replyMarkup)
	}
	if !strings.Contains(replyMarkup, "https://t.me/script_train_support") {
		t.Fatalf("expected support url in callback help, got: %q", replyMarkup)
	}
}

func TestStartModeCallbackShowsGenerationTypeChoice(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	err := router.handleCallback(ctx, &tgbotapi.CallbackQuery{
		ID:   "callback-start-mode",
		Data: "start:mode",
		From: &tgbotapi.User{ID: dbtx.user.TgID, UserName: "tester"},
		Message: &tgbotapi.Message{
			MessageID: 17,
			Chat:      &tgbotapi.Chat{ID: 100, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleCallback: %v", err)
	}

	form, ok := client.lastFormForMethod("sendMessage")
	if !ok {
		t.Fatal("expected start mode callback to send type chooser")
	}
	if got := form.Get("text"); got != "Выберите тип генерации:" {
		t.Fatalf("unexpected chooser text: %q", got)
	}
	replyMarkup := form.Get("reply_markup")
	if !strings.Contains(replyMarkup, "mode:image") || !strings.Contains(replyMarkup, "Фото") {
		t.Fatalf("expected image mode button, got: %q", replyMarkup)
	}
	if !strings.Contains(replyMarkup, "mode:video") || !strings.Contains(replyMarkup, "Видео") {
		t.Fatalf("expected video mode button, got: %q", replyMarkup)
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

func TestModeCallbackWhenModeAlreadySelectedShowsNotice(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	dbtx.session.Mode = "video"
	dbtx.session.Model = "Kling"
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	err := router.handleCallback(ctx, &tgbotapi.CallbackQuery{
		ID:   "callback-same-mode",
		Data: "mode:video",
		From: &tgbotapi.User{ID: dbtx.user.TgID, UserName: "tester", FirstName: "Test", LanguageCode: "ru"},
		Message: &tgbotapi.Message{
			MessageID: 77,
			Chat:      &tgbotapi.Chat{ID: 100, Type: "private"},
			ReplyMarkup: func() *tgbotapi.InlineKeyboardMarkup {
				kb := ModelsInlineKeyboard("video", "Kling")
				return &kb
			}(),
		},
	})
	if err != nil {
		t.Fatalf("handleCallback: %v", err)
	}

	form, ok := client.lastFormForMethod("answerCallbackQuery")
	if !ok {
		t.Fatal("expected repeated mode tap to answer callback query")
	}
	if got := form.Get("text"); got != "Этот тип уже выбран. При желании можно просто выбрать другую модель." {
		t.Fatalf("unexpected callback notice: %q", got)
	}
	if got := client.countRequestsForMethod("editMessageReplyMarkup"); got != 0 {
		t.Fatalf("expected no reply markup edit, got %d", got)
	}
	if got := client.countRequestsForMethod("sendMessage"); got != 0 {
		t.Fatalf("expected no extra chat message, got %d", got)
	}
}

func TestModeCallbackWhenSameModeFromChooserShowsModels(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	dbtx.session.Mode = "image"
	dbtx.session.Model = "GPT 4o Image"
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	err := router.handleCallback(ctx, &tgbotapi.CallbackQuery{
		ID:   "callback-same-mode-chooser",
		Data: "mode:image",
		From: &tgbotapi.User{ID: dbtx.user.TgID, UserName: "tester", FirstName: "Test", LanguageCode: "ru"},
		Message: &tgbotapi.Message{
			MessageID: 78,
			Chat:      &tgbotapi.Chat{ID: 100, Type: "private"},
			ReplyMarkup: func() *tgbotapi.InlineKeyboardMarkup {
				kb := ModeInlineKeyboard()
				return &kb
			}(),
		},
	})
	if err != nil {
		t.Fatalf("handleCallback: %v", err)
	}

	form, ok := client.lastFormForMethod("answerCallbackQuery")
	if !ok {
		t.Fatal("expected same-mode chooser tap to answer callback query")
	}
	if got := form.Get("text"); got != "Этот тип уже выбран. Сейчас покажу доступные модели." {
		t.Fatalf("unexpected callback notice: %q", got)
	}
	if got := client.countRequestsForMethod("editMessageReplyMarkup"); got != 1 {
		t.Fatalf("expected one reply markup edit, got %d", got)
	}
	if got := client.countRequestsForMethod("sendMessage"); got != 0 {
		t.Fatalf("expected no extra message when expanding same mode chooser, got %d", got)
	}

	editForm, ok := client.lastFormForMethod("editMessageReplyMarkup")
	if !ok {
		t.Fatal("expected edited reply markup form")
	}
	replyMarkup := editForm.Get("reply_markup")
	if !strings.Contains(replyMarkup, "model:image:GPT 4o Image") {
		t.Fatalf("expected expanded image models, got %q", replyMarkup)
	}
}

func TestInsufficientImageCreditsMessageShowsBuyAndProfileButtons(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	dbtx.session.Mode = "image"
	dbtx.session.Model = "GPT 4o Image"
	dbtx.spendImageErr = fmt.Errorf("not enough image credits")
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	err := router.handleMessage(ctx, &tgbotapi.Message{
		Text: "Нарисуй закат",
		From: &tgbotapi.User{ID: dbtx.user.TgID, UserName: "tester"},
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
	}, dbtx.user.ID)
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	form, ok := client.lastFormForMethod("sendMessage")
	if !ok {
		t.Fatal("expected insufficient credits message")
	}
	if got := form.Get("text"); got != "Недостаточно генераций для картинки" {
		t.Fatalf("unexpected insufficient credits text: %q", got)
	}
	replyMarkup := form.Get("reply_markup")
	if !strings.Contains(replyMarkup, "Пополнить баланс") || !strings.Contains(replyMarkup, "buy:menu") {
		t.Fatalf("expected buy button in insufficient credits message: %q", replyMarkup)
	}
	if !strings.Contains(replyMarkup, "Мой профиль") || !strings.Contains(replyMarkup, "profile:show") {
		t.Fatalf("expected profile button in insufficient credits message: %q", replyMarkup)
	}
}

func TestProfileCallbackShowsProfile(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	err := router.handleCallback(ctx, &tgbotapi.CallbackQuery{
		ID:   "callback-profile",
		Data: "profile:show",
		From: &tgbotapi.User{ID: dbtx.user.TgID, UserName: "tester"},
		Message: &tgbotapi.Message{
			MessageID: 9,
			Chat:      &tgbotapi.Chat{ID: 100, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleCallback: %v", err)
	}

	texts := client.textsForMethod("sendMessage")
	if len(texts) == 0 {
		t.Fatal("expected profile callback to send profile")
	}
	got := texts[len(texts)-1]
	if !strings.Contains(got, "<b>Профиль</b>") {
		t.Fatalf("expected profile text, got: %q", got)
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

func TestMediaGenerationSendsAnalysisAck(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	dbtx.session = db.GetUserSessionRow{
		Mode:  "image",
		Model: "GPT 4o Image",
	}
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	err := router.handleMessage(ctx, &tgbotapi.Message{
		Text: "Нарисуй дом",
		From: &tgbotapi.User{ID: dbtx.user.TgID},
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
	}, dbtx.user.ID)
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	foundAck := false
	for _, text := range client.textsForMethod("sendMessage") {
		if text == "Анализирую ваш запрос..." {
			foundAck = true
			break
		}
	}
	if !foundAck {
		t.Fatal("expected analysis ack for accepted media generation")
	}
}

func TestPhotoReferenceWithoutCaptionWaitsForPrompt(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	dbtx.session = db.GetUserSessionRow{
		Mode:  "video",
		Model: "Sora 2",
	}
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, client := newTestRouter(t, q, prov)

	prevEncode := encodeReferenceDataURI
	encodeReferenceDataURI = func(context.Context, *tgbotapi.BotAPI, []string) ([]string, error) {
		return []string{"data:image/jpeg;base64,ZmFrZQ=="}, nil
	}
	defer func() {
		encodeReferenceDataURI = prevEncode
	}()

	err := router.handleMessage(ctx, &tgbotapi.Message{
		Photo: []tgbotapi.PhotoSize{{FileID: "photo-ref-1", Width: 256, Height: 256}},
		From:  &tgbotapi.User{ID: dbtx.user.TgID},
		Chat:  &tgbotapi.Chat{ID: 100, Type: "private"},
	}, dbtx.user.ID)
	if err != nil {
		t.Fatalf("photo handleMessage: %v", err)
	}

	texts := client.textsForMethod("sendMessage")
	if len(texts) == 0 || texts[len(texts)-1] != "Референс получен. Теперь отправьте подпись одним сообщением." {
		t.Fatalf("expected ref-waiting message, got %v", texts)
	}
	if dbtx.enqueueGenerationJobCalls != 0 {
		t.Fatalf("expected no job enqueue before prompt, got %d", dbtx.enqueueGenerationJobCalls)
	}

	err = router.handleMessage(ctx, &tgbotapi.Message{
		Text: "cinematic shot",
		From: &tgbotapi.User{ID: dbtx.user.TgID},
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
	}, dbtx.user.ID)
	if err != nil {
		t.Fatalf("text handleMessage: %v", err)
	}

	if dbtx.enqueueGenerationJobCalls != 1 {
		t.Fatalf("expected one enqueued job, got %d", dbtx.enqueueGenerationJobCalls)
	}
	if !strings.Contains(dbtx.lastEnqueuedPrompt, "input_reference=data:image/jpeg;base64,ZmFrZQ==") {
		t.Fatalf("expected prompt to include encoded reference, got %q", dbtx.lastEnqueuedPrompt)
	}
	if !strings.Contains(dbtx.lastEnqueuedPrompt, "cinematic shot") {
		t.Fatalf("expected prompt text to be preserved, got %q", dbtx.lastEnqueuedPrompt)
	}
}

func TestMediaGroupPhotoWithCaptionEnqueuesSingleGeneration(t *testing.T) {
	ctx := context.Background()
	dbtx := newFakeDBTX()
	dbtx.session = db.GetUserSessionRow{
		Mode:  "video",
		Model: "Sora 2",
	}
	q := db.New(dbtx)
	prov := &fakeProvider{}
	router, _ := newTestRouter(t, q, prov)

	prevEncode := encodeReferenceDataURI
	prevDelay := mediaGroupSettleDelay
	encodeReferenceDataURI = func(context.Context, *tgbotapi.BotAPI, []string) ([]string, error) {
		return []string{"data:image/jpeg;base64,YWJj", "data:image/jpeg;base64,ZGVm"}, nil
	}
	mediaGroupSettleDelay = 10 * time.Millisecond
	defer func() {
		encodeReferenceDataURI = prevEncode
		mediaGroupSettleDelay = prevDelay
	}()

	first := &tgbotapi.Message{
		MediaGroupID: "grp-1",
		Caption:      "slow zoom",
		Photo:        []tgbotapi.PhotoSize{{FileID: "photo-1", Width: 320, Height: 320}},
		From:         &tgbotapi.User{ID: dbtx.user.TgID},
		Chat:         &tgbotapi.Chat{ID: 100, Type: "private"},
	}
	second := &tgbotapi.Message{
		MediaGroupID: "grp-1",
		Photo:        []tgbotapi.PhotoSize{{FileID: "photo-2", Width: 320, Height: 320}},
		From:         &tgbotapi.User{ID: dbtx.user.TgID},
		Chat:         &tgbotapi.Chat{ID: 100, Type: "private"},
	}

	if err := router.handleMessage(ctx, first, dbtx.user.ID); err != nil {
		t.Fatalf("first media group message: %v", err)
	}
	if err := router.handleMessage(ctx, second, dbtx.user.ID); err != nil {
		t.Fatalf("second media group message: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	if dbtx.enqueueGenerationJobCalls != 1 {
		t.Fatalf("expected one enqueued media-group job, got %d", dbtx.enqueueGenerationJobCalls)
	}
	if !strings.Contains(dbtx.lastEnqueuedPrompt, "input_reference=data:image/jpeg;base64,YWJj") {
		t.Fatalf("expected encoded media-group reference, got %q", dbtx.lastEnqueuedPrompt)
	}
	if !strings.Contains(dbtx.lastEnqueuedPrompt, "slow zoom") {
		t.Fatalf("expected media-group caption in prompt, got %q", dbtx.lastEnqueuedPrompt)
	}
}
