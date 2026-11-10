// Package doses registra las tomas de medicación y calcula historial y cumplimiento.
//
// Una "toma" es un medicamento a una hora programada. Solo se guardan las tomas
// hechas y las omitidas; lo demás se deduce de los horarios del plan (hora local
// del paciente) y del reloj. Una toma sin registro cuya hora pasó hace más del
// margen (30 min) cuenta como omitida.
package doses

import (
	"context"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

const (
	GracePeriod = 30 * time.Minute // margen para registrar una toma antes de darla por omitida
	MaxEarly    = 2 * time.Hour    // cuánto antes de la hora se puede registrar una toma
	MaxRange    = 93 * 24 * time.Hour
	clockSkew   = 5 * time.Minute // tolerancia entre el reloj del celular y el del servidor
)

// Puertos.

// PlanAccess devuelve el plan si el usuario puede verlo (dueño o cuidador de ese plan);
// ErrForbidden / ErrNotFound si no. Lo cumple plans.Service.
type PlanAccess interface {
	GetPlan(ctx context.Context, actor *domain.User, planID string) (*domain.Plan, error)
}

type Timezones interface {
	TimezoneOf(ctx context.Context, userID string) (string, error)
}

// StockHook descuenta el stock cuando se toma una dosis. Es opcional y de mejor esfuerzo:
// si falla, la toma ya quedó registrada y no se revierte. Lo cumple stock.Service.
type StockHook interface {
	OnDoseTaken(ctx context.Context, plan *domain.Plan, med domain.Medication) error
}

type LogRepo interface {
	Get(ctx context.Context, medicationID string, scheduledAt time.Time) (*domain.DoseLog, error) // ErrNotFound
	Save(ctx context.Context, l *domain.DoseLog) error                                            // upsert por (medicamento, hora)
	Between(ctx context.Context, medicationIDs []string, from, to time.Time) ([]*domain.DoseLog, error)
}

type Occurrence struct {
	MedicationID   string
	MedicationName string
	ScheduledAt    time.Time
	TakenAt        *time.Time
	Status         domain.DoseStatus
}

type Stats struct {
	Expected int
	Taken    int
	Missed   int
	Pending  int
	// AdherenceRate = tomadas / (tomadas + omitidas). nil si todavía no venció ninguna toma.
	AdherenceRate *float64
}

type Service struct {
	plans PlanAccess
	zones Timezones
	logs  LogRepo
	stock StockHook // opcional
	now   func() time.Time
}

// SetStock activa el descuento de stock en cada toma registrada.
func (s *Service) SetStock(h StockHook) { s.stock = h }

func NewService(p PlanAccess, z Timezones, l LogRepo) *Service {
	return &Service{plans: p, zones: z, logs: l, now: time.Now}
}

// RecordTaken registra que la toma programada para scheduledAt se hizo. Es idempotente:
// si ya estaba tomada, no cambia. Si estaba omitida, pasa a tomada (registro tardío).
// takenAt es opcional (toma hecha sin internet); por defecto, ahora.
func (s *Service) RecordTaken(ctx context.Context, actor *domain.User, planID, medID string, scheduledAt time.Time, takenAt *time.Time) (*domain.DoseLog, error) {
	plan, loc, err := s.load(ctx, actor, planID)
	if err != nil {
		return nil, err
	}
	med := findMedication(plan, medID)
	if med == nil {
		return nil, domain.ErrNotFound
	}
	now := s.now()
	// scheduledAt tiene que ser exactamente una de las horas programadas del medicamento.
	if exact := domain.Occurrences(*med, loc, scheduledAt, scheduledAt.Add(time.Minute), time.Time{}); len(exact) != 1 || !exact[0].Equal(scheduledAt) {
		return nil, domain.ErrInvalidDose
	}
	if scheduledAt.After(now.Add(MaxEarly)) {
		return nil, domain.ErrInvalidDose
	}
	when := now
	if takenAt != nil {
		if takenAt.After(now.Add(clockSkew)) {
			return nil, domain.ErrInvalidDose
		}
		when = *takenAt
	}

	existing, err := s.logs.Get(ctx, medID, scheduledAt)
	if err != nil && err != domain.ErrNotFound {
		return nil, err
	}
	if existing != nil && existing.Status == domain.DoseTaken {
		return existing, nil
	}
	log := &domain.DoseLog{MedicationID: medID, ScheduledAt: scheduledAt, TakenAt: &when, Status: domain.DoseTaken}
	if err := s.logs.Save(ctx, log); err != nil {
		return nil, err
	}
	// Solo llegamos acá si la toma pasó a "tomada" ahora (las repetidas salieron antes): descontamos una vez.
	if s.stock != nil {
		_ = s.stock.OnDoseTaken(ctx, plan, *med)
	}
	return log, nil
}

// History devuelve cada toma del plan dentro de [from, to) con su estado.
func (s *Service) History(ctx context.Context, actor *domain.User, planID string, from, to time.Time) ([]Occurrence, error) {
	if !from.Before(to) || to.Sub(from) > MaxRange {
		return nil, domain.ErrInvalidRange
	}
	plan, loc, err := s.load(ctx, actor, planID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(plan.Medications))
	for _, m := range plan.Medications {
		ids = append(ids, m.ID)
	}
	logs, err := s.logs.Between(ctx, ids, from, to)
	if err != nil {
		return nil, err
	}
	byKey := map[string]*domain.DoseLog{}
	for _, l := range logs {
		byKey[l.MedicationID+"|"+l.ScheduledAt.UTC().Format(time.RFC3339)] = l
	}

	now := s.now()
	var out []Occurrence
	for _, m := range plan.Medications {
		for _, at := range domain.Occurrences(m, loc, from, to, plan.CreatedAt) {
			o := Occurrence{MedicationID: m.ID, MedicationName: m.Name, ScheduledAt: at, Status: domain.DosePending}
			if l := byKey[m.ID+"|"+at.UTC().Format(time.RFC3339)]; l != nil {
				o.Status, o.TakenAt = l.Status, l.TakenAt
			} else if !now.Before(at.Add(GracePeriod)) {
				o.Status = domain.DoseMissed
			}
			out = append(out, o)
		}
	}
	sortOccurrences(out)
	return out, nil
}

// Stats resume el cumplimiento del plan en [from, to).
func (s *Service) Stats(ctx context.Context, actor *domain.User, planID string, from, to time.Time) (*Stats, error) {
	occ, err := s.History(ctx, actor, planID, from, to)
	if err != nil {
		return nil, err
	}
	st := &Stats{Expected: len(occ)}
	for _, o := range occ {
		switch o.Status {
		case domain.DoseTaken:
			st.Taken++
		case domain.DoseMissed:
			st.Missed++
		default:
			st.Pending++
		}
	}
	if due := st.Taken + st.Missed; due > 0 {
		rate := float64(st.Taken) / float64(due)
		st.AdherenceRate = &rate
	}
	return st, nil
}

// load verifica el acceso al plan y devuelve la zona horaria del DUEÑO (los horarios son hora local suya).
func (s *Service) load(ctx context.Context, actor *domain.User, planID string) (*domain.Plan, *time.Location, error) {
	plan, err := s.plans.GetPlan(ctx, actor, planID)
	if err != nil {
		return nil, nil, err
	}
	tz, err := s.zones.TimezoneOf(ctx, plan.PatientID)
	if err != nil {
		return nil, nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc, _ = time.LoadLocation(domain.DefaultTimezone)
	}
	return plan, loc, nil
}

func findMedication(p *domain.Plan, id string) *domain.Medication {
	for i := range p.Medications {
		if p.Medications[i].ID == id {
			return &p.Medications[i]
		}
	}
	return nil
}

func sortOccurrences(o []Occurrence) {
	for i := 1; i < len(o); i++ {
		for j := i; j > 0 && (o[j].ScheduledAt.Before(o[j-1].ScheduledAt)); j-- {
			o[j], o[j-1] = o[j-1], o[j]
		}
	}
}
