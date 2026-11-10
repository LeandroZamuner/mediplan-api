package httpapi

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/lea/mediplan-api/internal/usecase/notifications"
	"github.com/lea/mediplan-api/internal/usecase/reminders"
)

type NotificationsHandler struct{ svc *notifications.Service }

func NewNotificationsHandler(svc *notifications.Service) *NotificationsHandler {
	return &NotificationsHandler{svc: svc}
}

// RegisterDevice: POST /devices {"token":"<token FCM>","platform":"android"}
func (h *NotificationsHandler) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	var body struct{ Token, Platform string }
	if !decode(w, r, &body) {
		return
	}
	noContent(w, h.svc.RegisterDevice(r.Context(), actor, body.Token, body.Platform))
}

// UnregisterDevice: DELETE /devices {"token":"..."}  (al cerrar sesión)
func (h *NotificationsHandler) UnregisterDevice(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	var body struct{ Token string }
	if !decode(w, r, &body) {
		return
	}
	noContent(w, h.svc.UnregisterDevice(r.Context(), actor, body.Token))
}

type notificationDTO struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Data      map[string]string `json:"data"`
	ReadAt    *time.Time        `json:"readAt"`
	CreatedAt time.Time         `json:"createdAt"`
}

// List: GET /notifications  (las 50 más nuevas)
func (h *NotificationsHandler) List(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	list, err := h.svc.List(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]notificationDTO, 0, len(list))
	for _, n := range list {
		out = append(out, notificationDTO{ID: n.ID, Kind: n.Kind, Title: n.Title, Body: n.Body, Data: n.Data, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

// MarkRead: POST /notifications/{id}/read
func (h *NotificationsHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	noContent(w, h.svc.MarkRead(r.Context(), actor, chi.URLParam(r, "id")))
}

func noContent(w http.ResponseWriter, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Tareas programadas (Cloud Scheduler) ----

type JobsHandler struct {
	secret    string
	reminders *reminders.Service
}

func NewJobsHandler(secret string, rem *reminders.Service) *JobsHandler {
	return &JobsHandler{secret: secret, reminders: rem}
}

// MissedDoses: POST /internal/jobs/missed-doses con el header X-Job-Secret.
// Cloud Scheduler lo llama cada pocos minutos; no usa sesión de usuario.
func (h *JobsHandler) MissedDoses(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Job-Secret")), []byte(h.secret)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no autorizado"})
		return
	}
	rep, err := h.reminders.DetectMissed(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"newMissed": rep.NewMissed, "notified": rep.Notified, "failed": rep.Failed})
}
