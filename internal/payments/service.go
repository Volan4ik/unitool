package payments

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
)

type Package struct {
	ID            int64
	Title         string
	PriceRub      int
	TextCredits   int
	ImageCredits  int
	VideoCredits  int
	SearchCredits int
}

type Service struct {
	Bot           *tgbotapi.BotAPI
	ProviderToken string
}

func NewService(bot *tgbotapi.BotAPI, providerToken string) *Service { return &Service{Bot: bot, ProviderToken: providerToken} }

func (s *Service) SendInvoiceForPackage(chatID int64, pkg Package, buyerEmail string) (string, error) {
	orderID := uuid.NewString()
	amountKopek := pkg.PriceRub * 100
	prices := []tgbotapi.LabeledPrice{{Label: pkg.Title, Amount: amountKopek}}

	inv := tgbotapi.NewInvoice(chatID, pkg.Title, "Пакет попыток для нейросетей", orderID, s.ProviderToken, "RUB", prices)
	inv.NeedEmail = true
	inv.SendEmailToProvider = true
	inv.ProviderData = buildProviderDataReceipt(pkg.Title, pkg.PriceRub, buyerEmail)

	msg, err := s.Bot.Send(inv)
	if err != nil { return "", err }
	_ = msg // TODO: сохранить order в БД (status=created)
	return orderID, nil
}

func (s *Service) HandlePreCheckout(pcq *tgbotapi.PreCheckoutQuery) {
	ok := true
	resp := tgbotapi.PreCheckoutConfig{PreCheckoutQueryID: pcq.ID, OK: ok}
	if _, err := s.Bot.Request(resp); err != nil { log.Println("answerPreCheckoutQuery error:", err) }
	if ok { /* TODO: mark order precheckout_ok */ }
}

func (s *Service) HandleSuccessfulPayment(msg *tgbotapi.Message) {
	sp := msg.SuccessfulPayment
	if sp == nil { return }
	orderID := msg.InvoicePayload
	amountRub := sp.TotalAmount / 100
	if sp.Currency != "RUB" { log.Printf("Unexpected currency %s", sp.Currency); return }
	// TODO: update order -> paid, save charge ids; начислить попытки
	confirm := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Оплата успешна ✅\nЗаказ: %s\nСумма: %d ₽", orderID, amountRub))
	s.Bot.Send(confirm)
}

type providerData struct { Receipt receipt `json:"receipt"` }

type receipt struct {
	Items    []receiptItem `json:"items"`
	Customer *customer     `json:"customer,omitempty"`
}

type customer struct { Email string `json:"email,omitempty"` }

type receiptItem struct {
	Description    string      `json:"description"`
	Quantity       string      `json:"quantity"`
	Amount         moneyAmount `json:"amount"`
	VatCode        int         `json:"vat_code"`
	PaymentSubject string      `json:"payment_subject,omitempty"`
	PaymentMode    string      `json:"payment_mode,omitempty"`
}

type moneyAmount struct { Value, Currency string }

func buildProviderDataReceipt(title string, priceRub int, email string) string {
	pd := providerData{ Receipt: receipt{ Items: []receiptItem{ { Description: title, Quantity: "1.00", Amount: moneyAmount{ Value: fmt.Sprintf("%.2f", float64(priceRub)), Currency: "RUB" }, VatCode: 1, PaymentSubject: "service", PaymentMode: "full_prepayment", }, }, }, }
	if email != "" { pd.Receipt.Customer = &customer{Email: email} }
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(pd)
	return buf.String()
}