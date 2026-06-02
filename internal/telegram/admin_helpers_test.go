package telegram

import (
	"context"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"unitool/internal/admin"
	db "unitool/internal/db/generated"
)

func TestParsePackageInput(t *testing.T) {
	raw := "base_minimum|Базовый минимум|690|RUB|30|5|true"
	got, err := parsePackageInput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Code != "base_minimum" || got.PriceRub != 690 || got.AttemptsImage != 30 || got.AttemptsVideo != 5 || !got.IsActive {
		t.Fatalf("unexpected parsed input: %+v", got)
	}

	if _, err := parsePackageInput("too|short"); err == nil {
		t.Fatal("expected error for short input")
	}
	if _, err := parsePackageInput("x|y|bad|RUB|1|1|true"); err == nil {
		t.Fatal("expected error for invalid price")
	}
}

func TestParsePackageEditInput(t *testing.T) {
	raw := "12|golden_middle|Золотая середина|1490|RUB|100|10|1"
	id, in, err := parsePackageEditInput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 12 || in.Code != "golden_middle" || !in.IsActive {
		t.Fatalf("unexpected parsed edit input: id=%d in=%+v", id, in)
	}

	if _, _, err := parsePackageEditInput("bad|input"); err == nil {
		t.Fatal("expected error for bad edit input")
	}
}

func TestParseBanInput(t *testing.T) {
	id, reason, err := parseBanInput("123456|abuse")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 123456 || reason != "abuse" {
		t.Fatalf("unexpected parsed ban input: id=%d reason=%q", id, reason)
	}

	if _, _, err := parseBanInput("123456|   "); err == nil {
		t.Fatal("expected error for empty reason")
	}
}

func TestParseGrantInput(t *testing.T) {
	got, err := parseGrantInput("123456|7|2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TargetTGID != 123456 || got.ImageCredits != 7 || got.VideoCredits != 2 {
		t.Fatalf("unexpected parsed grant input: %+v", got)
	}

	if _, err := parseGrantInput("123456|bad|2"); err == nil {
		t.Fatal("expected error for invalid image credits")
	}
	if _, err := parseGrantInput("123456|1"); err == nil {
		t.Fatal("expected error for short input")
	}
}

func TestAdminFlowMainReplyCommandCancelsPendingInput(t *testing.T) {
	r := &Router{
		Admin:     &admin.Service{},
		adminIDs:  map[int64]struct{}{10: {}},
		adminFlow: map[int64]adminFlowState{10: {Action: adminActionGrantCredits}},
	}
	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 10},
		From: &tgbotapi.User{ID: 10},
	}

	handled, err := r.handleAdminTextInput(context.Background(), msg, 10, "Фото")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handled {
		t.Fatal("main reply command must continue through regular message handling")
	}
	if _, ok := r.getAdminFlow(10); ok {
		t.Fatal("admin flow must be cleared after main reply command")
	}
}

func TestAdminFlowCancelTextStopsPendingInput(t *testing.T) {
	api, mock := newTelegramBotMock(t)
	r := &Router{
		Bot:       &Bot{API: api},
		Admin:     &admin.Service{},
		adminIDs:  map[int64]struct{}{10: {}},
		adminFlow: map[int64]adminFlowState{10: {Action: adminActionGrantCredits}},
	}
	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 10},
		From: &tgbotapi.User{ID: 10},
	}

	handled, err := r.handleAdminTextInput(context.Background(), msg, 10, "Отмена")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatal("cancel text must not continue through regular message handling")
	}
	if _, ok := r.getAdminFlow(10); ok {
		t.Fatal("admin flow must be cleared after cancel text")
	}
	if mock.callCount("sendMessage") != 1 {
		t.Fatalf("sendMessage calls=%d want=1", mock.callCount("sendMessage"))
	}
}

func TestParseBoolAndSplitInput(t *testing.T) {
	truthy := []string{"1", "true", "YES", "y", "on"}
	for _, raw := range truthy {
		v, err := parseBool(raw)
		if err != nil || !v {
			t.Fatalf("raw=%q got=(%v,%v)", raw, v, err)
		}
	}
	falsy := []string{"0", "false", "NO", "n", "off"}
	for _, raw := range falsy {
		v, err := parseBool(raw)
		if err != nil || v {
			t.Fatalf("raw=%q got=(%v,%v)", raw, v, err)
		}
	}
	if _, err := parseBool("maybe"); err == nil {
		t.Fatal("expected parseBool error")
	}

	parts := splitInput(" a | b | c ", 3)
	if len(parts) != 3 || parts[0] != "a" || parts[1] != "b" || parts[2] != "c" {
		t.Fatalf("unexpected split result: %#v", parts)
	}
}

func TestFormattingHelpers(t *testing.T) {
	if got := textOrDash("name", true); got != "name" {
		t.Fatalf("got %q", got)
	}
	if got := textOrDash("", true); got != "-" {
		t.Fatalf("got %q", got)
	}
	if got := formatTS(time.Time{}, false); got != "-" {
		t.Fatalf("got %q", got)
	}

	ts := time.Date(2026, 4, 11, 10, 20, 30, 0, time.UTC)
	if got := formatTS(ts, true); got != "2026-04-11T10:20:30Z" {
		t.Fatalf("got %q", got)
	}

	notification := formatGrantNotification(admin.GrantInput{
		ImageCredits: 3,
		VideoCredits: 1,
	})
	if notification != "Вам начислено фото: 3, видео: 1." {
		t.Fatalf("unexpected notification: %q", notification)
	}

	result := formatGrantResult(admin.GrantResult{
		User:         db.User{TgID: 123},
		RowsAffected: 1,
		ImageBalance: 10,
		VideoBalance: 4,
	})
	if result == "" {
		t.Fatal("formatted grant result must not be empty")
	}
}

func TestFormatPackageAndUserCard(t *testing.T) {
	p := db.Package{
		ID:           7,
		Code:         "base_minimum",
		Title:        "Base",
		PriceRub:     690,
		Currency:     "RUB",
		ImageCredits: 30,
		VideoCredits: 5,
		IsActive:     true,
		CreatedAt:    pgtype.Timestamptz{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Valid: true},
		UpdatedAt:    pgtype.Timestamptz{Time: time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC), Valid: true},
	}
	formattedPackage := formatPackage(p)
	if formattedPackage == "" {
		t.Fatal("formatted package must not be empty")
	}

	card := formatUserCard(admin.UserCard{
		User: db.User{
			ID:           1,
			TgID:         2,
			Username:     pgtype.Text{String: "user", Valid: true},
			ImageBalance: 3,
			VideoBalance: 4,
			CreatedAt:    pgtype.Timestamptz{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
		},
		Status:         "active",
		TotalGenerates: 9,
		LastPackage:    "base_minimum",
	})
	if card == "" {
		t.Fatal("formatted user card must not be empty")
	}
}
