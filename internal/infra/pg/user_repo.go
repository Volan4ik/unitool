package pg

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/yourorg/ai-telebot/internal/repo"
)

type UserRepo struct{ db *DB }

func NewUserRepo(db *DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) GetByTgID(ctx context.Context, tgID int64) (*repo.User, error) {
	const q = `SELECT id, tg_id, free_tries, credits FROM users WHERE tg_id=$1`
	row := r.db.Pool.QueryRow(ctx, q, tgID)
	var u repo.User
	if err := row.Scan(&u.ID, &u.TgID, &u.FreeTries, &u.Credits); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) AddCredits(ctx context.Context, userID int64, delta int) error {
	const q = `UPDATE users SET credits = credits + $2 WHERE id=$1`
	_, err := r.db.Pool.Exec(ctx, q, userID, delta)
	return err
}

func (r *UserRepo) Create(ctx context.Context, u *repo.User) error {
	const q = `INSERT INTO users (tg_id, free_tries, credits) VALUES ($1,$2,$3) RETURNING id`
	return r.db.Pool.QueryRow(ctx, q, u.TgID, u.FreeTries, u.Credits).Scan(&u.ID)
}

func (r *UserRepo) DecrementFreeTry(ctx context.Context, tgID int64) (int, error) {
	const q = `UPDATE users
	           SET free_tries = GREATEST(free_tries-1,0)
			   WHERE tg_id=$1
			   RETURNING free_tries`
	var left int
	err := r.db.Pool.QueryRow(ctx, q, tgID).Scan(&left)
	return left, err
}