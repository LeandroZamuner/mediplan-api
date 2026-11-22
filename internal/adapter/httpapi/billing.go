package httpapi

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lea/mediplan-api/internal/adapter/mercadopago"
	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/billing"
)

// BillingHandler expone el nivel (gratis o Premium) del usuario, sus topes y el pago de la suscripción.
type BillingHandler struct {
	svc *billing.Service
	pay *billing.Payments // nil = pagos no habilitados (todo el mundo es gratis)
	// webhookSecret firma los avisos de Mercado Pago; vacío = no se exige la firma (igual cada aviso se verifica consultando a MP).
	webhookSecret string
}

func NewBillingHandler(svc *billing.Service) *BillingHandler { return &BillingHandler{svc: svc} }

// WithPayments habilita el cobro con Mercado Pago.
func (h *BillingHandler) WithPayments(p *billing.Payments, webhookSecret string) *BillingHandler {
	h.pay, h.webhookSecret = p, webhookSecret
	return h
}

// limitOrNil: sin tope se informa null (más claro para los clientes que un número mágico).
func limitOrNil(n int) *int {
	if n == domain.Unlimited {
		return nil
	}
	return &n
}

// Subscription: GET /me/subscription
func (h *BillingHandler) Subscription(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	if h.pay != nil {
		h.pay.RefreshIfNeeded(r.Context(), u.ID) // por si el aviso del pago no llegó
	}
	h.writeStatus(w, r, u.ID)
}

func (h *BillingHandler) writeStatus(w http.ResponseWriter, r *http.Request, userID string) {
	st, err := h.svc.Status(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	payments := map[string]any{"enabled": h.pay != nil}
	if h.pay != nil {
		payments["priceArs"], payments["currency"], payments["period"] = h.pay.PriceARS(), "ARS", "month"
	}
	var end *time.Time
	if st.CurrentPeriodEnd != nil {
		e := st.CurrentPeriodEnd.UTC()
		end = &e
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tier":             st.Tier,
		"cancelled":        st.Cancelled, // Premium que ya no se renueva
		"pending":          st.Pending,   // empezó a pagar y todavía no se confirmó
		"payments":         payments,
		"currentPeriodEnd": end,
		"limits": map[string]*int{
			"maxActivePlans":       limitOrNil(st.Limits.MaxActivePlans),
			"maxMedications":       limitOrNil(st.Limits.MaxMedications),
			"maxCaregiversPerPlan": limitOrNil(st.Limits.MaxCaregiversPerPlan),
		},
		"usage": map[string]int{"activePlans": st.ActivePlans, "medications": st.Medications},
	})
}

// Checkout: POST /me/subscription/checkout {"payerEmail":"opcional"} -> {"checkoutUrl":"https://..."}
// Crea la suscripción pendiente en Mercado Pago. La cuenta sigue siendo gratis hasta que el pago se confirme.
func (h *BillingHandler) Checkout(w http.ResponseWriter, r *http.Request) {
	if h.pay == nil {
		writeError(w, domain.ErrPaymentsDisabled)
		return
	}
	u, _ := UserFromContext(r.Context())
	var body struct {
		PayerEmail string `json:"payerEmail"`
	}
	if r.ContentLength != 0 && !decode(w, r, &body) {
		return
	}
	email := strings.TrimSpace(body.PayerEmail)
	if email != "" && !strings.Contains(email, "@") {
		writeError(w, domain.ErrInvalidEmail)
		return
	}
	url, err := h.pay.Checkout(r.Context(), u, email)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"checkoutUrl": url})
}

// Sync: POST /me/subscription/sync. La app lo llama al volver de pagar: consulta a Mercado Pago y devuelve el estado.
func (h *BillingHandler) Sync(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	if h.pay != nil {
		if err := h.pay.Sync(r.Context(), u.ID); err != nil {
			writeError(w, err)
			return
		}
	}
	h.writeStatus(w, r, u.ID)
}

// Cancel: DELETE /me/subscription. Deja de renovarse; sigue Premium hasta el fin del período pagado.
func (h *BillingHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	if h.pay == nil {
		writeError(w, domain.ErrPaymentsDisabled)
		return
	}
	u, _ := UserFromContext(r.Context())
	if err := h.pay.Cancel(r.Context(), u.ID); err != nil {
		writeError(w, err)
		return
	}
	h.writeStatus(w, r, u.ID)
}

// MercadoPagoWebhook: POST /webhooks/mercadopago (público; lo llama Mercado Pago).
// Se comprueba la firma y, aunque sea válida, el estado real se consulta de nuevo a Mercado Pago.
func (h *BillingHandler) MercadoPagoWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type string `json:"type"`
		Data struct {
			ID any `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
	q := r.URL.Query()
	dataID := q.Get("data.id")
	if dataID == "" {
		switch v := body.Data.ID.(type) {
		case string:
			dataID = v
		case float64:
			dataID = strconv.FormatInt(int64(v), 10)
		}
	}
	if h.webhookSecret != "" && !mercadopago.VerifySignature(h.webhookSecret, r.Header.Get("x-signature"), r.Header.Get("x-request-id"), q.Get("data.id")) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "firma inválida"})
		return
	}
	topic := q.Get("type")
	if topic == "" {
		topic = body.Type
	}
	if err := h.pay.HandleNotification(r.Context(), topic, dataID); err != nil {
		log.Printf("pagos: aviso %s/%s no procesado: %v", topic, dataID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reintentar"})
		return
	}
	w.WriteHeader(http.StatusOK)
}
