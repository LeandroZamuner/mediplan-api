package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/lea/mediplan-api/internal/usecase/doses"
)

type DosesHandler struct{ svc *doses.Service }

func NewDosesHandler(svc *doses.Service) *DosesHandler { return &DosesHandler{svc: svc} }

type doseDTO struct {
	ID           string     `json:"id,omitempty"`
	MedicationID string     `json:"medicationId"`
	ScheduledAt  time.Time  `json:"scheduledAt"`
	TakenAt      *time.Time `json:"takenAt,omitempty"`
	Status       string     `json:"status"`
}

type occurrenceDTO struct {
	MedicationID   string     `json:"medicationId"`
	MedicationName string     `json:"medicationName"`
	ScheduledAt    time.Time  `json:"scheduledAt"`
	TakenAt        *time.Time `json:"takenAt,omitempty"`
	Status         string     `json:"status"`
}

// RecordTaken: POST /plans/{planID}/medications/{medID}/doses {"scheduledAt":RFC3339,"takenAt"?:RFC3339}
func (h *DosesHandler) RecordTaken(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	var body struct {
		ScheduledAt time.Time  `json:"scheduledAt"`
		TakenAt     *time.Time `json:"takenAt"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.ScheduledAt.IsZero() {
		badRequest(w, "falta scheduledAt (formato RFC 3339)")
		return
	}
	l, err := h.svc.RecordTaken(r.Context(), actor, chi.URLParam(r, "planID"), chi.URLParam(r, "medID"), body.ScheduledAt, body.TakenAt)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doseDTO{ID: l.ID, MedicationID: l.MedicationID, ScheduledAt: l.ScheduledAt, TakenAt: l.TakenAt, Status: string(l.Status)})
}

// History: GET /plans/{planID}/doses?from=&to=  (por defecto, los últimos 7 días)
func (h *DosesHandler) History(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	from, to, ok := parseRange(w, r)
	if !ok {
		return
	}
	occ, err := h.svc.History(r.Context(), actor, chi.URLParam(r, "planID"), from, to)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]occurrenceDTO, 0, len(occ))
	for _, o := range occ {
		out = append(out, occurrenceDTO{MedicationID: o.MedicationID, MedicationName: o.MedicationName,
			ScheduledAt: o.ScheduledAt, TakenAt: o.TakenAt, Status: string(o.Status)})
	}
	writeJSON(w, http.StatusOK, out)
}

// Stats: GET /plans/{planID}/stats?from=&to=
func (h *DosesHandler) Stats(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	from, to, ok := parseRange(w, r)
	if !ok {
		return
	}
	st, err := h.svc.Stats(r.Context(), actor, chi.URLParam(r, "planID"), from, to)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expected": st.Expected, "taken": st.Taken, "missed": st.Missed, "pending": st.Pending,
		"adherenceRate": st.AdherenceRate, // null si todavía no venció ninguna toma
	})
}

func parseRange(w http.ResponseWriter, r *http.Request) (from, to time.Time, ok bool) {
	to = time.Now()
	if v := r.URL.Query().Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			badRequest(w, "parámetro 'to' inválido (usar RFC 3339)")
			return from, to, false
		}
		to = t
	}
	from = to.AddDate(0, 0, -7)
	if v := r.URL.Query().Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			badRequest(w, "parámetro 'from' inválido (usar RFC 3339)")
			return from, to, false
		}
		from = t
	}
	return from, to, true
}
