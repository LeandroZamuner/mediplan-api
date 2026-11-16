// Package brevo envía correos con la API HTTPS de Brevo (plan gratis: 300 por día).
// Se usa HTTPS y no SMTP porque Render bloquea el SMTP saliente en su plan gratuito.
package brevo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lea/mediplan-api/internal/usecase/accounts"
)

type Client struct {
	apiKey, from, fromName string
	baseURL                string
	http                   *http.Client
}

func New(apiKey, from, fromName string) *Client {
	return &Client{apiKey: apiKey, from: from, fromName: fromName, baseURL: "https://api.brevo.com", http: &http.Client{Timeout: 15 * time.Second}}
}

type address struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

func (c *Client) Send(ctx context.Context, m accounts.Message) error {
	body, _ := json.Marshal(map[string]any{
		"sender":      address{Email: c.from, Name: c.fromName},
		"to":          []address{{Email: m.To}},
		"subject":     m.Subject,
		"htmlContent": m.HTML,
		"textContent": m.Text,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v3/smtp/email", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("brevo: %w", redact(err, c.apiKey))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("brevo: respondió %d: %s", resp.StatusCode, detail)
	}
	return nil
}

// redact evita que la clave llegue a los logs si alguna vez aparece en un mensaje de error.
func redact(err error, secret string) error {
	if secret == "" {
		return err
	}
	msg := err.Error()
	for i := 0; i+len(secret) <= len(msg); i++ {
		if msg[i:i+len(secret)] == secret {
			return fmt.Errorf("%s", msg[:i]+"***"+msg[i+len(secret):])
		}
	}
	return err
}
