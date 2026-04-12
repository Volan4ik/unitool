package payments

import (
	"encoding/json"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestIsBoostPackageCode(t *testing.T) {
	if !isBoostPackageCode("boost_10_2") {
		t.Fatal("expected boost package")
	}
	if !isBoostPackageCode("  BOOST_extra ") {
		t.Fatal("expected case-insensitive boost package")
	}
	if isBoostPackageCode("base_minimum") {
		t.Fatal("base package must not be boost")
	}
}

func TestBuildProviderDataReceipt(t *testing.T) {
	raw := buildProviderDataReceipt("Plan", 690, "user@example.com")
	var pd providerData
	if err := json.Unmarshal([]byte(raw), &pd); err != nil {
		t.Fatalf("failed to unmarshal provider data: %v", err)
	}
	if len(pd.Receipt.Items) != 1 {
		t.Fatalf("unexpected items count: %d", len(pd.Receipt.Items))
	}
	item := pd.Receipt.Items[0]
	if item.Description != "Plan" || item.Amount.Value != "690.00" || item.Amount.Currency != "RUB" {
		t.Fatalf("unexpected receipt item: %+v", item)
	}
	if pd.Receipt.Customer == nil || pd.Receipt.Customer.Email != "user@example.com" {
		t.Fatalf("unexpected customer: %+v", pd.Receipt.Customer)
	}

	rawNoEmail := buildProviderDataReceipt("Plan", 1490, "")
	pd = providerData{}
	if err := json.Unmarshal([]byte(rawNoEmail), &pd); err != nil {
		t.Fatalf("failed to unmarshal provider data: %v", err)
	}
	if pd.Receipt.Customer != nil {
		t.Fatalf("customer must be omitted when email is empty, got %+v", pd.Receipt.Customer)
	}
}

func TestBuyerEmailText(t *testing.T) {
	sp := &tgbotapi.SuccessfulPayment{
		OrderInfo: &tgbotapi.OrderInfo{Email: " user@example.com "},
	}
	email := buyerEmailText(sp)
	if !email.Valid || email.String != "user@example.com" {
		t.Fatalf("unexpected email text: %+v", email)
	}

	if got := buyerEmailText(&tgbotapi.SuccessfulPayment{}); got.Valid {
		t.Fatalf("expected invalid text for empty order info, got %+v", got)
	}
	if got := buyerEmailText(nil); got.Valid {
		t.Fatalf("expected invalid text for nil payment, got %+v", got)
	}
}

func TestBuyerEmailFromOrderInfo(t *testing.T) {
	info := &tgbotapi.OrderInfo{Email: " user2@example.com "}
	got := buyerEmailFromOrderInfo(info)
	if !got.Valid || got.String != "user2@example.com" {
		t.Fatalf("unexpected email text: %+v", got)
	}
	if got := buyerEmailFromOrderInfo(&tgbotapi.OrderInfo{}); got.Valid {
		t.Fatalf("expected invalid text for empty order info, got %+v", got)
	}
	if got := buyerEmailFromOrderInfo(nil); got.Valid {
		t.Fatalf("expected invalid text for nil order info, got %+v", got)
	}
}

func TestBoostRestrictionMessage(t *testing.T) {
	msg := boostRestrictionMessage()
	if msg == "" {
		t.Fatal("message must not be empty")
	}
}
