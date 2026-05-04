package telegram

import (
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "unitool/internal/db/generated"
)

func TestNormalizeSourceTag(t *testing.T) {
	t.Run("empty becomes default", func(t *testing.T) {
		if got := normalizeSourceTag("   "); got != defaultSourceTag {
			t.Fatalf("got %q want %q", got, defaultSourceTag)
		}
	})

	t.Run("keeps allowed chars and lowercases", func(t *testing.T) {
		got := normalizeSourceTag("  TeST.Source_Tag-42 !@#$%^&*() ")
		if got != "test.source_tag-42" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("all invalid becomes default", func(t *testing.T) {
		if got := normalizeSourceTag("!!!***"); got != defaultSourceTag {
			t.Fatalf("got %q want %q", got, defaultSourceTag)
		}
	})

	t.Run("too long truncates", func(t *testing.T) {
		raw := strings.Repeat("a", maxSourceTagLen+10)
		got := normalizeSourceTag(raw)
		if len(got) != maxSourceTagLen {
			t.Fatalf("len(got)=%d want=%d", len(got), maxSourceTagLen)
		}
	})
}

func TestIsModeChooserMessage(t *testing.T) {
	t.Run("valid chooser keyboard", func(t *testing.T) {
		kb := ModeInlineKeyboard()
		msg := &tgbotapi.Message{ReplyMarkup: &kb}
		if !isModeChooserMessage(msg) {
			t.Fatal("expected true")
		}
	})

	t.Run("invalid shape", func(t *testing.T) {
		msg := &tgbotapi.Message{
			ReplyMarkup: &tgbotapi.InlineKeyboardMarkup{
				InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{
					{tgbotapi.NewInlineKeyboardButtonData("Фото", "mode:image")},
				},
			},
		}
		if isModeChooserMessage(msg) {
			t.Fatal("expected false")
		}
	})

	t.Run("nil message", func(t *testing.T) {
		if isModeChooserMessage(nil) {
			t.Fatal("expected false")
		}
	})
}

func TestOrderPublicPackages(t *testing.T) {
	pkgs := []db.Package{
		{Code: "custom_x"},
		{Code: "boost_10_2"},
		{Code: "video_base_minimum"},
		{Code: "photo_golden_middle"},
		{Code: "photo_base_minimum"},
		{Code: "video_luxury_maximum"},
		{Code: "photo_luxury_maximum"},
		{Code: "video_golden_middle"},
		{Code: "custom_y"},
	}
	got := orderPublicPackages(pkgs)
	wantCodes := []string{
		"photo_base_minimum",
		"photo_golden_middle",
		"photo_luxury_maximum",
		"video_base_minimum",
		"video_golden_middle",
		"video_luxury_maximum",
		"boost_10_2",
		"custom_x",
		"custom_y",
	}
	if len(got) != len(wantCodes) {
		t.Fatalf("len(got)=%d want=%d", len(got), len(wantCodes))
	}
	for i := range wantCodes {
		if got[i].Code != wantCodes[i] {
			t.Fatalf("idx=%d got=%q want=%q", i, got[i].Code, wantCodes[i])
		}
	}
}

func TestPackageButtonLabel(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{code: "photo_base_minimum", want: "Базовый минимум | Фото"},
		{code: "photo_golden_middle", want: "Золотая середина | Фото"},
		{code: "photo_luxury_maximum", want: "Роскошный максимум | Фото"},
		{code: "video_base_minimum", want: "Базовый минимум | Видео"},
		{code: "video_golden_middle", want: "Золотая середина | Видео"},
		{code: "video_luxury_maximum", want: "Роскошный максимум | Видео"},
		{code: "boost_10_2", want: "Буст: +10 фото и 2 видео"},
		{code: "other", want: "Custom"},
	}
	for _, tc := range cases {
		got := packageButtonLabel(db.Package{Code: tc.code, Title: "Custom"})
		if got != tc.want {
			t.Fatalf("code=%q got=%q want=%q", tc.code, got, tc.want)
		}
	}
}

func TestBuildPackagesMenuText(t *testing.T) {
	text := buildPackagesMenuText()
	if !strings.Contains(text, "<b>1. Базовый минимум: 99 руб</b>") {
		t.Fatalf("unexpected text: %q", text)
	}
	if !strings.Contains(text, "<b>1. Базовый минимум: 249 руб</b>") {
		t.Fatalf("unexpected text: %q", text)
	}
	if !strings.Contains(text, "299 рублей") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestPackageKindHelpers(t *testing.T) {
	if !isBoostPackage(db.Package{Code: " boost_10_2 "}) {
		t.Fatal("expected boost package")
	}
	if isBoostPackage(db.Package{Code: "photo_base_minimum"}) {
		t.Fatal("base package must not be boost")
	}

	if !isBasePackage(db.Package{Code: "photo_base_minimum", ImageCredits: 1}) {
		t.Fatal("expected base package")
	}
	if isBasePackage(db.Package{Code: "boost_10_2", ImageCredits: 10, VideoCredits: 2}) {
		t.Fatal("boost package must not be treated as base")
	}
}

func TestGenerationRequestConverters(t *testing.T) {
	now := time.Now().UTC()
	rowExisting := db.GetGenerationRequestByUpdateIDRow{
		ID:               10,
		UserID:           20,
		UpdateID:         pgtype.Int8{Int64: 30, Valid: true},
		Kind:             "image",
		Provider:         "comet",
		Model:            "m1",
		OutputTokens:     pgtype.Int4{Int32: 11, Valid: true},
		CostCreditsText:  0,
		CostCreditsImage: 1,
		CostCreditsVideo: 0,
		Status:           "queued",
		ErrorMessage:     pgtype.Text{String: "x", Valid: true},
		LatencyMs:        pgtype.Int4{Int32: 55, Valid: true},
		CreatedAt:        pgtype.Timestamptz{Time: now, Valid: true},
		FinishedAt:       pgtype.Timestamptz{},
	}
	gotExisting := generationRequestFromExisting(rowExisting)
	if gotExisting.ID != rowExisting.ID || gotExisting.Model != rowExisting.Model || gotExisting.Status != rowExisting.Status {
		t.Fatalf("unexpected conversion from existing: %+v", gotExisting)
	}

	rowInsert := db.InsertGenerationRequestRow{
		ID:               99,
		UserID:           7,
		UpdateID:         pgtype.Int8{Int64: 100, Valid: true},
		Kind:             "video",
		Provider:         "comet",
		Model:            "m2",
		OutputTokens:     pgtype.Int4{},
		CostCreditsText:  0,
		CostCreditsImage: 0,
		CostCreditsVideo: 1,
		Status:           "running",
		ErrorMessage:     pgtype.Text{},
		LatencyMs:        pgtype.Int4{},
		CreatedAt:        pgtype.Timestamptz{Time: now, Valid: true},
		FinishedAt:       pgtype.Timestamptz{},
	}
	gotInsert := generationRequestFromInsert(rowInsert)
	if gotInsert.ID != rowInsert.ID || gotInsert.Kind != rowInsert.Kind || gotInsert.CostCreditsVideo != 1 {
		t.Fatalf("unexpected conversion from insert: %+v", gotInsert)
	}
}

func TestGenerationRequestHelpers(t *testing.T) {
	if got := generationRequestStatus("ok"); got != "ok" {
		t.Fatalf("got %q", got)
	}
	if got := generationRequestStatus(123); got != "123" {
		t.Fatalf("got %q", got)
	}

	if !isTerminalGenerationRequestStatus("ok") {
		t.Fatal("expected terminal")
	}
	if isTerminalGenerationRequestStatus("queued") {
		t.Fatal("queued must not be terminal")
	}

	if !isUniqueViolation(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("expected unique violation")
	}
	if isUniqueViolation(&pgconn.PgError{Code: "40001"}) {
		t.Fatal("unexpected unique violation")
	}

	i8 := toInt8(5)
	if !i8.Valid || i8.Int64 != 5 {
		t.Fatalf("unexpected pgtype.Int8: %+v", i8)
	}
	i8 = toInt8(0)
	if i8.Valid {
		t.Fatalf("expected invalid int8, got %+v", i8)
	}
}

func TestKindAndModeHelpers(t *testing.T) {
	if kindToRus("image") != "картинки" {
		t.Fatalf("unexpected kind translation")
	}
	if kindToRus("unknown") != "контента" {
		t.Fatalf("unexpected fallback translation")
	}

	if !isSupportedMode("image") || !isSupportedMode("video") {
		t.Fatal("supported modes must be true")
	}
	if isSupportedMode("text") {
		t.Fatal("text must be unsupported")
	}
}

func TestCallbackChatID(t *testing.T) {
	if id, ok := callbackChatID(nil); ok || id != 0 {
		t.Fatalf("nil callback must be empty: id=%d ok=%v", id, ok)
	}

	withMsg := &tgbotapi.CallbackQuery{
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 42}},
		From:    &tgbotapi.User{ID: 99},
	}
	if id, ok := callbackChatID(withMsg); !ok || id != 42 {
		t.Fatalf("got id=%d ok=%v", id, ok)
	}

	withFrom := &tgbotapi.CallbackQuery{From: &tgbotapi.User{ID: 99}}
	if id, ok := callbackChatID(withFrom); !ok || id != 99 {
		t.Fatalf("got id=%d ok=%v", id, ok)
	}
}

func TestSplitTelegramTextAndFirstChunk(t *testing.T) {
	if got := splitTelegramText("", 3); got != nil {
		t.Fatalf("expected nil for empty text, got %#v", got)
	}

	chunks := splitTelegramText("abcdef", 2)
	want := []string{"ab", "cd", "ef"}
	if len(chunks) != len(want) {
		t.Fatalf("len(chunks)=%d want=%d", len(chunks), len(want))
	}
	for i := range want {
		if chunks[i] != want[i] {
			t.Fatalf("idx=%d got=%q want=%q", i, chunks[i], want[i])
		}
	}

	// Must split by runes, not bytes.
	chunks = splitTelegramText("абвг", 3)
	if len(chunks) != 2 || chunks[0] != "абв" || chunks[1] != "г" {
		t.Fatalf("unexpected unicode split: %#v", chunks)
	}

	if first := firstTelegramChunk("abcdef", 2); first != "ab" {
		t.Fatalf("got %q", first)
	}
	if first := firstTelegramChunk("", 2); first != "" {
		t.Fatalf("got %q", first)
	}
}
