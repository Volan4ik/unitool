package yookassa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type Client struct {
	shopID string
	secret string
	http   *http.Client
}

func New(shopID, secret string) *Client {
	return &Client{shopID: shopID, secret: secret, http: &http.Client{Timeout: 15 * time.Second}}
}

type Amount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}
type Confirmation struct {
	Type      string `json:"type"`
	ReturnURL string `json:"return_url"`
}
type PaymentMethodData struct {
	Type string `json:"type"` // "sbp"
}
type CreatePaymentReq struct {
	Amount            Amount            `json:"amount"`
	Confirmation      Confirmation      `json:"confirmation"`
	Capture           bool              `json:"capture"`
	Description       string            `json:"description,omitempty"`
	PaymentMethodData PaymentMethodData `json:"payment_method_data"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}
type CreatePaymentResp struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Confirmation struct {
		Type string `json:"type"`
		URL  string `json:"confirmation_url"`
	} `json:"confirmation"`
}

func (c *Client) CreatePayment(ctx context.Context, in CreatePaymentReq) (*CreatePaymentResp, error) {
	js, _ := json.Marshal(in)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.yookassa.ru/v3/payments", bytes.NewReader(js))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotence-Key", uuid.New().String())
	req.SetBasicAuth(c.shopID, c.secret)

	resp, err := c.http.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 { return nil, fmt.Errorf("yookassa http %d", resp.StatusCode) }

	var out CreatePaymentResp
	return &out, json.NewDecoder(resp.Body).Decode(&out)
}
