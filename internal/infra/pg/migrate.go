package pg

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"unitool/migrations"
)

// MigrateAll executes all embedded *.sql in name order, tracking progress in schema_migrations.
func MigrateAll(ctx context.Context, db *DB) error {
	// ensure schema_migrations
	if _, err := db.Pool.Exec(ctx, `
	  CREATE TABLE IF NOT EXISTS schema_migrations (
	    id serial PRIMARY KEY,
	    name text UNIQUE NOT NULL,
	    applied_at timestamptz NOT NULL DEFAULT now()
	  );`); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}

	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists bool
		if err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sqlBytes, err := migrations.Files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(name) VALUES($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("mark %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Optional: single-file legacy (not used in prod)
func Migrate(ctx context.Context, db *DB, sql string) error {
	_, err := db.Pool.Exec(ctx, sql)
	return err
}
