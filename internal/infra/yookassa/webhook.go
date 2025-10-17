package yookassa

type WebhookEvent struct {
	Type  string        `json:"type"`  // "notification"
	Event string        `json:"event"` // "payment.succeeded" | ...
	Object PaymentObject `json:"object"`
}
type PaymentObject struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Paid   bool   `json:"paid"`
	Amount Amount `json:"amount"`
}
