package telegram

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "unitool/internal/db/generated"
)

type atomicScriptedRow struct {
	values []any
	err    error
}

func (r atomicScriptedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan values mismatch: got %d want %d", len(dest), len(r.values))
	}
	for i := range dest {
		if err := assignAtomicScanDest(dest[i], r.values[i]); err != nil {
			return fmt.Errorf("scan col=%d: %w", i, err)
		}
	}
	return nil
}

type atomicScriptedExec struct {
	tag pgconn.CommandTag
	err error
}

type atomicBeginDB struct {
	tx *atomicTx
}

func (db *atomicBeginDB) Begin(context.Context) (pgx.Tx, error) { return db.tx, nil }
func (db *atomicBeginDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("exec outside tx")
}
func (db *atomicBeginDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("query outside tx")
}
func (db *atomicBeginDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return atomicScriptedRow{err: errors.New("query row outside tx")}
}

type atomicTx struct {
	mu sync.Mutex

	queryRows map[string][]atomicScriptedRow
	execs     map[string][]atomicScriptedExec
	calls     []string

	child *atomicTx

	commitCount   int
	rollbackCount int
}

func newAtomicTx() *atomicTx {
	return &atomicTx{
		queryRows: make(map[string][]atomicScriptedRow),
		execs:     make(map[string][]atomicScriptedExec),
	}
}

func (tx *atomicTx) Begin(context.Context) (pgx.Tx, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.calls = append(tx.calls, "BEGIN")
	if tx.child == nil {
		tx.child = newAtomicTx()
	}
	return tx.child, nil
}

func (tx *atomicTx) Commit(context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.commitCount++
	tx.calls = append(tx.calls, "COMMIT")
	return nil
}

func (tx *atomicTx) Rollback(context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.rollbackCount++
	tx.calls = append(tx.calls, "ROLLBACK")
	return nil
}

func (tx *atomicTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	name := atomicSQLQueryName(sql)
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.calls = append(tx.calls, name)
	queue := tx.execs[name]
	if len(queue) == 0 {
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	spec := queue[0]
	tx.execs[name] = queue[1:]
	return spec.tag, spec.err
}

func (tx *atomicTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	name := atomicSQLQueryName(sql)
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.calls = append(tx.calls, name)
	queue := tx.queryRows[name]
	if len(queue) == 0 {
		return atomicScriptedRow{err: fmt.Errorf("no scripted row for query %q", name)}
	}
	spec := queue[0]
	tx.queryRows[name] = queue[1:]
	return spec
}

func (tx *atomicTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("query not expected")
}
func (tx *atomicTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("copy not expected")
}
func (tx *atomicTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (tx *atomicTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (tx *atomicTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("prepare not expected")
}
func (tx *atomicTx) Conn() *pgx.Conn { return nil }

func TestSubmitAsyncGenerationRollsBackSpendWhenEnqueueFails(t *testing.T) {
	parent := newAtomicTx()
	spend := newAtomicTx()
	parent.child = spend

	now := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	convID := uuid.New()
	parent.queryRows["GetGenerationRequestByUpdateID"] = []atomicScriptedRow{{err: pgx.ErrNoRows}}
	parent.queryRows["InsertGenerationRequest"] = []atomicScriptedRow{{
		values: []any{
			int64(10),
			int64(20),
			pgtype.Int8{Int64: 30, Valid: true},
			"image",
			"comet",
			"gpt-image-2",
			pgtype.Int4{},
			int32(0),
			int32(0),
			int32(0),
			"queued",
			pgtype.Text{},
			pgtype.Int4{},
			now,
			pgtype.Timestamptz{},
		},
	}}
	parent.queryRows["InsertChatMessage"] = []atomicScriptedRow{{
		values: []any{
			int64(100),
			int64(20),
			pgtype.UUID{Bytes: convID, Valid: true},
			"image",
			"user",
			pgtype.Text{String: "prompt", Valid: true},
			pgtype.Text{},
			pgtype.Text{String: "comet", Valid: true},
			pgtype.Text{String: "gpt-image-2", Valid: true},
			pgtype.Int4{},
			pgtype.Int4{},
			pgtype.Int8{Int64: 10, Valid: true},
			now,
		},
	}}
	parent.queryRows["EnqueueGenerationJob"] = []atomicScriptedRow{{err: errors.New("enqueue boom")}}
	spend.execs["SpendImage"] = []atomicScriptedExec{{tag: pgconn.NewCommandTag("INSERT 1")}}

	r := &Router{Q: db.New(&atomicBeginDB{tx: parent})}
	_, err := r.submitAsyncGeneration(context.Background(), submitAsyncGenerationParams{
		ChatID:         99,
		UserID:         20,
		ConversationID: convID,
		UpdateID:       30,
		Kind:           "image",
		Provider:       "comet",
		Model:          "gpt-image-2",
		CleanPrompt:    "prompt",
		RawPrompt:      "prompt",
		MaxAttempts:    1,
	})
	if err == nil || !strings.Contains(err.Error(), "enqueue boom") {
		t.Fatalf("expected enqueue error, got %v", err)
	}
	if parent.commitCount != 0 || parent.rollbackCount != 1 {
		t.Fatalf("parent tx commit=%d rollback=%d, want commit=0 rollback=1", parent.commitCount, parent.rollbackCount)
	}
	if spend.commitCount != 1 || spend.rollbackCount != 0 {
		t.Fatalf("spend savepoint commit=%d rollback=%d, want commit=1 rollback=0", spend.commitCount, spend.rollbackCount)
	}
	if !containsAtomicCall(spend.calls, "SpendImage") {
		t.Fatalf("spend was not attempted, calls=%v", spend.calls)
	}
	if !containsAtomicCall(parent.calls, "EnqueueGenerationJob") {
		t.Fatalf("enqueue was not attempted, calls=%v", parent.calls)
	}
}

func atomicSQLQueryName(sql string) string {
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

func assignAtomicScanDest(dest any, value any) error {
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

func containsAtomicCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}
