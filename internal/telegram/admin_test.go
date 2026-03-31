package telegram

import (
	"strings"
	"testing"

	db "unitool/internal/db/generated"
)

func TestParsePackageInputImageVideoOnly(t *testing.T) {
	in, err := parsePackageInput("img_100|Фото M|699|RUB|100|0|true")
	if err != nil {
		t.Fatalf("parsePackageInput: %v", err)
	}
	if in.Code != "img_100" {
		t.Fatalf("unexpected code: %q", in.Code)
	}
	if in.AttemptsText != 0 {
		t.Fatalf("expected attempts_text=0, got %d", in.AttemptsText)
	}
	if in.AttemptsImage != 100 {
		t.Fatalf("unexpected attempts_image: %d", in.AttemptsImage)
	}
	if in.AttemptsVideo != 0 {
		t.Fatalf("unexpected attempts_video: %d", in.AttemptsVideo)
	}
	if !in.IsActive {
		t.Fatal("expected active=true")
	}
}

func TestParsePackageEditInputImageVideoOnly(t *testing.T) {
	id, in, err := parsePackageEditInput("42|mix_100_15|Комбо M|1690|RUB|100|15|1")
	if err != nil {
		t.Fatalf("parsePackageEditInput: %v", err)
	}
	if id != 42 {
		t.Fatalf("unexpected id: %d", id)
	}
	if in.Code != "mix_100_15" {
		t.Fatalf("unexpected code: %q", in.Code)
	}
	if in.AttemptsText != 0 {
		t.Fatalf("expected attempts_text=0, got %d", in.AttemptsText)
	}
	if in.AttemptsImage != 100 || in.AttemptsVideo != 15 {
		t.Fatalf("unexpected attempts image/video: %d/%d", in.AttemptsImage, in.AttemptsVideo)
	}
	if !in.IsActive {
		t.Fatal("expected active=true")
	}
}

func TestParsePackageInputRejectsLegacyTextFieldFormat(t *testing.T) {
	_, err := parsePackageInput("legacy|Пакет|499|RUB|10|20|30|true")
	if err == nil {
		t.Fatal("expected error for legacy format with text field")
	}
}

func TestFormatPackageOmitsTextCredits(t *testing.T) {
	got := formatPackage(db.Package{
		ID:           7,
		Code:         "mix_30_5",
		Title:        "Комбо S",
		PriceRub:     619,
		Currency:     "RUB",
		TextCredits:  999,
		ImageCredits: 30,
		VideoCredits: 5,
		IsActive:     true,
	})
	if strings.Contains(got, "text=") {
		t.Fatalf("formatPackage should not include text credits, got: %q", got)
	}
	if !strings.Contains(got, "image=30") || !strings.Contains(got, "video=5") {
		t.Fatalf("formatPackage should include image/video credits, got: %q", got)
	}
}
