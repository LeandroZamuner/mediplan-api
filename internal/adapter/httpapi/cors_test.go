package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// givenCORSRouter: API con CORS habilitado solo para el sitio de GitHub Pages y para localhost.
func givenCORSRouter(origins ...string) http.Handler {
	return NewRouter(Handlers{Auth: NewAuthHandler(nil), AllowedOrigins: origins})
}

func request(h http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

const pages = "https://leandro.github.io"

func TestCORS(t *testing.T) {
	t.Run("dado un origen permitido, cuando hace una consulta normal, entonces la respuesta lo autoriza y avisa que varía por origen", func(t *testing.T) {
		h := givenCORSRouter(pages, "http://localhost:3000")
		w := request(h, "GET", "/healthz", map[string]string{"Origin": pages})
		if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != pages || w.Header().Get("Vary") != "Origin" {
			t.Fatalf("code=%d headers=%v", w.Code, w.Header())
		}
	})

	t.Run("dado un origen que no está en la lista, cuando hace una consulta, entonces no se le agrega ninguna autorización", func(t *testing.T) {
		h := givenCORSRouter(pages)
		w := request(h, "GET", "/healthz", map[string]string{"Origin": "https://sitio-malicioso.example"})
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("no debería autorizar: %v", w.Header())
		}
	})

	t.Run("dado un origen permitido, cuando el navegador pregunta antes de enviar (preflight), entonces responde 204 con métodos y cabeceras y no llega a ninguna ruta", func(t *testing.T) {
		h := givenCORSRouter(pages)
		w := request(h, "OPTIONS", "/api/v1/plans", map[string]string{
			"Origin":                         pages,
			"Access-Control-Request-Method":  "POST",
			"Access-Control-Request-Headers": "authorization,content-type",
		})
		if w.Code != http.StatusNoContent {
			t.Fatalf("esperaba 204, got %d", w.Code)
		}
		hd := w.Header()
		if hd.Get("Access-Control-Allow-Origin") != pages || hd.Get("Access-Control-Allow-Methods") == "" ||
			hd.Get("Access-Control-Allow-Headers") != "Authorization, Content-Type" || hd.Get("Access-Control-Max-Age") == "" {
			t.Fatalf("cabeceras incompletas: %v", hd)
		}
	})

	t.Run("dado un origen NO permitido, cuando hace un preflight, entonces se rechaza con 403", func(t *testing.T) {
		h := givenCORSRouter(pages)
		w := request(h, "OPTIONS", "/api/v1/plans", map[string]string{"Origin": "https://sitio-malicioso.example", "Access-Control-Request-Method": "POST"})
		if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("code=%d headers=%v", w.Code, w.Header())
		}
	})

	t.Run("dada una consulta sin cabecera Origin (app móvil, Cloud Scheduler, curl), cuando llega, entonces pasa sin cambios", func(t *testing.T) {
		h := givenCORSRouter(pages)
		w := request(h, "GET", "/healthz", nil)
		if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Vary") != "" {
			t.Fatalf("code=%d headers=%v", w.Code, w.Header())
		}
	})

	t.Run("dada la lista de orígenes vacía, cuando llega una consulta con Origin, entonces CORS queda apagado", func(t *testing.T) {
		h := givenCORSRouter()
		w := request(h, "GET", "/healthz", map[string]string{"Origin": pages})
		if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("code=%d headers=%v", w.Code, w.Header())
		}
	})

	t.Run("dado un origen configurado con barra final o espacios, cuando llega el mismo origen sin barra, entonces coincide", func(t *testing.T) {
		h := givenCORSRouter("  "+pages+"/ ", "")
		w := request(h, "GET", "/healthz", map[string]string{"Origin": pages})
		if w.Header().Get("Access-Control-Allow-Origin") != pages {
			t.Fatalf("no coincidió: %v", w.Header())
		}
	})

	t.Run("dado que se comparan los orígenes de forma exacta, cuando llega uno parecido (otro subdominio o puerto), entonces no se autoriza", func(t *testing.T) {
		h := givenCORSRouter(pages)
		for _, o := range []string{"https://leandro.github.io.evil.example", "http://leandro.github.io", "https://otro.github.io", "https://leandro.github.io:8443"} {
			if w := request(h, "GET", "/healthz", map[string]string{"Origin": o}); w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("%s no debería autorizarse", o)
			}
		}
	})
}
