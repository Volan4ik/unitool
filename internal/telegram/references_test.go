package telegram

import (
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestLargestPhotoFileID(t *testing.T) {
	if got := largestPhotoFileID(nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	photos := []tgbotapi.PhotoSize{
		{FileID: "small", Width: 100, Height: 100},
		{FileID: "wide", Width: 300, Height: 80},
		{FileID: "big", Width: 500, Height: 400},
	}
	if got := largestPhotoFileID(photos); got != "big" {
		t.Fatalf("got %q", got)
	}

	// Equal area should pick the last one because of >= comparison.
	photos = []tgbotapi.PhotoSize{
		{FileID: "first", Width: 100, Height: 100},
		{FileID: "second", Width: 200, Height: 50},
	}
	if got := largestPhotoFileID(photos); got != "second" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildPromptWithReferences(t *testing.T) {
	got := buildPromptWithReferences("make it cinematic", []string{"data:image/png;base64,AAA", "  ", "data:image/jpeg;base64,BBB"})
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("unexpected lines count=%d prompt=%q", len(lines), got)
	}
	if lines[0] != "input_reference=data:image/png;base64,AAA" {
		t.Fatalf("unexpected first line: %q", lines[0])
	}
	if lines[2] != "make it cinematic" {
		t.Fatalf("unexpected prompt line: %q", lines[2])
	}

	if same := buildPromptWithReferences("prompt only", nil); same != "prompt only" {
		t.Fatalf("unexpected prompt: %q", same)
	}
}

func TestPromptWithoutReferenceLines(t *testing.T) {
	raw := strings.Join([]string{
		"input_reference=data:image/png;base64,AAA",
		"subject in neon city",
		"  input_reference: https://example.com/image.jpg ",
		"camera: close-up",
	}, "\n")
	got := promptWithoutReferenceLines(raw)
	if strings.Contains(strings.ToLower(got), "input_reference") {
		t.Fatalf("reference line leaked: %q", got)
	}
	if !strings.Contains(got, "subject in neon city") || !strings.Contains(got, "camera: close-up") {
		t.Fatalf("prompt lines missing: %q", got)
	}
}

func TestParseTelegramInputReferenceLine(t *testing.T) {
	cases := []struct {
		line string
		want string
		ok   bool
	}{
		{line: "input_reference=data:image/png;base64,AAA", want: "data:image/png;base64,AAA", ok: true},
		{line: " input_reference: <https://example.com/a.png> ", want: "https://example.com/a.png", ok: true},
		{line: `input_reference: "https://example.com/b.png"`, want: "https://example.com/b.png", ok: true},
		{line: "input_reference", want: "", ok: false},
		{line: "input_reference-", want: "", ok: false},
		{line: "other=value", want: "", ok: false},
	}
	for _, tc := range cases {
		got, ok := parseTelegramInputReferenceLine(tc.line)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("line=%q got=(%q,%v) want=(%q,%v)", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}
