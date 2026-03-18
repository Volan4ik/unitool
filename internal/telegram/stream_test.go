package telegram

import (
	"strings"
	"testing"
)

func TestSplitTelegramTextWithinLimit(t *testing.T) {
	in := "hello"
	chunks := splitTelegramText(in, tgMessageLimit)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != in {
		t.Fatalf("unexpected chunk: %q", chunks[0])
	}
}

func TestSplitTelegramTextOverLimit(t *testing.T) {
	in := strings.Repeat("a", tgMessageLimit+10)
	chunks := splitTelegramText(in, tgMessageLimit)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if len([]rune(chunks[0])) != tgMessageLimit {
		t.Fatalf("unexpected first chunk len: %d", len([]rune(chunks[0])))
	}
	if len([]rune(chunks[1])) != 10 {
		t.Fatalf("unexpected second chunk len: %d", len([]rune(chunks[1])))
	}
}

func TestFirstTelegramChunk(t *testing.T) {
	in := strings.Repeat("b", tgEditPreviewLimit+20)
	got := firstTelegramChunk(in, tgEditPreviewLimit)
	if len([]rune(got)) != tgEditPreviewLimit {
		t.Fatalf("unexpected preview len: %d", len([]rune(got)))
	}
}
