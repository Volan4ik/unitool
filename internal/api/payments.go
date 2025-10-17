package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/yourorg/ai-telebot/internal/infra/yookassa"
)

type paymentsUC interface{
	CreateSBPPayment(rCtx interface{}, userID int64, product string, amountCents int64) (int64, string, error)
	HandleWebhookSucceeded(rCtx interface{}, providerPaymentID string, paidAt time.Time) error
}
type PaymentsHandler struct{ uc paymentsUC }

func NewPaymentsHandler(uc paymentsUC) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/payments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { w.WriteHeader(405); return }
		var in struct{ Product string; AmountCents int64 }
		_ = json.NewDecoder(r.Body).Decode(&in)
		userID := r.Context().Value("userID").(int64) // подставь свою аутентификацию/админку
		id, url, err := uc.CreateSBPPayment(r.Context(), userID, in.Product, in.AmountCents)
		if err != nil { http.Error(w, "cannot create payment", 500); return }
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "pay_url": url})
	})
	mux.HandleFunc("/payments/webhook", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { w.WriteHeader(405); return }
		var ev yookassa.WebhookEvent
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil { w.WriteHeader(400); return }
		if ev.Event == "payment.succeeded" && ev.Object.ID != "" {
			_ = apiAuthCheckBasic(w, r) // при необходимости проверяй Basic-Auth
			_ = (func() error {
				return HandlerHandleSucceeded(r.Context(), ev, /* uc */)
			})()
		}
		w.WriteHeader(200)
	})
	return mux
}

func HandlerHandleSucceeded(ctx interface{}, ev yookassa.WebhookEvent, uc paymentsUC) error {
	return uc.HandleWebhookSucceeded(ctx, ev.Object.ID, time.Now())
}