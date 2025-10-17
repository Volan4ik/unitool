package repo

import "time"

type ModelRequestRow struct {
	ID               int64
	UserID           int64
	Model            string
	PromptLen        int
	CompletionLen    *int
	LatencyMS        int
	Error            *string
	TokensPrompt     *int
	TokensCompletion *int
	Cost             *float64
	CreatedAt        time.Time
}

type ModelRequestsRepository interface {
	InsertModelRequest(ctxCtx interface{}, row ModelRequestRow) error
}

type UsageLogRow struct {
	ID        int64
	UserID    int64
	Model     string
	Tokens    *int
	Cost      *float64
	CreatedAt time.Time
}

type UsageLogsRepository interface {
	InsertUsage(ctxCtx interface{}, row UsageLogRow) error
}