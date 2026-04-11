package telegram

import (
	"context"
	"testing"
)

func TestUpdateIDContext(t *testing.T) {
	if got := updateIDFromContext(nil); got != 0 {
		t.Fatalf("got %d", got)
	}

	base := context.Background()
	ctx := withUpdateID(base, 0)
	if ctx != base {
		t.Fatal("with invalid updateID must return original context")
	}

	ctx = withUpdateID(base, 123)
	if got := updateIDFromContext(ctx); got != 123 {
		t.Fatalf("got %d want 123", got)
	}

	other := context.WithValue(base, updateIDContextKey{}, "bad-type")
	if got := updateIDFromContext(other); got != 0 {
		t.Fatalf("got %d", got)
	}
}
