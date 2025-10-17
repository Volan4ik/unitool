package repo

import "context"


type User struct {
	ID                     int64
	TgID                   int64
	Username               *string
	FirstName              *string
	FreeTries              int
	Credits                int
	SubscriptionExpiresAt  *time.Time
	CreatedAt              time.Time
	LastActiveAt           *time.Time
}

type UserRepository interface {
	GetByTgID(ctx context.Context, tgID int64) (*User, error)
	Create(ctx context.Context, u *User) error
	DecrementFreeTry(ctx context.Context, tgID int64) (remaining int, err error)
	AddCredits(ctx context.Context, userID int64, delta int) error
}