package mercadopago

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/billing"
)

func sign(secret, manifest string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(manifest))
	return hex.EncodeToString(m.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	secret := "clave"
	good := "ts=1700000000,v1=" + sign(secret, "id:abc123;request-id:req-1;ts:1700000000;")
	cases := []struct {
		name               string
		secret, sig, reqID string
		dataID             string
		want               bool
	}{
		{"con firma válida, entonces se acepta", secret, good, "req-1", "abc123", true},
		{"con el id en mayúsculas, entonces se compara en minúsculas y se acepta", secret, good, "req-1", "ABC123", true},
		{"con otro id, entonces se rechaza", secret, good, "req-1", "otro", false},
		{"con otro request-id, entonces se rechaza", secret, good, "req-2", "abc123", false},
		{"con otra clave, entonces se rechaza", "otra", good, "req-1", "abc123", false},
		{"sin firma, entonces se rechaza", secret, "", "req-1", "abc123", false},
		{"con firma mal formada, entonces se rechaza", secret, "ts=1,v1=zzz", "req-1", "abc123", false},
		{"sin clave configurada, entonces se rechaza", "", good, "req-1", "abc123", false},
	}
	for _, c := range cases {
		t.Run("dado un aviso "+c.name, func(t *testing.T) {
			if got := VerifySignature(c.secret, c.sig, c.reqID, c.dataID); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestClient(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch {
		case r.Method == "POST" && r.URL.Path == "/preapproval":
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id":"pre1","status":"pending","init_point":"https://mp/pagar","external_reference":"u1","auto_recurring":{"transaction_amount":2999}}`))
		case r.Method == "GET" && r.URL.Path == "/preapproval/pre1":
			_, _ = w.Write([]byte(`{"id":"pre1","status":"authorized","external_reference":"u1","next_payment_date":"2026-12-22T10:00:00.000-03:00","auto_recurring":{"transaction_amount":2999}}`))
		case r.Method == "GET" && r.URL.Path == "/authorized_payments/pay1":
			_, _ = w.Write([]byte(`{"id":1,"preapproval_id":"pre1"}`))
		case r.Method == "PUT" && r.URL.Path == "/preapproval/pre1":
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/preapproval/boom":
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"message":"falla"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := New("TOKEN-SECRETO")
	c.baseURL = srv.URL
	ctx := context.Background()

	t.Run("dados los datos del pago, cuando se crea la suscripción, entonces manda mensual en pesos, pendiente y devuelve el enlace", func(t *testing.T) {
		pa, err := c.Create(ctx, billing.CreateParams{Reason: "MediPlan Premium", ExternalReference: "u1", PayerEmail: "a@b.com", AmountARS: 2999, BackURL: "https://web/planes/", NotificationURL: "https://api/hook"})
		if err != nil || pa.ID != "pre1" || pa.InitPoint != "https://mp/pagar" || pa.Status != "pending" {
			t.Fatalf("%+v %v", pa, err)
		}
		ar := gotBody["auto_recurring"].(map[string]any)
		if gotAuth != "Bearer TOKEN-SECRETO" || gotBody["status"] != "pending" || gotBody["external_reference"] != "u1" || gotBody["payer_email"] != "a@b.com" ||
			ar["currency_id"] != "ARS" || ar["frequency_type"] != "months" || ar["transaction_amount"] != float64(2999) || gotBody["notification_url"] != "https://api/hook" {
			t.Fatalf("pedido inesperado: %v", gotBody)
		}
	})
	t.Run("dada una suscripción autorizada, cuando se consulta, entonces trae la próxima fecha de cobro en UTC", func(t *testing.T) {
		pa, err := c.Get(ctx, "pre1")
		if err != nil || pa.Status != "authorized" || pa.NextPaymentDate == nil || pa.NextPaymentDate.Format("2006-01-02T15:04") != "2026-12-22T13:00" {
			t.Fatalf("%+v %v", pa, err)
		}
	})
	t.Run("dado un cobro autorizado, cuando se pide su suscripción, entonces devuelve el id", func(t *testing.T) {
		if id, err := c.AuthorizedPaymentSubscription(ctx, "pay1"); err != nil || id != "pre1" {
			t.Fatalf("%q %v", id, err)
		}
	})
	t.Run("dada una baja, cuando se cancela, entonces manda el estado cancelled", func(t *testing.T) {
		if err := c.Cancel(ctx, "pre1"); err != nil || gotBody["status"] != "cancelled" {
			t.Fatalf("%v %v", err, gotBody)
		}
	})
	t.Run("dada una suscripción inexistente, cuando se consulta, entonces es no encontrada", func(t *testing.T) {
		if _, err := c.Get(ctx, "nada"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("dada una falla de Mercado Pago, cuando se consulta, entonces el error es 'no disponible' y no filtra la clave", func(t *testing.T) {
		_, err := c.Get(ctx, "boom")
		if !errors.Is(err, domain.ErrPaymentUnavailable) || strings.Contains(err.Error(), "TOKEN-SECRETO") {
			t.Fatalf("%v", err)
		}
	})
}
