package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/plans"
)

const dateLayout = "2006-01-02"

type PlansHandler struct{ svc *plans.Service }

func NewPlansHandler(svc *plans.Service) *PlansHandler { return &PlansHandler{svc: svc} }

// ---- DTOs ----

type scheduleDTO struct {
	DayOfWeek int    `json:"dayOfWeek"`
	TimeOfDay string `json:"timeOfDay"`
}

type medicationDTO struct {
	ID           string        `json:"id,omitempty"`
	Name         string        `json:"name"`
	Dose         string        `json:"dose"`
	StartDate    string        `json:"startDate"`
	EndDate      *string       `json:"endDate,omitempty"`
	Schedules    []scheduleDTO `json:"schedules"`
	StockUnits   int           `json:"stockUnits"`
	StockAlertAt int           `json:"stockAlertAt"`
}

type planDTO struct {
	ID          string          `json:"id"`
	PatientID   string          `json:"patientId"`
	Name        string          `json:"name"`
	Status      string          `json:"status"`
	CreatedAt   time.Time       `json:"createdAt"`
	Medications []medicationDTO `json:"medications"`
}

func (d medicationDTO) toInput() (plans.MedicationInput, error) {
	start, err := time.Parse(dateLayout, d.StartDate)
	if err != nil {
		return plans.MedicationInput{}, err
	}
	in := plans.MedicationInput{Name: d.Name, Dose: d.Dose, StartDate: start, StockUnits: d.StockUnits, StockAlertAt: d.StockAlertAt}
	if d.EndDate != nil {
		end, err := time.Parse(dateLayout, *d.EndDate)
		if err != nil {
			return plans.MedicationInput{}, err
		}
		in.EndDate = &end
	}
	for _, s := range d.Schedules {
		in.Schedules = append(in.Schedules, domain.Schedule{DayOfWeek: s.DayOfWeek, TimeOfDay: s.TimeOfDay})
	}
	return in, nil
}

func toPlanDTO(p *domain.Plan) planDTO {
	out := planDTO{ID: p.ID, PatientID: p.PatientID, Name: p.Name, Status: string(p.Status), CreatedAt: p.CreatedAt, Medications: []medicationDTO{}}
	for _, m := range p.Medications {
		md := medicationDTO{ID: m.ID, Name: m.Name, Dose: m.Dose, StartDate: m.StartDate.Format(dateLayout),
			StockUnits: m.StockUnits, StockAlertAt: m.StockAlertAt, Schedules: []scheduleDTO{}}
		if m.EndDate != nil {
			e := m.EndDate.Format(dateLayout)
			md.EndDate = &e
		}
		for _, s := range m.Schedules {
			md.Schedules = append(md.Schedules, scheduleDTO{DayOfWeek: s.DayOfWeek, TimeOfDay: s.TimeOfDay})
		}
		out.Medications = append(out.Medications, md)
	}
	return out
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "json inválido"})
		return false
	}
	return true
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

// medInput decodifica y convierte un medicamento del body.
func medInput(w http.ResponseWriter, r *http.Request) (plans.MedicationInput, bool) {
	var d medicationDTO
	if !decode(w, r, &d) {
		return plans.MedicationInput{}, false
	}
	in, err := d.toInput()
	if err != nil {
		badRequest(w, "fecha inválida (usar AAAA-MM-DD)")
		return plans.MedicationInput{}, false
	}
	return in, true
}

// ---- handlers ----

// Create: POST /plans  {name, medications[]}. El plan queda a nombre del usuario autenticado.
func (h *PlansHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	var body struct {
		Name        string          `json:"name"`
		Medications []medicationDTO `json:"medications"`
	}
	if !decode(w, r, &body) {
		return
	}
	var meds []plans.MedicationInput
	for _, d := range body.Medications {
		in, err := d.toInput()
		if err != nil {
			badRequest(w, "fecha inválida (usar AAAA-MM-DD)")
			return
		}
		meds = append(meds, in)
	}
	p, err := h.svc.CreatePlan(r.Context(), actor, body.Name, meds)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toPlanDTO(p))
}

// List: GET /plans  (los planes propios)
func (h *PlansHandler) List(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	list, err := h.svc.ListPlans(r.Context(), actor)
	h.respondList(w, list, err)
}

// Supervised: GET /plans/supervised  (planes de otras personas que el usuario cuida)
func (h *PlansHandler) Supervised(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	list, err := h.svc.ListSupervised(r.Context(), actor)
	h.respondList(w, list, err)
}

func (h *PlansHandler) respondList(w http.ResponseWriter, list []*domain.Plan, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]planDTO, 0, len(list))
	for _, p := range list {
		out = append(out, toPlanDTO(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *PlansHandler) Get(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	p, err := h.svc.GetPlan(r.Context(), actor, chi.URLParam(r, "planID"))
	h.respond(w, http.StatusOK, p, err)
}

func (h *PlansHandler) Rename(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, err := h.svc.RenamePlan(r.Context(), actor, chi.URLParam(r, "planID"), body.Name)
	h.respond(w, http.StatusOK, p, err)
}

func (h *PlansHandler) Delete(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	if err := h.svc.DeletePlan(r.Context(), actor, chi.URLParam(r, "planID")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *PlansHandler) AddMedication(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	in, ok := medInput(w, r)
	if !ok {
		return
	}
	p, err := h.svc.AddMedication(r.Context(), actor, chi.URLParam(r, "planID"), in)
	h.respond(w, http.StatusCreated, p, err)
}

func (h *PlansHandler) UpdateMedication(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	in, ok := medInput(w, r)
	if !ok {
		return
	}
	p, err := h.svc.UpdateMedication(r.Context(), actor, chi.URLParam(r, "planID"), chi.URLParam(r, "medID"), in)
	h.respond(w, http.StatusOK, p, err)
}

func (h *PlansHandler) RemoveMedication(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	p, err := h.svc.RemoveMedication(r.Context(), actor, chi.URLParam(r, "planID"), chi.URLParam(r, "medID"))
	h.respond(w, http.StatusOK, p, err)
}

func (h *PlansHandler) respond(w http.ResponseWriter, status int, p *domain.Plan, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, status, toPlanDTO(p))
}

// PatchMedication: PATCH /plans/{planID}/medications/{medID}
// Modifica solo los campos que vienen en el JSON; "endDate": null borra la fecha de fin.
// Los campos desconocidos se rechazan (400): un error de tipeo no se ignora en silencio.
func (h *PlansHandler) PatchMedication(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	var body struct {
		Name         *string         `json:"name"`
		Dose         *string         `json:"dose"`
		StartDate    *string         `json:"startDate"`
		EndDate      json.RawMessage `json:"endDate"` // ausente = no tocar; null = borrar; "AAAA-MM-DD" = fijar
		Schedules    *[]scheduleDTO  `json:"schedules"`
		StockUnits   *int            `json:"stockUnits"`
		StockAlertAt *int            `json:"stockAlertAt"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		badRequest(w, "json inválido o con campos desconocidos")
		return
	}

	patch := plans.MedicationPatch{Name: body.Name, Dose: body.Dose, StockUnits: body.StockUnits, StockAlertAt: body.StockAlertAt}
	if body.StartDate != nil {
		t, err := time.Parse(dateLayout, *body.StartDate)
		if err != nil {
			badRequest(w, "fecha inválida (usar AAAA-MM-DD)")
			return
		}
		patch.StartDate = &t
	}
	if body.EndDate != nil {
		if string(body.EndDate) == "null" {
			patch.ClearEndDate = true
		} else {
			t, ok := parseJSONDate(body.EndDate)
			if !ok {
				badRequest(w, "fecha inválida (usar AAAA-MM-DD o null)")
				return
			}
			patch.EndDate = &t
		}
	}
	if body.Schedules != nil {
		list := make([]domain.Schedule, 0, len(*body.Schedules))
		for _, s := range *body.Schedules {
			list = append(list, domain.Schedule{DayOfWeek: s.DayOfWeek, TimeOfDay: s.TimeOfDay})
		}
		patch.Schedules = &list
	}

	p, err := h.svc.PatchMedication(r.Context(), actor, chi.URLParam(r, "planID"), chi.URLParam(r, "medID"), patch)
	h.respond(w, http.StatusOK, p, err)
}

// parseJSONDate interpreta un valor JSON de tipo texto "AAAA-MM-DD".
func parseJSONDate(raw json.RawMessage) (time.Time, bool) {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(dateLayout, text)
	return t, err == nil
}
