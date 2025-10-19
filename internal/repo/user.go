package repo

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID         uuid.UUID
	TgUserID   int64
	Username   *string
	IsAdmin    bool
	CreatedAt  time.Time
	LastSeenAt *time.Time
}

type UserRepository interface {
	GetByTgUserID(ctx context.Context, tgUserID int64) (*User, error)
	Create(ctx context.Context, u *User) error
	TouchLastSeen(ctx context.Context, userID uuid.UUID, at time.Time) error
}
