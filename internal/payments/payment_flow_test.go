package payments

import (
	"context"
	"errors"
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
)

type paymentTGRequest struct {
	method string
	form   url.Values
}

type paymentTGClient struct {
	mu        sync.Mutex
	requests  []paymentTGRequest
	messageID int
}

func (f *paymentTGClient) Do(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	method := path.Base(req.URL.Path)
	form, _ := url.ParseQuery(string(body))

	f.mu.Lock()
	f.requests = append(f.requests, paymentTGRequest{method: method, form: form})
	if method == "sendMessage" || method == "sendInvoice" {
		f.messageID++
	}
	messageID := f.messageID
	f.mu.Unlock()

	var resp string
	switch method {
	case "getMe":
		resp = `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"test","username":"test_bot"}}`
	case "sendMessage", "sendInvoice":
		chatID, _ := strconv.ParseInt(form.Get("chat_id"), 10, 64)
		resp = fmt.Sprintf(`{"ok":true,"result":{"message_id":%d,"date":0,"chat":{"id":%d,"type":"private"}}}`, messageID, chatID)
	default:
		resp = `{"ok":true,"result":true}`
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(resp)),
	}, nil
}

func (f *paymentTGClient) countMethod(method string) int {
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

func (f *paymentTGClient) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var texts []string
	for _, req := range f.requests {
		if req.method == "sendMessage" {
			texts = append(texts, req.form.Get("text"))
		}
	}
	return texts
}

type paymentStore struct {
	mu sync.Mutex

	user  db.User
	order db.GetOrderByIDRow
	pkg   db.Package

	createOrderCalls      int
	markOrderPaidCalls    int
	markManualReviewCalls int
	addCreditsCalls       int
	beginCalls            int
	commitCalls           int
	rollbackCalls         int
}

type paymentDB struct {
	store *paymentStore
}

type paymentTx struct {
	store *paymentStore
}

func newPaymentStore() *paymentStore {
	orderID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	return &paymentStore{
		user: db.User{
			ID:           10,
			TgID:         42,
			Username:     pgtype.Text{String: "tester", Valid: true},
			FirstName:    pgtype.Text{String: "Test", Valid: true},
			TextBalance:  0,
			ImageBalance: 0,
			VideoBalance: 0,
		},
		order: db.GetOrderByIDRow{
			ID:        orderID,
			UserID:    10,
			PackageID: 20,
			AmountRub: 199,
			Currency:  "RUB",
			Status:    "created",
		},
		pkg: db.Package{
			ID:           20,
			Code:         "starter",
			Title:        "Starter",
			PriceRub:     199,
			Currency:     "RUB",
			TextCredits:  100,
			ImageCredits: 5,
			VideoCredits: 1,
			IsActive:     true,
		},
	}
}

func (dbx *paymentDB) Exec(_ context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	dbx.store.mu.Lock()
	defer dbx.store.mu.Unlock()

	switch {
	case strings.Contains(query, "name: MarkOrderManualReview"):
		dbx.store.markManualReviewCalls++
		if dbx.store.order.Status == "created" || dbx.store.order.Status == "precheckout_ok" {
			dbx.store.order.Status = "manual_review"
			dbx.store.order.PaidAt = pgtype.Timestamptz{Valid: true}
			dbx.store.order.TgPaymentChargeID = args[1].(pgtype.Text)
			dbx.store.order.ProviderPaymentChargeID = args[2].(pgtype.Text)
			return pgconn.NewCommandTag("UPDATE 1"), nil
		}
		return pgconn.NewCommandTag("UPDATE 0"), nil
	case strings.Contains(query, "name: MarkOrderFailed"):
		if dbx.store.order.Status == "created" || dbx.store.order.Status == "precheckout_ok" {
			dbx.store.order.Status = "failed"
			return pgconn.NewCommandTag("UPDATE 1"), nil
		}
		return pgconn.NewCommandTag("UPDATE 0"), nil
	case strings.Contains(query, "name: MarkOrderPrecheckout"):
		if dbx.store.order.Status == "created" {
			dbx.store.order.Status = "precheckout_ok"
			return pgconn.NewCommandTag("UPDATE 1"), nil
		}
		return pgconn.NewCommandTag("UPDATE 0"), nil
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected exec query: %s", paymentFirstLine(query))
	}
}

func (dbx *paymentDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected query")
}

func (dbx *paymentDB) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	dbx.store.mu.Lock()
	defer dbx.store.mu.Unlock()

	switch {
	case strings.Contains(query, "name: GetUserByID"):
		return paymentRowFromUser(dbx.store.user)
	case strings.Contains(query, "name: GetOrderByID"):
		return paymentRowFromOrder(dbx.store.order)
	case strings.Contains(query, "name: CreateOrder"):
		dbx.store.createOrderCalls++
		dbx.store.order.ID = args[0].(pgtype.UUID)
		dbx.store.order.UserID = args[1].(int64)
		dbx.store.order.PackageID = args[2].(int64)
		dbx.store.order.AmountRub = args[3].(int32)
		dbx.store.order.Currency = "RUB"
		dbx.store.order.Status = "created"
		dbx.store.order.BuyerEmail = args[4].(pgtype.Text)
		dbx.store.order.ProviderData = args[5].([]byte)
		return paymentRowFromCreateOrder(db.CreateOrderRow{
			ID:                      dbx.store.order.ID,
			UserID:                  dbx.store.order.UserID,
			PackageID:               dbx.store.order.PackageID,
			AmountRub:               dbx.store.order.AmountRub,
			Currency:                dbx.store.order.Currency,
			Status:                  dbx.store.order.Status,
			TgPaymentChargeID:       dbx.store.order.TgPaymentChargeID,
			ProviderPaymentChargeID: dbx.store.order.ProviderPaymentChargeID,
			BuyerEmail:              dbx.store.order.BuyerEmail,
			ProviderData:            dbx.store.order.ProviderData,
			CreatedAt:               dbx.store.order.CreatedAt,
			PaidAt:                  dbx.store.order.PaidAt,
		})
	default:
		return &paymentFakeRow{err: fmt.Errorf("unexpected query row: %s", paymentFirstLine(query))}
	}
}

func (tx *paymentTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("not implemented")
}
func (tx *paymentTx) Commit(context.Context) error {
	tx.store.mu.Lock()
	defer tx.store.mu.Unlock()
	tx.store.commitCalls++
	return nil
}
func (tx *paymentTx) Rollback(context.Context) error {
	tx.store.mu.Lock()
	defer tx.store.mu.Unlock()
	tx.store.rollbackCalls++
	return nil
}
func (tx *paymentTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("not implemented")
}
func (tx *paymentTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (tx *paymentTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (tx *paymentTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("not implemented")
}
func (tx *paymentTx) Exec(_ context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	tx.store.mu.Lock()
	defer tx.store.mu.Unlock()

	switch {
	case strings.Contains(query, "name: AddPurchaseCredits"):
		tx.store.addCreditsCalls++
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected tx exec query: %s", paymentFirstLine(query))
	}
}
func (tx *paymentTx) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected tx query")
}
func (tx *paymentTx) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	tx.store.mu.Lock()
	defer tx.store.mu.Unlock()

	switch {
	case strings.Contains(query, "name: MarkOrderPaid"):
		tx.store.markOrderPaidCalls++
		if tx.store.order.Status != "created" && tx.store.order.Status != "precheckout_ok" {
			return &paymentFakeRow{err: pgx.ErrNoRows}
		}
		tx.store.order.Status = "paid"
		tx.store.order.PaidAt = pgtype.Timestamptz{Valid: true}
		tx.store.order.TgPaymentChargeID = args[1].(pgtype.Text)
		tx.store.order.ProviderPaymentChargeID = args[2].(pgtype.Text)
		return &paymentFakeRow{values: []any{tx.store.order.ID}}
	case strings.Contains(query, "name: GetPackageByID"):
		return paymentRowFromPackage(tx.store.pkg)
	default:
		return &paymentFakeRow{err: fmt.Errorf("unexpected tx query row: %s", paymentFirstLine(query))}
	}
}
func (tx *paymentTx) Conn() *pgx.Conn { return nil }

type paymentFakeRow struct {
	values []any
	err    error
}

func (r *paymentFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan mismatch: got %d want %d", len(dest), len(r.values))
	}
	for i, d := range dest {
		if err := paymentAssignScanValue(d, r.values[i]); err != nil {
			return err
		}
	}
	return nil
}

func paymentAssignScanValue(dest any, value any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("destination must be pointer")
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

func paymentRowFromUser(user db.User) *paymentFakeRow {
	return &paymentFakeRow{values: []any{
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

func paymentRowFromOrder(order db.GetOrderByIDRow) *paymentFakeRow {
	return &paymentFakeRow{values: []any{
		order.ID,
		order.UserID,
		order.PackageID,
		order.AmountRub,
		order.Currency,
		order.Status,
		order.TgPaymentChargeID,
		order.ProviderPaymentChargeID,
		order.BuyerEmail,
		order.ProviderData,
		order.CreatedAt,
		order.PaidAt,
	}}
}

func paymentRowFromCreateOrder(order db.CreateOrderRow) *paymentFakeRow {
	return &paymentFakeRow{values: []any{
		order.ID,
		order.UserID,
		order.PackageID,
		order.AmountRub,
		order.Currency,
		order.Status,
		order.TgPaymentChargeID,
		order.ProviderPaymentChargeID,
		order.BuyerEmail,
		order.ProviderData,
		order.CreatedAt,
		order.PaidAt,
	}}
}

func paymentRowFromPackage(pkg db.Package) *paymentFakeRow {
	return &paymentFakeRow{values: []any{
		pkg.ID,
		pkg.Code,
		pkg.Title,
		pkg.PriceRub,
		pkg.Currency,
		pkg.TextCredits,
		pkg.ImageCredits,
		pkg.VideoCredits,
		pkg.IsActive,
		pkg.CreatedAt,
		pkg.UpdatedAt,
	}}
}

func paymentFirstLine(query string) string {
	query = strings.TrimSpace(query)
	if idx := strings.IndexByte(query, '\n'); idx >= 0 {
		return query[:idx]
	}
	return query
}

func newTestPaymentService(t *testing.T, store *paymentStore) (*Service, *paymentTGClient) {
	t.Helper()

	client := &paymentTGClient{}
	api, err := tgbotapi.NewBotAPIWithClient(
		"TEST_TOKEN",
		"https://fake.telegram.local/bot%s/%s",
		client,
	)
	if err != nil {
		t.Fatalf("new bot api: %v", err)
	}

	svc := &Service{
		Bot:       api,
		Q:         db.New(&paymentDB{store: store}),
		DBTimeout: time.Second,
		beginTx: func(context.Context) (pgx.Tx, error) {
			store.mu.Lock()
			store.beginCalls++
			store.mu.Unlock()
			return &paymentTx{store: store}, nil
		},
	}
	return svc, client
}

func TestSendInvoiceForPackageRejectsBannedUser(t *testing.T) {
	store := newPaymentStore()
	store.user.IsBanned = true
	svc, client := newTestPaymentService(t, store)

	_, err := svc.SendInvoiceForPackage(context.Background(), 100, store.user.ID, Package{
		ID:       store.pkg.ID,
		Title:    store.pkg.Title,
		PriceRub: int(store.pkg.PriceRub),
	}, "")
	if !errors.Is(err, ErrPaymentUnavailableForBanned) {
		t.Fatalf("expected banned payment error, got %v", err)
	}
	if store.createOrderCalls != 0 {
		t.Fatalf("expected no order to be created, got %d", store.createOrderCalls)
	}
	if got := client.countMethod("sendInvoice"); got != 0 {
		t.Fatalf("expected no invoice to be sent, got %d", got)
	}
}

func TestHandleSuccessfulPaymentBannedUserMovesOrderToManualReview(t *testing.T) {
	store := newPaymentStore()
	svc, client := newTestPaymentService(t, store)

	if _, err := svc.SendInvoiceForPackage(context.Background(), 100, store.user.ID, Package{
		ID:       store.pkg.ID,
		Title:    store.pkg.Title,
		PriceRub: int(store.pkg.PriceRub),
	}, ""); err != nil {
		t.Fatalf("SendInvoiceForPackage: %v", err)
	}

	store.mu.Lock()
	store.user.IsBanned = true
	orderID := uuid.UUID(store.order.ID.Bytes)
	store.mu.Unlock()

	err := svc.HandleSuccessfulPayment(context.Background(), &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
		SuccessfulPayment: &tgbotapi.SuccessfulPayment{
			InvoicePayload:          orderID.String(),
			Currency:                "RUB",
			TotalAmount:             int(store.pkg.PriceRub) * 100,
			TelegramPaymentChargeID: "tg-charge-1",
			ProviderPaymentChargeID: "provider-charge-1",
		},
	})
	if err != nil {
		t.Fatalf("HandleSuccessfulPayment: %v", err)
	}

	if store.order.Status != "manual_review" {
		t.Fatalf("expected manual_review status, got %q", store.order.Status)
	}
	if store.markManualReviewCalls != 1 {
		t.Fatalf("expected one manual review transition, got %d", store.markManualReviewCalls)
	}
	if store.addCreditsCalls != 0 {
		t.Fatalf("expected no credits to be granted, got %d", store.addCreditsCalls)
	}
	if store.beginCalls != 0 {
		t.Fatalf("expected no payment tx for banned user, got %d", store.beginCalls)
	}
	found := false
	for _, text := range client.sentTexts() {
		if text == bannedPaymentMessage {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected banned payment message %q", bannedPaymentMessage)
	}
}

func TestHandleSuccessfulPaymentDuplicateDoesNotDoubleCredit(t *testing.T) {
	store := newPaymentStore()
	svc, client := newTestPaymentService(t, store)

	orderID := uuid.UUID(store.order.ID.Bytes)
	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 100, Type: "private"},
		SuccessfulPayment: &tgbotapi.SuccessfulPayment{
			InvoicePayload:          orderID.String(),
			Currency:                "RUB",
			TotalAmount:             int(store.order.AmountRub) * 100,
			TelegramPaymentChargeID: "tg-charge-1",
			ProviderPaymentChargeID: "provider-charge-1",
		},
	}

	if err := svc.HandleSuccessfulPayment(context.Background(), msg); err != nil {
		t.Fatalf("first HandleSuccessfulPayment: %v", err)
	}
	if err := svc.HandleSuccessfulPayment(context.Background(), msg); err != nil {
		t.Fatalf("second HandleSuccessfulPayment: %v", err)
	}

	if store.addCreditsCalls != 1 {
		t.Fatalf("expected credits to be granted once, got %d", store.addCreditsCalls)
	}
	if store.order.Status != "paid" {
		t.Fatalf("expected order status paid, got %q", store.order.Status)
	}
	if store.rollbackCalls != 1 {
		t.Fatalf("expected one rollback for duplicate tx, got %d", store.rollbackCalls)
	}
	found := false
	for _, text := range client.sentTexts() {
		if strings.Contains(text, "Оплата уже была обработана") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected duplicate payment confirmation message")
	}
}
