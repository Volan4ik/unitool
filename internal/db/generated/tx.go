package generated

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

func (q *Queries) BeginTx(ctx context.Context) (pgx.Tx, error) {
	if q == nil {
		return nil, errors.New("queries is nil")
	}
	b, ok := q.db.(txBeginner)
	if !ok {
		return nil, errors.New("queries db does not support transactions")
	}
	return b.Begin(ctx)
}
