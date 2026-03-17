package storage

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PG struct {
	Pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string, maxConns, minConns int) (*PG, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if maxConns <= 0 {
		maxConns = 50
	}
	if minConns < 0 {
		minConns = 0
	}
	if minConns > maxConns {
		minConns = maxConns
	}
	cfg.MaxConns = int32(maxConns)
	cfg.MinConns = int32(minConns)
	cfg.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &PG{Pool: pool}, nil
}

func (p *PG) Close() { p.Pool.Close() }
