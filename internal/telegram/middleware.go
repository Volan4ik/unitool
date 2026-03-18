package telegram

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "unitool/internal/db/generated"
)

// EnsureUser upserts the user by TG ID and returns internal user id.
func EnsureUser(ctx context.Context, q *db.Queries, m *tgbotapi.Message) (int64, error) {
	if m == nil || m.From == nil {
		return 0, nil
	}
	var username, firstName, lastName, lang string
	username = m.From.UserName
	firstName = m.From.FirstName
	lastName = m.From.LastName
	lang = m.From.LanguageCode

	u, err := q.UpsertUserByTGID(ctx, db.UpsertUserByTGIDParams{
		TgID:      m.From.ID,
		Username:  pgtype.Text{String: username, Valid: username != ""},
		FirstName: pgtype.Text{String: firstName, Valid: firstName != ""},
		LastName:  pgtype.Text{String: lastName, Valid: lastName != ""},
		LangCode:  pgtype.Text{String: lang, Valid: lang != ""},
	})
	if err != nil {
		return 0, err
	}
	return u.ID, nil
}
