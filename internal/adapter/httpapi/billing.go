package httpapi

import (
	"net/http"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/billing"
)

// BillingHandler expone el nivel (gratis o Premium) del usuario y sus topes.
type BillingHandler struct{ svc *billing.Service }

func NewBillingHandler(svc *billing.Service) *BillingHandler { return &BillingHandler{svc: svc} }

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
	st, err := h.svc.Status(r.Context(), u.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	var end *time.Time
	if st.CurrentPeriodEnd != nil {
		e := st.CurrentPeriodEnd.UTC()
		end = &e
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tier":             st.Tier,
		"cancelled":        st.Cancelled, // Premium que ya no se renueva
		"currentPeriodEnd": end,
		"limits": map[string]*int{
			"maxActivePlans":       limitOrNil(st.Limits.MaxActivePlans),
			"maxMedications":       limitOrNil(st.Limits.MaxMedications),
			"maxCaregiversPerPlan": limitOrNil(st.Limits.MaxCaregiversPerPlan),
		},
		"usage": map[string]int{"activePlans": st.ActivePlans, "medications": st.Medications},
	})
}
