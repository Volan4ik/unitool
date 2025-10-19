package pg

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"unitool/internal/repo"
)

type UserRepo struct{ db *DB }

func NewUserRepo(db *DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) GetByTgUserID(ctx context.Context, tgID int64) (*repo.User, error) {
	const q = `SELECT id, tg_user_id, username, is_admin, created_at, last_seen_at
	           FROM users WHERE tg_user_id=$1`
	row := r.db.Pool.QueryRow(ctx, q, tgID)
	var u repo.User
	var username *string
	var lastSeen *time.Time
	if err := row.Scan(&u.ID, &u.TgUserID, &username, &u.IsAdmin, &u.CreatedAt, &lastSeen); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	u.Username = username
	u.LastSeenAt = lastSeen
	return &u, nil
}

func (r *UserRepo) Create(ctx context.Context, u *repo.User) error {
	const q = `INSERT INTO users (tg_user_id, username, is_admin, last_seen_at)
	           VALUES ($1,$2,$3,$4)
	           RETURNING id, created_at`
	var lastSeen interface{}
	if u.LastSeenAt != nil {
		lastSeen = *u.LastSeenAt
	}
	if err := r.db.Pool.
		QueryRow(ctx, q, u.TgUserID, u.Username, u.IsAdmin, lastSeen).
		Scan(&u.ID, &u.CreatedAt); err != nil {
		return err
	}
	return nil
}

func (r *UserRepo) TouchLastSeen(ctx context.Context, userID uuid.UUID, at time.Time) error {
	const q = `UPDATE users SET last_seen_at=$2 WHERE id=$1`
	_, err := r.db.Pool.Exec(ctx, q, userID, at)
	return err
}
