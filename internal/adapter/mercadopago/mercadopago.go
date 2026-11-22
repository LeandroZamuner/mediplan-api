// Package mercadopago es el cliente de la API de suscripciones de Mercado Pago (Argentina, ARS).
package mercadopago

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/billing"
)

type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

// New recibe el Access Token de PRODUCCIÓN de la aplicación (secreto: solo en variables del hosting).
func New(accessToken string) *Client {
	return &Client{token: accessToken, baseURL: "https://api.mercadopago.com", http: &http.Client{Timeout: 15 * time.Second}}
}

type preapprovalJSON struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	ExternalReference string `json:"external_reference"`
	InitPoint         string `json:"init_point"`
	NextPaymentDate   string `json:"next_payment_date"`
	AutoRecurring     struct {
		TransactionAmount float64 `json:"transaction_amount"`
	} `json:"auto_recurring"`
}

func (j preapprovalJSON) toDomain() *billing.Preapproval {
	pa := &billing.Preapproval{ID: j.ID, Status: j.Status, ExternalReference: j.ExternalReference, InitPoint: j.InitPoint, AmountARS: j.AutoRecurring.TransactionAmount}
	if j.NextPaymentDate != "" {
		if t, err := time.Parse(time.RFC3339Nano, j.NextPaymentDate); err == nil {
			t = t.UTC()
			pa.NextPaymentDate = &t
		} else if t, err := time.Parse("2006-01-02T15:04:05.000Z0700", j.NextPaymentDate); err == nil {
			t = t.UTC()
			pa.NextPaymentDate = &t
		}
	}
	return pa
}

func (c *Client) Create(ctx context.Context, p billing.CreateParams) (*billing.Preapproval, error) {
	body := map[string]any{
		"reason":             p.Reason,
		"external_reference": p.ExternalReference,
		"payer_email":        p.PayerEmail,
		"back_url":           p.BackURL,
		"status":             "pending",
		"auto_recurring": map[string]any{
			"frequency":          1,
			"frequency_type":     "months",
			"transaction_amount": p.AmountARS,
			"currency_id":        "ARS",
		},
	}
	if p.NotificationURL != "" {
		body["notification_url"] = p.NotificationURL
	}
	var out preapprovalJSON
	if err := c.do(ctx, http.MethodPost, "/preapproval", body, &out); err != nil {
		return nil, err
	}
	return out.toDomain(), nil
}

func (c *Client) Get(ctx context.Context, id string) (*billing.Preapproval, error) {
	var out preapprovalJSON
	if err := c.do(ctx, http.MethodGet, "/preapproval/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return out.toDomain(), nil
}

func (c *Client) AuthorizedPaymentSubscription(ctx context.Context, paymentID string) (string, error) {
	var out struct {
		PreapprovalID string `json:"preapproval_id"`
	}
	if err := c.do(ctx, http.MethodGet, "/authorized_payments/"+url.PathEscape(paymentID), nil, &out); err != nil {
		return "", err
	}
	if out.PreapprovalID == "" {
		return "", domain.ErrNotFound
	}
	return out.PreapprovalID, nil
}

func (c *Client) Cancel(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPut, "/preapproval/"+url.PathEscape(id), map[string]string{"status": "cancelled"}, nil)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var rd io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: mercadopago: %s", domain.ErrPaymentUnavailable, strings.ReplaceAll(err.Error(), c.token, "***"))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return domain.ErrNotFound
	case resp.StatusCode/100 != 2:
		detail := string(raw)
		if len(detail) > 300 {
			detail = detail[:300]
		}
		return fmt.Errorf("%w: mercadopago respondió %d: %s", domain.ErrPaymentUnavailable, resp.StatusCode, detail)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%w: respuesta ilegible: %v", domain.ErrPaymentUnavailable, err)
		}
	}
	return nil
}

// VerifySignature comprueba el encabezado x-signature ("ts=…,v1=…") de un aviso: HMAC-SHA256 con la clave secreta
// del webhook sobre "id:<data.id en minúsculas>;request-id:<x-request-id>;ts:<ts>;".
func VerifySignature(secret, xSignature, xRequestID, dataID string) bool {
	var ts, v1 string
	for _, part := range strings.Split(xSignature, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "ts":
			ts = v
		case "v1":
			v1 = v
		}
	}
	if secret == "" || ts == "" || v1 == "" {
		return false
	}
	want, err := hex.DecodeString(v1)
	if err != nil {
		return false
	}
	var manifest strings.Builder
	if dataID != "" {
		manifest.WriteString("id:" + strings.ToLower(dataID) + ";")
	}
	if xRequestID != "" {
		manifest.WriteString("request-id:" + xRequestID + ";")
	}
	manifest.WriteString("ts:" + ts + ";")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(manifest.String()))
	return hmac.Equal(mac.Sum(nil), want)
}
