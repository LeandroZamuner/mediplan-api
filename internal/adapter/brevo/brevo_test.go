package brevo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lea/mediplan-api/internal/usecase/accounts"
)

func TestSend(t *testing.T) {
	msg := accounts.Message{To: "ana@mail.com", Subject: "Hola", HTML: "<b>hola</b>", Text: "hola"}

	t.Run("dado un mensaje, cuando se envía, entonces llama a la API de Brevo con la clave, el remitente y el contenido", func(t *testing.T) {
		// Given
		var got map[string]any
		var key, path string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, path = r.Header.Get("api-key"), r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&got)
			w.WriteHeader(http.StatusCreated)
		}))
		defer srv.Close()
		c := New("clave-x", "mediplan@mail.com", "MediPlan")
		c.baseURL = srv.URL
		// When
		err := c.Send(context.Background(), msg)
		// Then
		if err != nil {
			t.Fatal(err)
		}
		if key != "clave-x" || path != "/v3/smtp/email" {
			t.Fatalf("key=%q path=%q", key, path)
		}
		sender := got["sender"].(map[string]any)
		to := got["to"].([]any)[0].(map[string]any)
		if sender["email"] != "mediplan@mail.com" || sender["name"] != "MediPlan" || to["email"] != "ana@mail.com" ||
			got["subject"] != "Hola" || got["htmlContent"] != "<b>hola</b>" || got["textContent"] != "hola" {
			t.Fatalf("cuerpo inesperado: %v", got)
		}
	})

	t.Run("dado que Brevo rechaza el envío, cuando se envía, entonces devuelve error sin filtrar la clave", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Key not found"}`))
		}))
		defer srv.Close()
		c := New("clave-secreta", "m@mail.com", "MediPlan")
		c.baseURL = srv.URL
		err := c.Send(context.Background(), msg)
		if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "clave-secreta") {
			t.Fatalf("error inesperado: %v", err)
		}
	})
}
