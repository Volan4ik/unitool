package telegram

import (
	"context"
	"testing"
)

func TestUpdateIDContextRoundTrip(t *testing.T) {
	ctx := withUpdateID(context.Background(), 12345)
	if got := updateIDFromContext(ctx); got != 12345 {
		t.Fatalf("expected update_id=12345, got %d", got)
	}
}

func TestUpdateIDContextZeroIgnored(t *testing.T) {
	ctx := withUpdateID(context.Background(), 0)
	if got := updateIDFromContext(ctx); got != 0 {
		t.Fatalf("expected update_id=0, got %d", got)
	}
}
