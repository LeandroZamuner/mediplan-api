package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/auth"
)

type ctxKey struct{}

// UserFromContext devuelve el usuario autenticado por el middleware.
func UserFromContext(ctx context.Context) (*domain.User, bool) {
	u, ok := ctx.Value(ctxKey{}).(*domain.User)
	return u, ok
}

type AuthHandler struct{ svc *auth.Service }

func NewAuthHandler(svc *auth.Service) *AuthHandler { return &AuthHandler{svc: svc} }

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// Require exige un token válido de un usuario ya registrado.
func (h *AuthHandler) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := h.svc.Authenticate(r.Context(), bearer(r))
		if err != nil {
			writeError(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

// Register: POST /auth/register {"role":"patient|caregiver"} con el ID token de Firebase.
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role domain.Role `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "json inválido"})
		return
	}
	u, err := h.svc.Register(r.Context(), bearer(r), body.Role)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userDTO(u))
}

// Me: GET /me
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	writeJSON(w, http.StatusOK, userDTO(u))
}

// UpdateMe: PATCH /me {"timezone":"America/Montevideo"}
func (h *AuthHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	var body struct {
		Timezone string `json:"timezone"`
	}
	if !decode(w, r, &body) {
		return
	}
	u, err := h.svc.SetTimezone(r.Context(), actor, body.Timezone)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userDTO(u))
}

func userDTO(u *domain.User) map[string]any {
	return map[string]any{"id": u.ID, "email": u.Email, "role": u.Role, "emailVerified": u.EmailVerified, "timezone": u.Timezone}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	status, msg := http.StatusInternalServerError, "error interno"
	switch {
	case errors.Is(err, domain.ErrInvalidToken):
		status, msg = http.StatusUnauthorized, err.Error()
	case errors.Is(err, domain.ErrEmailNotVerified):
		status, msg = http.StatusForbidden, err.Error()
	case errors.Is(err, domain.ErrNotRegistered):
		status, msg = http.StatusForbidden, err.Error()
	case errors.Is(err, domain.ErrInvalidRole), errors.Is(err, domain.ErrInvalidPlan), errors.Is(err, domain.ErrInvalidEmail), errors.Is(err, domain.ErrInvalidTimezone), errors.Is(err, domain.ErrInvalidDose), errors.Is(err, domain.ErrInvalidRange), errors.Is(err, domain.ErrInvalidDeviceToken), errors.Is(err, domain.ErrInvalidStock):
		status, msg = http.StatusUnprocessableEntity, err.Error()
	case errors.Is(err, domain.ErrForbidden):
		status, msg = http.StatusForbidden, err.Error()
	case errors.Is(err, domain.ErrAlreadyInvited):
		status, msg = http.StatusConflict, err.Error()
	case errors.Is(err, domain.ErrInvitationExpired):
		status, msg = http.StatusGone, err.Error()
	case errors.Is(err, domain.ErrNotFound):
		status, msg = http.StatusNotFound, err.Error()
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

// DeleteMe: DELETE /me  (elimina la cuenta y todos sus datos)
func (h *AuthHandler) DeleteMe(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	if err := h.svc.DeleteAccount(r.Context(), actor); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
