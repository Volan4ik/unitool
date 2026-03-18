package telegram

import "context"

type updateIDContextKey struct{}

func withUpdateID(ctx context.Context, updateID int64) context.Context {
	if ctx == nil || updateID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, updateIDContextKey{}, updateID)
}

func updateIDFromContext(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	v := ctx.Value(updateIDContextKey{})
	id, ok := v.(int64)
	if !ok || id <= 0 {
		return 0
	}
	return id
}
