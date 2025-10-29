package storage

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PG struct {
	Pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*PG, error) {
    cfg, err := pgxpool.ParseConfig(dsn)
    if err != nil { return nil, err }
    cfg.MaxConns = 50
    cfg.MinConns = 10
    cfg.HealthCheckPeriod = 30 * time.Second
    pool, err := pgxpool.NewWithConfig(ctx, cfg)
    if err != nil { return nil, err }
    return &PG{Pool: pool}, nil
}

func (p *PG) Close() { p.Pool.Close() }
