package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"unitool/internal/infra/yookassa"
)

type paymentsUC interface {
	CreateCheckout(ctx context.Context, tgUserID int64, packageCode string) (uuid.UUID, string, error)
	HandleWebhookSucceeded(ctx context.Context, providerPaymentID string, paidAt time.Time) error
}
type PaymentsHandler struct{ uc paymentsUC }

func NewPaymentsHandler(uc paymentsUC) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/payments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var in struct {
			TgUserID    int64  `json:"tg_user_id"`
			PackageCode string `json:"package_code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			w.WriteHeader(400)
			return
		}
		if in.TgUserID == 0 || in.PackageCode == "" {
			w.WriteHeader(400)
			return
		}
		orderID, url, err := uc.CreateCheckout(r.Context(), in.TgUserID, in.PackageCode)
		if err != nil {
			http.Error(w, "cannot create payment", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"order_id": orderID.String(), "pay_url": url})
	})
	mux.HandleFunc("/payments/webhook", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var ev yookassa.WebhookEvent
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			w.WriteHeader(400)
			return
		}
		if ev.Event == "payment.succeeded" && ev.Object.ID != "" {
			_ = uc.HandleWebhookSucceeded(r.Context(), ev.Object.ID, time.Now())
		}
		w.WriteHeader(200)
	})
	return mux
}
