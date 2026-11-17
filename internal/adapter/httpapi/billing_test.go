package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/auth"
	"github.com/lea/mediplan-api/internal/usecase/billing"
)

type subRepo map[string]*domain.Subscription

func (r subRepo) ByUser(_ context.Context, id string) (*domain.Subscription, error) {
	if s, ok := r[id]; ok {
		return s, nil
	}
	return nil, domain.ErrNotFound
}

type usage struct{}

func (usage) Usage(context.Context, string) (int, int, error) { return 1, 2, nil }

func billingRouter(subs subRepo) http.Handler {
	v := verifier{"ana": {UID: "ana", Email: "ana@mail.com", EmailVerified: true}}
	users := repo{"ana": {ID: "u-ana", FirebaseUID: "ana", Email: "ana@mail.com"}}
	return NewRouter(Handlers{Auth: NewAuthHandler(auth.NewService(v, users)), Billing: NewBillingHandler(billing.NewService(subs, usage{}))})
}

func TestHTTPBilling(t *testing.T) {
	t.Run("dado un usuario gratis, cuando pide su suscripción, entonces ve nivel, topes y uso", func(t *testing.T) {
		w := do(billingRouter(subRepo{}), "GET", "/api/v1/me/subscription", "ana", "")
		var got struct {
			Tier   string `json:"tier"`
			Limits struct {
				MaxActivePlans *int `json:"maxActivePlans"`
				MaxMedications *int `json:"maxMedications"`
			} `json:"limits"`
			Usage struct {
				ActivePlans int `json:"activePlans"`
				Medications int `json:"medications"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
			t.Fatalf("code=%d body=%s", w.Code, w.Body)
		}
		if got.Tier != "free" || got.Limits.MaxActivePlans == nil || *got.Limits.MaxActivePlans != 1 || *got.Limits.MaxMedications != 3 || got.Usage.ActivePlans != 1 || got.Usage.Medications != 2 {
			t.Fatalf("respuesta inesperada: %s", w.Body)
		}
	})
	t.Run("dado un usuario Premium, cuando pide su suscripción, entonces los topes vienen en null (sin tope) y trae la fecha de fin", func(t *testing.T) {
		end := time.Now().Add(10 * 24 * time.Hour).UTC()
		w := do(billingRouter(subRepo{"u-ana": {UserID: "u-ana", Tier: domain.TierPremium, Status: domain.SubActive, CurrentPeriodEnd: &end}}), "GET", "/api/v1/me/subscription", "ana", "")
		var raw map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &raw)
		limits := raw["limits"].(map[string]any)
		if raw["tier"] != "premium" || limits["maxActivePlans"] != nil || limits["maxMedications"] != nil || raw["currentPeriodEnd"] == nil || raw["cancelled"] != false {
			t.Fatalf("respuesta inesperada: %s", w.Body)
		}
	})
	t.Run("dado que no hay sesión, cuando pide su suscripción, entonces responde 401", func(t *testing.T) {
		if w := do(billingRouter(subRepo{}), "GET", "/api/v1/me/subscription", "", ""); w.Code != 401 {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

func TestPlanLimitErrorResponse(t *testing.T) {
	t.Run("dado un error de tope del plan gratuito, cuando se responde, entonces es 402 con el recurso y el tope para que la app muestre Premium", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeError(rec, &domain.PlanLimitError{Resource: domain.ResourceMedications, Limit: 3})
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != 402 || body["code"] != "plan_limit" || body["resource"] != "medications" || body["limit"] != float64(3) || body["error"] == "" {
			t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
		}
	})
}
