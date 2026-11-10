package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/caregivers"
)

type CaregiversHandler struct{ svc *caregivers.Service }

func NewCaregiversHandler(svc *caregivers.Service) *CaregiversHandler {
	return &CaregiversHandler{svc: svc}
}

type caregiverDTO struct {
	ID          string    `json:"id"`
	PlanID      string    `json:"planId"`
	Email       string    `json:"email"`
	CaregiverID string    `json:"caregiverId,omitempty"`
	Status      string    `json:"status"`
	ExpiresAt   time.Time `json:"expiresAt"`
	// Token solo se devuelve al crear la invitación, para armar el enlace a compartir.
	Token string `json:"token,omitempty"`
}

func toCaregiverDTO(c *domain.PlanCaregiver) caregiverDTO {
	return caregiverDTO{ID: c.ID, PlanID: c.PlanID, Email: c.InviteEmail, CaregiverID: c.CaregiverID,
		Status: string(c.Status), ExpiresAt: c.ExpiresAt}
}

// Invite: POST /plans/{planID}/caregivers {email}
func (h *CaregiversHandler) Invite(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	var body struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &body) {
		return
	}
	c, tok, err := h.svc.Invite(r.Context(), actor, chi.URLParam(r, "planID"), body.Email)
	if err != nil {
		writeError(w, err)
		return
	}
	dto := toCaregiverDTO(c)
	dto.Token = tok
	writeJSON(w, http.StatusCreated, dto)
}

// List: GET /plans/{planID}/caregivers
func (h *CaregiversHandler) List(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	list, err := h.svc.ListCaregivers(r.Context(), actor, chi.URLParam(r, "planID"))
	h.respondList(w, list, err)
}

// Revoke: DELETE /plans/{planID}/caregivers/{linkID}  (lo hace el dueño)
func (h *CaregiversHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	h.noContent(w, h.svc.Revoke(r.Context(), actor, chi.URLParam(r, "planID"), chi.URLParam(r, "linkID")))
}

// Leave: DELETE /plans/{planID}/follow  (la cuidadora deja de seguir el plan)
func (h *CaregiversHandler) Leave(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	h.noContent(w, h.svc.Leave(r.Context(), actor, chi.URLParam(r, "planID")))
}

// Preview: GET /invitations/preview?token=  (público, sin sesión: solo el nombre del plan)
func (h *CaregiversHandler) Preview(w http.ResponseWriter, r *http.Request) {
	pv, err := h.svc.Preview(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"planName": pv.PlanName, "expiresAt": pv.ExpiresAt})
}

// AcceptByToken: POST /invitations/accept {token}
func (h *CaregiversHandler) AcceptByToken(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	var body struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &body) {
		return
	}
	c, err := h.svc.Accept(r.Context(), actor, body.Token)
	h.respondOne(w, c, err)
}

// Pending: GET /invitations  (invitaciones vigentes dirigidas a mi email)
func (h *CaregiversHandler) Pending(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	list, err := h.svc.ListPending(r.Context(), actor)
	h.respondList(w, list, err)
}

// AcceptByID: POST /invitations/{id}/accept
func (h *CaregiversHandler) AcceptByID(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	c, err := h.svc.AcceptByID(r.Context(), actor, chi.URLParam(r, "id"))
	h.respondOne(w, c, err)
}

// Reject: POST /invitations/{id}/reject
func (h *CaregiversHandler) Reject(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	h.noContent(w, h.svc.Reject(r.Context(), actor, chi.URLParam(r, "id")))
}

func (h *CaregiversHandler) respondOne(w http.ResponseWriter, c *domain.PlanCaregiver, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toCaregiverDTO(c))
}

func (h *CaregiversHandler) respondList(w http.ResponseWriter, list []*domain.PlanCaregiver, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]caregiverDTO, 0, len(list))
	for _, c := range list {
		out = append(out, toCaregiverDTO(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *CaregiversHandler) noContent(w http.ResponseWriter, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
