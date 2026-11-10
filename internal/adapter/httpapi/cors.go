package httpapi

import (
	"net/http"
	"strings"
)

// cors deja que el navegador llame a la API desde la web (que vive en otro dominio, por
// ejemplo GitHub Pages). Solo autoriza los orígenes de la lista, comparados de forma EXACTA
// (esquema + dominio + puerto): nunca "*". La API usa el token en la cabecera Authorization,
// no cookies, así que no hace falta Allow-Credentials.
//
// Las consultas sin cabecera Origin (app móvil, Cloud Scheduler, curl) pasan sin cambios.
// Con la lista vacía, CORS queda apagado.
func cors(allowed []string) func(http.Handler) http.Handler {
	set := map[string]bool{}
	for _, o := range allowed {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			set[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		if len(set) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Add("Vary", "Origin")
			ok := set[origin]
			preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""

			switch {
			case ok && preflight:
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
			case !ok && preflight:
				http.Error(w, "origen no permitido", http.StatusForbidden)
			default:
				if ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
				}
				next.ServeHTTP(w, r)
			}
		})
	}
}
