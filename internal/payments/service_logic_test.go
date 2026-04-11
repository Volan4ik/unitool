package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"reflect"
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

type scriptedRow struct {
	values []any
	err    error
}

func (r scriptedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan values mismatch: got %d want %d", len(dest), len(r.values))
	}
	for i := range dest {
		if err := assignScanDest(dest[i], r.values[i]); err != nil {
			return fmt.Errorf("scan col=%d: %w", i, err)
		}
	}
	return nil
}

type scriptedExec struct {
	tag pgconn.CommandTag
	err error
}

type scriptedDB struct {
	mu sync.Mutex

	queryRows map[string][]scriptedRow
	execs     map[string][]scriptedExec

	queryCalls map[string]int
	execCalls  map[string]int
}

func newScriptedDB() *scriptedDB {
	return &scriptedDB{
		queryRows:  make(map[string][]scriptedRow),
		execs:      make(map[string][]scriptedExec),
		queryCalls: make(map[string]int),
		execCalls:  make(map[string]int),
	}
}

func (s *scriptedDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	name := sqlQueryName(sql)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queryCalls[name]++
	queue := s.queryRows[name]
	if len(queue) == 0 {
		return scriptedRow{err: fmt.Errorf("no scripted row for query %q", name)}
	}
	spec := queue[0]
	s.queryRows[name] = queue[1:]
	return spec
}

func (s *scriptedDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	name := sqlQueryName(sql)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.execCalls[name]++
	queue := s.execs[name]
	if len(queue) == 0 {
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	spec := queue[0]
	s.execs[name] = queue[1:]
	return spec.tag, spec.err
}

func (s *scriptedDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("query not expected in this test")
}

func sqlQueryName(sql string) string {
	line := strings.TrimSpace(strings.SplitN(sql, "\n", 2)[0])
	const prefix = "-- name: "
	if !strings.HasPrefix(line, prefix) {
		return line
	}
	rest := strings.TrimPrefix(line, prefix)
	idx := strings.Index(rest, " :")
	if idx < 0 {
		return rest
	}
	return rest[:idx]
}

func assignScanDest(dest any, value any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("destination is not a pointer")
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
	return fmt.Errorf("cannot assign %s to %s", vv.Type(), dv.Elem().Type())
}

func userScanValues(u db.User) []any {
	return []any{
		u.ID, u.TgID, u.Username, u.FirstName, u.LastName, u.LangCode,
		u.IsBanned, u.BannedAt, u.BannedReason,
		u.TextBalance, u.ImageBalance, u.VideoBalance,
		u.CreatedAt, u.UpdatedAt,
	}
}

func orderScanValues(o db.GetOrderByIDRow) []any {
	return []any{
		o.ID, o.UserID, o.PackageID, o.AmountRub, o.Currency, o.Status,
		o.TgPaymentChargeID, o.ProviderPaymentChargeID,
		o.BuyerEmail, o.ProviderData, o.CreatedAt, o.PaidAt,
	}
}

func packageScanValues(p db.Package) []any {
	return []any{
		p.ID, p.Code, p.Title, p.PriceRub, p.Currency, p.TextCredits,
		p.ImageCredits, p.VideoCredits, p.IsActive, p.CreatedAt, p.UpdatedAt,
	}
}

func createOrderScanValues(r db.CreateOrderRow) []any {
	return []any{
		r.ID, r.UserID, r.PackageID, r.AmountRub, r.Currency, r.Status,
		r.TgPaymentChargeID, r.ProviderPaymentChargeID, r.BuyerEmail,
		r.ProviderData, r.CreatedAt, r.PaidAt,
	}
}

type botStub struct {
	mu    sync.Mutex
	calls map[string]int
	fail  map[string]bool
}

func newBotStub(t *testing.T) (*tgbotapi.BotAPI, *botStub) {
	t.Helper()

	stub := &botStub{
		calls: make(map[string]int),
		fail:  make(map[string]bool),
	}
	bot, err := tgbotapi.NewBotAPIWithClient("TEST", "https://api.telegram.test/bot%s/%s", stub)
	if err != nil {
		t.Fatalf("new test bot api: %v", err)
	}
	return bot, stub
}

func (b *botStub) Do(r *http.Request) (*http.Response, error) {
	method := path.Base(r.URL.Path)
	b.mu.Lock()
	b.calls[method]++
	fail := b.fail[method]
	b.mu.Unlock()

	var body string
	if method == "getMe" {
		body = `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"test","username":"bot"}}`
	} else if fail {
		body = `{"ok":false,"error_code":400,"description":"forced failure"}`
	} else if method == "answerPreCheckoutQuery" {
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

func (b *botStub) setFail(method string, fail bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fail[method] = fail
}

func (b *botStub) callCount(method string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls[method]
}

func TestWithDBTimeout(t *testing.T) {
	s := &Service{DBTimeout: 0}
	ctx, cancel := s.withDBTimeout(nil)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}
	if until := time.Until(deadline); until <= 0 || until > 6*time.Second {
		t.Fatalf("unexpected timeout: %v", until)
	}

	s.DBTimeout = 50 * time.Millisecond
	ctx2, cancel2 := s.withDBTimeout(context.Background())
	defer cancel2()
	d2, ok := ctx2.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}
	if until := time.Until(d2); until <= 0 || until > 200*time.Millisecond {
		t.Fatalf("unexpected timeout: %v", until)
	}
}

func TestEnsurePaymentAllowed(t *testing.T) {
	baseUser := db.User{ID: 10, TgID: 11}

	t.Run("banned user", func(t *testing.T) {
		sdb := newScriptedDB()
		sdb.queryRows["GetUserByID"] = []scriptedRow{{values: userScanValues(db.User{ID: 10, TgID: 11, IsBanned: true})}}
		s := &Service{Q: db.New(sdb)}
		err := s.ensurePaymentAllowed(context.Background(), 10, Package{Code: "base_minimum"})
		if !errors.Is(err, ErrPaymentUnavailableForBanned) {
			t.Fatalf("got err=%v", err)
		}
	})

	t.Run("boost without base purchase", func(t *testing.T) {
		sdb := newScriptedDB()
		sdb.queryRows["GetUserByID"] = []scriptedRow{{values: userScanValues(baseUser)}}
		sdb.queryRows["GetLastPaidPackageByUser"] = []scriptedRow{{err: pgx.ErrNoRows}}
		s := &Service{Q: db.New(sdb)}
		err := s.ensurePaymentAllowed(context.Background(), 10, Package{Code: "boost_10_2"})
		if !errors.Is(err, ErrBoostRequiresBasePackage) {
			t.Fatalf("got err=%v", err)
		}
	})

	t.Run("boost after boost", func(t *testing.T) {
		sdb := newScriptedDB()
		sdb.queryRows["GetUserByID"] = []scriptedRow{{values: userScanValues(baseUser)}}
		sdb.queryRows["GetLastPaidPackageByUser"] = []scriptedRow{{
			values: []any{"boost_10_2", "Boost", int32(290), "RUB", pgtype.Timestamptz{}},
		}}
		s := &Service{Q: db.New(sdb)}
		err := s.ensurePaymentAllowed(context.Background(), 10, Package{Code: "boost_10_2"})
		if !errors.Is(err, ErrBoostRequiresBasePackage) {
			t.Fatalf("got err=%v", err)
		}
	})

	t.Run("boost after base package", func(t *testing.T) {
		sdb := newScriptedDB()
		sdb.queryRows["GetUserByID"] = []scriptedRow{{values: userScanValues(baseUser)}}
		sdb.queryRows["GetLastPaidPackageByUser"] = []scriptedRow{{
			values: []any{"base_minimum", "Base", int32(690), "RUB", pgtype.Timestamptz{}},
		}}
		s := &Service{Q: db.New(sdb)}
		if err := s.ensurePaymentAllowed(context.Background(), 10, Package{Code: "boost_10_2"}); err != nil {
			t.Fatalf("unexpected err=%v", err)
		}
	})
}

func TestValidatePreCheckout(t *testing.T) {
	t.Run("empty payload", func(t *testing.T) {
		s := &Service{}
		ok, msg, err := s.validatePreCheckout(context.Background(), &tgbotapi.PreCheckoutQuery{})
		if ok || err != nil || msg == "" {
			t.Fatalf("got ok=%v msg=%q err=%v", ok, msg, err)
		}
	})

	t.Run("invalid uuid", func(t *testing.T) {
		s := &Service{}
		ok, msg, err := s.validatePreCheckout(context.Background(), &tgbotapi.PreCheckoutQuery{InvoicePayload: "bad"})
		if ok || err != nil || !strings.Contains(strings.ToLower(msg), "идентификатор") {
			t.Fatalf("got ok=%v msg=%q err=%v", ok, msg, err)
		}
	})

	t.Run("order mismatch", func(t *testing.T) {
		sdb := newScriptedDB()
		id := uuid.New()
		sdb.queryRows["GetOrderByID"] = []scriptedRow{{
			values: orderScanValues(db.GetOrderByIDRow{
				ID:        pgtype.UUID{Bytes: id, Valid: true},
				UserID:    1,
				PackageID: 2,
				AmountRub: 100,
				Currency:  "RUB",
				Status:    "created",
			}),
		}}
		sdb.execs["MarkOrderFailed"] = []scriptedExec{{tag: pgconn.NewCommandTag("UPDATE 1")}}
		s := &Service{Q: db.New(sdb)}
		ok, msg, err := s.validatePreCheckout(context.Background(), &tgbotapi.PreCheckoutQuery{
			InvoicePayload: id.String(),
			Currency:       "USD",
			TotalAmount:    10000,
		})
		if ok || err != nil || msg == "" {
			t.Fatalf("got ok=%v msg=%q err=%v", ok, msg, err)
		}
		if sdb.execCalls["MarkOrderFailed"] != 1 {
			t.Fatalf("expected MarkOrderFailed call, got=%d", sdb.execCalls["MarkOrderFailed"])
		}
	})

	t.Run("success path", func(t *testing.T) {
		sdb := newScriptedDB()
		id := uuid.New()
		sdb.queryRows["GetOrderByID"] = []scriptedRow{{
			values: orderScanValues(db.GetOrderByIDRow{
				ID:        pgtype.UUID{Bytes: id, Valid: true},
				UserID:    10,
				PackageID: 20,
				AmountRub: 690,
				Currency:  "RUB",
				Status:    "created",
			}),
		}}
		sdb.queryRows["GetUserByID"] = []scriptedRow{
			{values: userScanValues(db.User{ID: 10, TgID: 11})}, // direct check in validatePreCheckout
			{values: userScanValues(db.User{ID: 10, TgID: 11})}, // repeated check in ensurePaymentAllowed
		}
		sdb.queryRows["GetPackageByID"] = []scriptedRow{{
			values: packageScanValues(db.Package{ID: 20, Code: "base_minimum", Title: "Base", PriceRub: 690, Currency: "RUB", IsActive: true}),
		}}
		sdb.execs["MarkOrderPrecheckout"] = []scriptedExec{{tag: pgconn.NewCommandTag("UPDATE 1")}}
		s := &Service{Q: db.New(sdb)}
		ok, msg, err := s.validatePreCheckout(context.Background(), &tgbotapi.PreCheckoutQuery{
			InvoicePayload: id.String(),
			Currency:       "RUB",
			TotalAmount:    69000,
		})
		if !ok || err != nil || msg != "" {
			t.Fatalf("got ok=%v msg=%q err=%v", ok, msg, err)
		}
	})
}

func TestHandlePreCheckoutAndSendInvoice(t *testing.T) {
	t.Run("handle precheckout nil query", func(t *testing.T) {
		s := &Service{}
		if err := s.HandlePreCheckout(context.Background(), nil); err != nil {
			t.Fatalf("unexpected err=%v", err)
		}
	})

	t.Run("handle precheckout sends response", func(t *testing.T) {
		bot, stub := newBotStub(t)
		s := &Service{Bot: bot}
		err := s.HandlePreCheckout(context.Background(), &tgbotapi.PreCheckoutQuery{
			ID:             "pcq-1",
			InvoicePayload: "", // forces validatePreCheckout => not ok, no DB usage
		})
		if err != nil {
			t.Fatalf("unexpected err=%v", err)
		}
		if got := stub.callCount("answerPreCheckoutQuery"); got != 1 {
			t.Fatalf("answerPreCheckoutQuery calls=%d want=1", got)
		}
	})

	t.Run("send invoice fails and marks order failed", func(t *testing.T) {
		bot, stub := newBotStub(t)
		stub.setFail("sendInvoice", true)

		sdb := newScriptedDB()
		sdb.queryRows["GetUserByID"] = []scriptedRow{{values: userScanValues(db.User{ID: 1, TgID: 2})}}
		sdb.queryRows["CreateOrder"] = []scriptedRow{{
			values: createOrderScanValues(db.CreateOrderRow{
				ID:        pgtype.UUID{Bytes: uuid.New(), Valid: true},
				UserID:    1,
				PackageID: 100,
				AmountRub: 690,
				Currency:  "RUB",
				Status:    "created",
			}),
		}}
		sdb.execs["MarkOrderFailed"] = []scriptedExec{{tag: pgconn.NewCommandTag("UPDATE 1")}}

		s := &Service{
			Bot:       bot,
			Q:         db.New(sdb),
			DBTimeout: 2 * time.Second,
		}
		_, err := s.SendInvoiceForPackage(context.Background(), 101, 1, Package{
			ID: 100, Code: "base_minimum", Title: "Base", PriceRub: 690,
		}, "buyer@example.com")
		if err == nil {
			t.Fatal("expected invoice send error")
		}
		if got := stub.callCount("sendInvoice"); got != 1 {
			t.Fatalf("sendInvoice calls=%d want=1", got)
		}
		if sdb.execCalls["MarkOrderFailed"] != 1 {
			t.Fatalf("expected MarkOrderFailed call, got=%d", sdb.execCalls["MarkOrderFailed"])
		}
	})

	t.Run("send invoice success", func(t *testing.T) {
		bot, _ := newBotStub(t)
		sdb := newScriptedDB()
		sdb.queryRows["GetUserByID"] = []scriptedRow{{values: userScanValues(db.User{ID: 1, TgID: 2})}}
		sdb.queryRows["CreateOrder"] = []scriptedRow{{
			values: createOrderScanValues(db.CreateOrderRow{
				ID:        pgtype.UUID{Bytes: uuid.New(), Valid: true},
				UserID:    1,
				PackageID: 100,
				AmountRub: 690,
				Currency:  "RUB",
				Status:    "created",
			}),
		}}
		s := &Service{
			Bot:       bot,
			Q:         db.New(sdb),
			DBTimeout: 2 * time.Second,
		}
		orderID, err := s.SendInvoiceForPackage(context.Background(), 101, 1, Package{
			ID: 100, Code: "base_minimum", Title: "Base", PriceRub: 690,
		}, "")
		if err != nil {
			t.Fatalf("unexpected err=%v", err)
		}
		if _, parseErr := uuid.Parse(orderID); parseErr != nil {
			t.Fatalf("returned order id must be UUID, got=%q err=%v", orderID, parseErr)
		}
	})

	t.Run("provider data is valid json for send invoice", func(t *testing.T) {
		raw := buildProviderDataReceipt("Base", 690, "buyer@example.com")
		var anyJSON map[string]any
		if err := json.Unmarshal([]byte(raw), &anyJSON); err != nil {
			t.Fatalf("provider data must be valid JSON: %v", err)
		}
	})
}
