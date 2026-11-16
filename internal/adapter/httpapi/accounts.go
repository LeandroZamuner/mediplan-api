package httpapi

import (
	"net"
	"net/http"

	"github.com/lea/mediplan-api/internal/usecase/accounts"
)

// AccountsHandler expone los correos de cuenta con el diseño de MediPlan.
type AccountsHandler struct{ svc *accounts.Service }

func NewAccountsHandler(svc *accounts.Service) *AccountsHandler { return &AccountsHandler{svc: svc} }

// PasswordReset: POST /auth/password-reset {"email":"..."}. Público; responde 202 exista o no la cuenta.
func (h *AccountsHandler) PasswordReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := h.svc.SendPasswordReset(r.Context(), body.Email, clientIP(r)); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// EmailVerification: POST /auth/email-verification con el ID token (aunque el correo aún no esté verificado).
func (h *AccountsHandler) EmailVerification(w http.ResponseWriter, r *http.Request) {
	sent, err := h.svc.SendVerification(r.Context(), bearer(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": sent, "alreadyVerified": !sent})
}

// clientIP toma la IP de quien llama (middleware.RealIP ya la dejó en RemoteAddr detrás del proxy de Render).
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
