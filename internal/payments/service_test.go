package payments

import (
	"encoding/json"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestBuildProviderDataReceiptWithEmail(t *testing.T) {
	raw := buildProviderDataReceipt("Пакет PRO", 1490, "user@example.com")

	var out providerData
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal receipt: %v", err)
	}
	if len(out.Receipt.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(out.Receipt.Items))
	}
	it := out.Receipt.Items[0]
	if it.Description != "Пакет PRO" {
		t.Fatalf("unexpected description: %q", it.Description)
	}
	if it.Amount.Value != "1490.00" {
		t.Fatalf("unexpected amount value: %q", it.Amount.Value)
	}
	if it.Amount.Currency != "RUB" {
		t.Fatalf("unexpected currency: %q", it.Amount.Currency)
	}
	if out.Receipt.Customer == nil || out.Receipt.Customer.Email != "user@example.com" {
		t.Fatalf("expected customer email in receipt")
	}
}

func TestBuildProviderDataReceiptWithoutEmail(t *testing.T) {
	raw := buildProviderDataReceipt("Starter", 199, "")

	var out providerData
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal receipt: %v", err)
	}
	if out.Receipt.Customer != nil {
		t.Fatal("expected customer to be omitted when email is empty")
	}
}

func TestHandlePreCheckoutNilNoPanic(t *testing.T) {
	svc := &Service{}
	svc.HandlePreCheckout(nil, nil)
}

func TestValidatePreCheckoutEmptyPayload(t *testing.T) {
	svc := &Service{}
	ok, msg, err := svc.validatePreCheckout(nil, &tgbotapi.PreCheckoutQuery{InvoicePayload: ""})
	if ok {
		t.Fatal("expected pre-checkout validation to fail on empty payload")
	}
	if msg == "" {
		t.Fatal("expected error message")
	}
	if err != nil {
		t.Fatalf("expected nil infra error, got %v", err)
	}
}

func TestValidatePreCheckoutInvalidPayload(t *testing.T) {
	svc := &Service{}
	ok, msg, err := svc.validatePreCheckout(nil, &tgbotapi.PreCheckoutQuery{InvoicePayload: "not-a-uuid"})
	if ok {
		t.Fatal("expected pre-checkout validation to fail on invalid uuid payload")
	}
	if msg == "" {
		t.Fatal("expected error message")
	}
	if err != nil {
		t.Fatalf("expected nil infra error, got %v", err)
	}
}
