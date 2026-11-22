package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lea/mediplan-api/internal/usecase/ai"
)

func TestChatWithTools(t *testing.T) {
	var bodies []map[string]any
	var key, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, path = r.Header.Get("x-goog-api-key"), r.URL.Path
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		if len(bodies) == 1 {
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"get_adherence","args":{"days":7}},"thoughtSignature":"firma"}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"Vas muy bien: 80%."}]}}]}`))
	}))
	defer srv.Close()
	c := New("CLAVE-SECRETA", "gemini-test")
	c.baseURL = srv.URL
	var gotArgs map[string]any
	tools := []ai.Tool{{Name: "get_adherence", Description: "cumplimiento", Run: func(_ context.Context, a map[string]any) (any, error) {
		gotArgs = a
		return map[string]any{"adherencePercent": 80}, nil
	}}}

	t.Run("dado un modelo que pide una herramienta, cuando se chatea, entonces se ejecuta, se le devuelve el resultado y se obtiene el texto final", func(t *testing.T) {
		got, err := c.Chat(context.Background(), "sistema", []ai.Msg{{Role: "user", Text: "¿cómo vengo?"}}, tools)
		if err != nil || got != "Vas muy bien: 80%." {
			t.Fatalf("%q %v", got, err)
		}
		if key != "CLAVE-SECRETA" || path != "/v1beta/models/gemini-test:generateContent" || gotArgs["days"] != float64(7) || len(bodies) != 2 {
			t.Fatalf("key=%s path=%s args=%v n=%d", key, path, gotArgs, len(bodies))
		}
		first := bodies[0]
		if first["systemInstruction"] == nil || first["tools"] == nil {
			t.Fatalf("falta el sistema o las herramientas: %v", first)
		}
		raw, _ := json.Marshal(bodies[1]["contents"])
		s := string(raw)
		// el turno del modelo vuelve intacto (con su firma) y el resultado va como functionResponse con el mismo id
		if !strings.Contains(s, `"thoughtSignature":"firma"`) || !strings.Contains(s, `"functionResponse":{"id":"c1","name":"get_adherence","response":{"result":{"adherencePercent":80}}}`) {
			t.Fatalf("contenido inesperado: %s", s)
		}
	})
	t.Run("dado un error de la API, cuando se chatea, entonces falla sin filtrar la clave", func(t *testing.T) {
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":"cuota"}`))
		}))
		defer bad.Close()
		c2 := New("CLAVE-SECRETA", "m")
		c2.baseURL = bad.URL
		_, err := c2.Chat(context.Background(), "", []ai.Msg{{Role: "user", Text: "hola"}}, nil)
		if err == nil || !strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "CLAVE-SECRETA") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("dada una respuesta vacía, cuando se chatea, entonces es un error", func(t *testing.T) {
		empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[]}}]}`))
		}))
		defer empty.Close()
		c3 := New("k", "m")
		c3.baseURL = empty.URL
		if _, err := c3.Chat(context.Background(), "", []ai.Msg{{Role: "user", Text: "hola"}}, nil); err == nil {
			t.Fatal("esperaba error")
		}
	})
}
