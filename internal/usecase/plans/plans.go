// Package plans maneja los planes de tratamiento y sus medicamentos.
//
// Ser "cuidador" no es un rol de la cuenta: es estar vinculado (con vínculo
// aceptado) a un plan concreto. Permisos sobre un plan:
//   - Ver y agregar medicamentos: su dueño (el paciente) y los cuidadores de ESE plan.
//   - Editar y eliminar: los cuidadores de ese plan; el dueño solo si ese plan
//     NO tiene ningún cuidador (si tiene, ese rol lo asume el cuidador).
//   - Cada usuario crea sus propios planes; nadie crea planes a nombre de otro.
package plans

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

// Puertos.

type PlanRepo interface {
	Create(ctx context.Context, p *domain.Plan) error // completa ID del plan y de sus medicamentos
	// Update reemplaza nombre y medicamentos pero NO toca el stock de los que ya existen:
	// así una edición no pisa un descuento concurrente. ErrNotFound si no existe.
	Update(ctx context.Context, p *domain.Plan) error
	Delete(ctx context.Context, id string) error               // ErrNotFound
	ByID(ctx context.Context, id string) (*domain.Plan, error) // ErrNotFound
	ByPatient(ctx context.Context, patientID string) ([]*domain.Plan, error)
	// SetMedicationStock fija el stock exacto de un medicamento (ajuste explícito). ErrNotFound.
	SetMedicationStock(ctx context.Context, medicationID string, units int) error
}

// CaregiverLinks responde sobre los cuidadores aceptados de cada plan.
type CaregiverLinks interface {
	IsAcceptedCaregiver(ctx context.Context, userID, planID string) (bool, error)
	HasAcceptedCaregiver(ctx context.Context, planID string) (bool, error)
	SupervisedPlanIDs(ctx context.Context, userID string) ([]string, error)
	AcceptedCaregiverIDs(ctx context.Context, planID string) ([]string, error)
}

// Syncer avisa a los participantes de un plan (dueño y cuidadores) que cambiaron sus horarios,
// para que sus celulares vuelvan a consultarlo y reprogramen las alarmas locales. Es
// "mejor esfuerzo": no devuelve error y no debe bloquear la operación.
type Syncer interface {
	Sync(ctx context.Context, userIDs []string, planID string)
}

type MedicationInput struct {
	Name         string
	Dose         string
	StartDate    time.Time
	EndDate      *time.Time
	Schedules    []domain.Schedule
	StockUnits   int
	StockAlertAt int
}

type Service struct {
	plans PlanRepo
	links CaregiverLinks
	sync  Syncer // opcional
	now   func() time.Time
}

// SetSyncer activa el aviso de cambios a los participantes del plan.
func (s *Service) SetSyncer(x Syncer) { s.sync = x }

// participants: el dueño y sus cuidadores aceptados.
func (s *Service) participants(ctx context.Context, p *domain.Plan) []string {
	ids := []string{p.PatientID}
	if cg, err := s.links.AcceptedCaregiverIDs(ctx, p.ID); err == nil {
		ids = append(ids, cg...)
	}
	return ids
}

func (s *Service) notifyChange(ctx context.Context, participants []string, planID string) {
	if s.sync != nil {
		s.sync.Sync(ctx, participants, planID)
	}
}

func NewService(p PlanRepo, l CaregiverLinks) *Service {
	return &Service{plans: p, links: l, now: time.Now}
}

type action int

const (
	actView action = iota
	actCreate
	actModify // editar o eliminar
)

// authorize aplica la matriz de permisos descripta arriba sobre un plan.
func (s *Service) authorize(ctx context.Context, actor *domain.User, p *domain.Plan, act action) error {
	isOwner := actor.ID == p.PatientID
	isSupervisor := false
	if !isOwner {
		ok, err := s.links.IsAcceptedCaregiver(ctx, actor.ID, p.ID)
		if err != nil {
			return err
		}
		isSupervisor = ok
	}
	if !isOwner && !isSupervisor {
		return domain.ErrForbidden
	}
	if act == actModify && isOwner {
		hasCaregiver, err := s.links.HasAcceptedCaregiver(ctx, p.ID)
		if err != nil {
			return err
		}
		if hasCaregiver {
			return domain.ErrForbidden
		}
	}
	return nil
}

// load trae el plan y verifica el permiso sobre él.
func (s *Service) load(ctx context.Context, actor *domain.User, planID string, act action) (*domain.Plan, error) {
	p, err := s.plans.ByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, actor, p, act); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) CreatePlan(ctx context.Context, actor *domain.User, name string, meds []MedicationInput) (*domain.Plan, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, invalid("el plan necesita un nombre")
	}
	if len(meds) == 0 {
		return nil, invalid("el plan necesita al menos un medicamento")
	}
	plan := &domain.Plan{PatientID: actor.ID, Name: name, Status: domain.PlanActive, CreatedAt: s.now()}
	for _, in := range meds {
		m, err := toMedication(in)
		if err != nil {
			return nil, err
		}
		plan.Medications = append(plan.Medications, m)
	}
	if err := s.plans.Create(ctx, plan); err != nil {
		return nil, err
	}
	s.notifyChange(ctx, []string{plan.PatientID}, plan.ID)
	return plan, nil
}

func (s *Service) GetPlan(ctx context.Context, actor *domain.User, planID string) (*domain.Plan, error) {
	return s.load(ctx, actor, planID, actView)
}

// ListPlans devuelve los planes propios del usuario.
func (s *Service) ListPlans(ctx context.Context, actor *domain.User) ([]*domain.Plan, error) {
	return s.plans.ByPatient(ctx, actor.ID)
}

// ListSupervised devuelve los planes de otras personas que el usuario cuida.
func (s *Service) ListSupervised(ctx context.Context, actor *domain.User) ([]*domain.Plan, error) {
	ids, err := s.links.SupervisedPlanIDs(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	var out []*domain.Plan
	for _, id := range ids {
		p, err := s.plans.ByID(ctx, id)
		if err == domain.ErrNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Service) AddMedication(ctx context.Context, actor *domain.User, planID string, in MedicationInput) (*domain.Plan, error) {
	p, err := s.load(ctx, actor, planID, actCreate)
	if err != nil {
		return nil, err
	}
	m, err := toMedication(in)
	if err != nil {
		return nil, err
	}
	p.Medications = append(p.Medications, m)
	return s.saveSchedule(ctx, p, nil)
}

func (s *Service) UpdateMedication(ctx context.Context, actor *domain.User, planID, medID string, in MedicationInput) (*domain.Plan, error) {
	p, err := s.load(ctx, actor, planID, actModify)
	if err != nil {
		return nil, err
	}
	i := medIndex(p, medID)
	if i < 0 {
		return nil, domain.ErrNotFound
	}
	m, err := toMedication(in)
	if err != nil {
		return nil, err
	}
	m.ID = medID
	p.Medications[i] = m
	return s.saveSchedule(ctx, p, &stockSet{medID, m.StockUnits})
}

// MedicationPatch son los cambios parciales de un medicamento: solo se modifica lo que viene
// con valor. EndDate fija la fecha de fin; ClearEndDate la borra (no se pueden usar juntos).
type MedicationPatch struct {
	Name         *string
	Dose         *string
	StartDate    *time.Time
	EndDate      *time.Time
	ClearEndDate bool
	Schedules    *[]domain.Schedule // si viene, reemplaza TODOS los horarios
	StockUnits   *int               // ajuste explícito (fija ese valor exacto)
	StockAlertAt *int
}

func (p MedicationPatch) isEmpty() bool {
	return p.Name == nil && p.Dose == nil && p.StartDate == nil && p.EndDate == nil && !p.ClearEndDate &&
		p.Schedules == nil && p.StockUnits == nil && p.StockAlertAt == nil
}

// touchesAlarm: la alarma local muestra nombre y dosis y se programa con horarios y fechas;
// el stock no la afecta, así que un cambio solo de stock no manda sincronización.
func (p MedicationPatch) touchesAlarm() bool {
	return p.Name != nil || p.Dose != nil || p.StartDate != nil || p.EndDate != nil || p.ClearEndDate || p.Schedules != nil
}

// PatchMedication modifica solo los campos indicados; el resto queda como estaba.
func (s *Service) PatchMedication(ctx context.Context, actor *domain.User, planID, medID string, patch MedicationPatch) (*domain.Plan, error) {
	if patch.isEmpty() {
		return nil, invalid("no hay nada para cambiar")
	}
	if patch.ClearEndDate && patch.EndDate != nil {
		return nil, invalid("no se puede fijar y borrar la fecha de fin a la vez")
	}
	p, err := s.load(ctx, actor, planID, actModify)
	if err != nil {
		return nil, err
	}
	i := medIndex(p, medID)
	if i < 0 {
		return nil, domain.ErrNotFound
	}
	cur := p.Medications[i]
	in := MedicationInput{Name: cur.Name, Dose: cur.Dose, StartDate: cur.StartDate, EndDate: cur.EndDate,
		Schedules: cur.Schedules, StockUnits: cur.StockUnits, StockAlertAt: cur.StockAlertAt}
	if patch.Name != nil {
		in.Name = *patch.Name
	}
	if patch.Dose != nil {
		in.Dose = *patch.Dose
	}
	if patch.StartDate != nil {
		in.StartDate = *patch.StartDate
	}
	if patch.EndDate != nil {
		in.EndDate = patch.EndDate
	}
	if patch.ClearEndDate {
		in.EndDate = nil
	}
	if patch.Schedules != nil {
		in.Schedules = *patch.Schedules
	}
	if patch.StockUnits != nil {
		in.StockUnits = *patch.StockUnits
	}
	if patch.StockAlertAt != nil {
		in.StockAlertAt = *patch.StockAlertAt
	}
	m, err := toMedication(in) // valida el resultado completo
	if err != nil {
		return nil, err
	}
	m.ID = medID
	p.Medications[i] = m

	var set *stockSet
	if patch.StockUnits != nil {
		set = &stockSet{medID, *patch.StockUnits}
	}
	if patch.touchesAlarm() {
		return s.saveSchedule(ctx, p, set)
	}
	return s.saveWithStock(ctx, p, set)
}

func (s *Service) RemoveMedication(ctx context.Context, actor *domain.User, planID, medID string) (*domain.Plan, error) {
	p, err := s.load(ctx, actor, planID, actModify)
	if err != nil {
		return nil, err
	}
	i := medIndex(p, medID)
	if i < 0 {
		return nil, domain.ErrNotFound
	}
	if len(p.Medications) == 1 {
		return nil, invalid("el plan no puede quedar sin medicamentos; eliminá el plan")
	}
	p.Medications = append(p.Medications[:i], p.Medications[i+1:]...)
	return s.saveSchedule(ctx, p, nil)
}

func (s *Service) RenamePlan(ctx context.Context, actor *domain.User, planID, name string) (*domain.Plan, error) {
	p, err := s.load(ctx, actor, planID, actModify)
	if err != nil {
		return nil, err
	}
	if name = strings.TrimSpace(name); name == "" {
		return nil, invalid("el plan necesita un nombre")
	}
	p.Name = name
	return s.save(ctx, p)
}

func (s *Service) DeletePlan(ctx context.Context, actor *domain.User, planID string) error {
	p, err := s.load(ctx, actor, planID, actModify)
	if err != nil {
		return err
	}
	participants := s.participants(ctx, p) // antes de borrar: después ya no hay vínculos
	if err := s.plans.Delete(ctx, planID); err != nil {
		return err
	}
	s.notifyChange(ctx, participants, planID)
	return nil
}

// stockSet es un ajuste explícito del stock de un medicamento (distinto de descontar o reponer).
type stockSet struct {
	medID string
	units int
}

func (s *Service) save(ctx context.Context, p *domain.Plan) (*domain.Plan, error) {
	return s.saveWithStock(ctx, p, nil)
}

// saveWithStock guarda el plan y, si se indica, fija el stock de un medicamento.
// El stock se fija aparte de Update a propósito: Update nunca lo pisa por accidente.
func (s *Service) saveWithStock(ctx context.Context, p *domain.Plan, set *stockSet) (*domain.Plan, error) {
	if err := s.plans.Update(ctx, p); err != nil {
		return nil, err
	}
	if set != nil {
		if err := s.plans.SetMedicationStock(ctx, set.medID, set.units); err != nil {
			return nil, err
		}
	}
	return s.plans.ByID(ctx, p.ID)
}

// saveSchedule guarda un cambio que afecta horarios o medicamentos y avisa a los participantes.
func (s *Service) saveSchedule(ctx context.Context, p *domain.Plan, set *stockSet) (*domain.Plan, error) {
	saved, err := s.saveWithStock(ctx, p, set)
	if err != nil {
		return nil, err
	}
	s.notifyChange(ctx, s.participants(ctx, saved), saved.ID)
	return saved, nil
}

func medIndex(p *domain.Plan, medID string) int {
	for i, m := range p.Medications {
		if m.ID == medID {
			return i
		}
	}
	return -1
}

func toMedication(in MedicationInput) (domain.Medication, error) {
	name, dose := strings.TrimSpace(in.Name), strings.TrimSpace(in.Dose)
	switch {
	case name == "":
		return domain.Medication{}, invalid("el medicamento necesita nombre")
	case dose == "":
		return domain.Medication{}, invalid("el medicamento %q necesita dosis", name)
	case len(in.Schedules) == 0:
		return domain.Medication{}, invalid("el medicamento %q necesita al menos un horario", name)
	case in.EndDate != nil && in.EndDate.Before(in.StartDate):
		return domain.Medication{}, invalid("el medicamento %q termina antes de empezar", name)
	case in.StockUnits < 0 || in.StockAlertAt < 0:
		return domain.Medication{}, invalid("el stock de %q no puede ser negativo", name)
	}
	for _, sc := range in.Schedules {
		if sc.DayOfWeek < 0 || sc.DayOfWeek > 6 {
			return domain.Medication{}, invalid("día de semana inválido: %d", sc.DayOfWeek)
		}
		if _, err := time.Parse("15:04", sc.TimeOfDay); err != nil {
			return domain.Medication{}, invalid("hora inválida: %q (usar HH:MM)", sc.TimeOfDay)
		}
	}
	return domain.Medication{
		Name: name, Dose: dose, StartDate: in.StartDate, EndDate: in.EndDate,
		Schedules: in.Schedules, StockUnits: in.StockUnits, StockAlertAt: in.StockAlertAt,
	}, nil
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalidPlan, fmt.Sprintf(format, a...))
}
