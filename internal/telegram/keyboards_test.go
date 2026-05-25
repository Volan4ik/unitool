package telegram

import "testing"

func TestWelcomeInlineKeyboardIncludesTrendPhoto(t *testing.T) {
	kb := WelcomeInlineKeyboard()
	if len(kb.InlineKeyboard) == 0 || len(kb.InlineKeyboard[0]) != 1 {
		t.Fatalf("expected trend photo button in first row, got %#v", kb.InlineKeyboard)
	}
	btn := kb.InlineKeyboard[0][0]
	if btn.Text != "СГЕНЕРИРОВАТЬ ТРЕНДОВОЕ ФОТО" || btn.CallbackData == nil || *btn.CallbackData != "start:trend_photo" {
		t.Fatalf("unexpected first button: %#v", btn)
	}
}

func TestModelsInlineKeyboard(t *testing.T) {
	kb := ModelsInlineKeyboard("image", "Nano Banana")
	if len(kb.InlineKeyboard) < 2 {
		t.Fatalf("expected mode row + model rows, got %d rows", len(kb.InlineKeyboard))
	}

	modeRow := kb.InlineKeyboard[0]
	if len(modeRow) != 2 {
		t.Fatalf("expected 2 mode buttons, got %d", len(modeRow))
	}

	foundSelected := false
	for _, row := range kb.InlineKeyboard[1:] {
		for _, btn := range row {
			if btn.Text == "✅ Nano Banana" {
				foundSelected = true
			}
		}
	}
	if !foundSelected {
		t.Fatal("selected model mark not found")
	}
}

func TestPackagesInlineKeyboard(t *testing.T) {
	kb := PackagesInlineKeyboard([]PackageButton{
		{Code: "base_minimum", Label: "Base"},
		{Code: "boost_10_2", Label: ""},
	})
	if len(kb.InlineKeyboard) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(kb.InlineKeyboard))
	}

	if kb.InlineKeyboard[0][0].Text != "Base" {
		t.Fatalf("unexpected label: %q", kb.InlineKeyboard[0][0].Text)
	}
	if kb.InlineKeyboard[1][0].Text != "boost_10_2" {
		t.Fatalf("fallback label must use code, got %q", kb.InlineKeyboard[1][0].Text)
	}
}

func TestCancelPaymentInlineKeyboard(t *testing.T) {
	kb := CancelPaymentInlineKeyboard()
	if len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 1 {
		t.Fatalf("expected one cancel button, got %#v", kb.InlineKeyboard)
	}
	btn := kb.InlineKeyboard[0][0]
	if btn.Text != "Отменить платёж" || btn.CallbackData == nil || *btn.CallbackData != "buy:cancel_sbp" {
		t.Fatalf("unexpected cancel button: %#v", btn)
	}
}
