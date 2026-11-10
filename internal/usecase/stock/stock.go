// Package stock lleva el stock de cada medicamento: descuenta una unidad por toma,
// avisa cuando queda poco o se agota, y permite reponer.
//
// Se avisa solo al CRUZAR el umbral (pasar de "por encima" a "en el umbral o por debajo"),
// no en cada toma. Al llegar a 0 el aviso es "se agotó" y reemplaza al de "queda poco".
// Los avisos van al paciente y a todos los cuidadores del plan.
package stock

import (
	"context"
	"fmt"

	"github.com/lea/mediplan-api/internal/domain"
)

const maxRefill = 100000

// Puertos.

// Repo cambia el stock de forma atómica (varias tomas a la vez no se pisan).
type Repo interface {
	// Consume descuenta n unidades sin bajar de 0 y devuelve el stock antes y después.
	Consume(ctx context.Context, medicationID string, n int) (before, after int, err error) // ErrNotFound
	// Add suma n unidades y devuelve el nuevo total.
	Add(ctx context.Context, medicationID string, n int) (after int, err error) // ErrNotFound
}

// PlanAccess devuelve el plan si el usuario puede verlo (dueño o cuidador). Lo cumple plans.Service.
type PlanAccess interface {
	GetPlan(ctx context.Context, actor *domain.User, planID string) (*domain.Plan, error)
}

// Participants son los usuarios que reciben los avisos del plan: el dueño y sus cuidadores aceptados.
type Participants interface {
	ParticipantIDs(ctx context.Context, plan *domain.Plan) ([]string, error)
}

type Notifier interface {
	Notify(ctx context.Context, userID string, n domain.Notification) error
}

type Service struct {
	repo         Repo
	plans        PlanAccess
	participants Participants
	notifier     Notifier
}

func NewService(r Repo, p PlanAccess, ps Participants, n Notifier) *Service {
	return &Service{repo: r, plans: p, participants: ps, notifier: n}
}

// OnDoseTaken descuenta una unidad del medicamento y avisa si cruzó el umbral.
// Los fallos al avisar no se propagan: el stock ya cambió y el aviso es de mejor esfuerzo.
func (s *Service) OnDoseTaken(ctx context.Context, plan *domain.Plan, med domain.Medication) error {
	before, after, err := s.repo.Consume(ctx, med.ID, 1)
	if err != nil {
		return err
	}
	var n *domain.Notification
	switch {
	case before > 0 && after == 0:
		n = alert(plan, med, "out", after, "Sin stock",
			fmt.Sprintf("Se agotó %s (%s) — plan %s. Hay que reponerlo.", med.Name, med.Dose, plan.Name))
	case before > med.StockAlertAt && after <= med.StockAlertAt:
		n = alert(plan, med, "low", after, "Stock bajo",
			fmt.Sprintf("Quedan %d unidades de %s (%s) — plan %s.", after, med.Name, med.Dose, plan.Name))
	}
	if n == nil {
		return nil
	}
	ids, err := s.participants.ParticipantIDs(ctx, plan)
	if err != nil {
		return nil
	}
	for _, id := range ids {
		_ = s.notifier.Notify(ctx, id, *n)
	}
	return nil
}

// Refill suma unidades al stock. Lo puede hacer el dueño del plan y sus cuidadores
// (solo suma, nunca resta: no es una edición destructiva). Devuelve el nuevo total.
func (s *Service) Refill(ctx context.Context, actor *domain.User, planID, medID string, units int) (int, error) {
	plan, err := s.plans.GetPlan(ctx, actor, planID)
	if err != nil {
		return 0, err
	}
	found := false
	for _, m := range plan.Medications {
		found = found || m.ID == medID
	}
	if !found {
		return 0, domain.ErrNotFound
	}
	if units <= 0 || units > maxRefill {
		return 0, domain.ErrInvalidStock
	}
	return s.repo.Add(ctx, medID, units)
}

func alert(plan *domain.Plan, med domain.Medication, level string, remaining int, title, body string) *domain.Notification {
	return &domain.Notification{
		Kind: domain.NotifLowStock, Title: title, Body: body,
		Data: map[string]string{
			"planId": plan.ID, "medicationId": med.ID, "level": level, "remaining": fmt.Sprint(remaining),
		},
	}
}
