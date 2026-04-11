package generation

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "unitool/internal/db/generated"
)

func TestIsHTTPURL(t *testing.T) {
	if !isHTTPURL("https://example.com/path") {
		t.Fatal("expected true for https URL")
	}
	if !isHTTPURL("http://example.com") {
		t.Fatal("expected true for http URL")
	}
	if isHTTPURL("ftp://example.com") {
		t.Fatal("expected false for unsupported scheme")
	}
	if isHTTPURL("https:///no-host") {
		t.Fatal("expected false for missing host")
	}
}

func TestShouldDownloadVideoFirst(t *testing.T) {
	if !shouldDownloadVideoFirst("https://api.cometapi.com/v1/videos/abc/content") {
		t.Fatal("expected true for comet content URL")
	}
	if shouldDownloadVideoFirst("https://api.cometapi.com/v1/images/abc/content") {
		t.Fatal("expected false for non-video endpoint")
	}
	if shouldDownloadVideoFirst("https://example.com/v1/videos/abc/content") {
		t.Fatal("expected false for non-comet host")
	}
}

func TestDataImageURIHelpers(t *testing.T) {
	raw := `see this: data:image/png;base64,aGVsbG8=)`
	extracted := extractDataImageURI(raw)
	if extracted != "data:image/png;base64,aGVsbG8=" {
		t.Fatalf("unexpected extracted URI: %q", extracted)
	}
	if !isDataImageURI(raw) {
		t.Fatal("expected true for embedded data URI")
	}
	if isDataImageURI("no image here") {
		t.Fatal("expected false")
	}
}

func TestDecodeDataImageURI(t *testing.T) {
	okURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("hello"))
	header, payload, err := decodeDataImageURI(okURI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header != "data:image/png;base64" {
		t.Fatalf("unexpected header: %q", header)
	}
	if string(payload) != "hello" {
		t.Fatalf("unexpected payload: %q", string(payload))
	}

	if _, _, err := decodeDataImageURI("data:text/plain;base64,SGk="); err == nil {
		t.Fatal("expected non-image error")
	}
	if _, _, err := decodeDataImageURI("data:image/png;base64,@@@"); err == nil {
		t.Fatal("expected invalid base64 error")
	}
	if _, _, err := decodeDataImageURI("no data uri"); err == nil {
		t.Fatal("expected missing uri error")
	}
}

func TestParseAndSplitInputReferences(t *testing.T) {
	ref, ok := parseInputReferenceLine(`input_reference: "https://example.com/a.png"`)
	if !ok || ref != "https://example.com/a.png" {
		t.Fatalf("unexpected parse result: ref=%q ok=%v", ref, ok)
	}
	if _, ok := parseInputReferenceLine("input_ref=abc"); ok {
		t.Fatal("expected false for unknown prefix")
	}

	raw := strings.Join([]string{
		"input_reference=data:image/png;base64,AAA",
		"please keep style",
		"input_reference: https://example.com/x.png",
		"high contrast",
	}, "\n")
	prompt, refs := splitPromptInputReferences(raw)
	if prompt != "please keep style\nhigh contrast" {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
	if len(refs) != 2 {
		t.Fatalf("unexpected refs length: %d", len(refs))
	}
}

func TestGenerationOutputAndErrorsHelpers(t *testing.T) {
	short := shortOutput("  hello  ")
	if short != "hello" {
		t.Fatalf("unexpected shortOutput: %q", short)
	}

	longInput := strings.Repeat("a", maxProviderOutputSize+50)
	long := shortOutput(longInput)
	if !strings.HasSuffix(long, "...(truncated)") {
		t.Fatalf("expected truncation suffix, got %q", long)
	}

	moderationErr := "Blocked by our moderation system when checking inputs"
	if !isProviderModerationError(moderationErr) {
		t.Fatal("expected moderation detection")
	}
	if isProviderModerationError("network timeout") {
		t.Fatal("unexpected moderation detection")
	}

	msg := userFailureMessage(moderationErr, false)
	if !strings.Contains(msg, promptRulesURL) || !strings.Contains(msg, "Попытка возвращена") {
		t.Fatalf("unexpected moderation user message: %q", msg)
	}

	msg = userFailureMessage("random error", true)
	if !strings.Contains(msg, "в обработке") {
		t.Fatalf("unexpected refund pending message: %q", msg)
	}
}

func TestGenerationJobFromClaimed(t *testing.T) {
	now := time.Now().UTC()
	row := db.ClaimNextGenerationJobRow{
		ID:                  1,
		GenerationRequestID: 2,
		UserID:              3,
		ChatID:              4,
		ConversationID:      pgtype.UUID{},
		Kind:                "image",
		Provider:            "comet",
		Model:               "m",
		Prompt:              "p",
		Status:              "queued",
		ResultText:          pgtype.Text{},
		ErrorMessage:        pgtype.Text{},
		Attempts:            0,
		MaxAttempts:         3,
		NextAttemptAt:       pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:           pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:           pgtype.Timestamptz{Time: now, Valid: true},
		FinishedAt:          pgtype.Timestamptz{},
	}

	job := generationJobFromClaimed(row)
	if job.ID != row.ID || job.UserID != row.UserID || job.Model != row.Model {
		t.Fatalf("unexpected job conversion: %+v", job)
	}
}
