// Package billing decide qué nivel (gratis o Premium) tiene cada usuario y qué topes le corresponden.
// La suscripción se guarda en la base; quien la actualiza es la integración con el medio de pago.
package billing

import (
	"context"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

// gracePeriod es la tolerancia con un cobro demorado: una suscripción activa cuyo período venció hace menos que esto
// sigue siendo Premium (el aviso del medio de pago puede tardar). Una cancelada no tiene tolerancia.
const gracePeriod = 3 * 24 * time.Hour

// Puertos.

type SubscriptionRepo interface {
	ByUser(ctx context.Context, userID string) (*domain.Subscription, error) // ErrNotFound si nunca tuvo
}

// Usage cuenta lo que el usuario ya tiene cargado (planes activos y medicamentos de esos planes).
type Usage interface {
	Usage(ctx context.Context, userID string) (activePlans, medications int, err error)
}

type Service struct {
	repo  SubscriptionRepo
	usage Usage
	now   func() time.Time
}

func NewService(r SubscriptionRepo, u Usage) *Service {
	return &Service{repo: r, usage: u, now: time.Now}
}

// TierOf devuelve el nivel vigente del usuario hoy.
func (s *Service) TierOf(ctx context.Context, userID string) (domain.Tier, error) {
	sub, err := s.repo.ByUser(ctx, userID)
	if err == domain.ErrNotFound {
		return domain.TierFree, nil
	}
	if err != nil {
		return "", err
	}
	return s.effective(sub), nil
}

func (s *Service) effective(sub *domain.Subscription) domain.Tier {
	if sub.Tier != domain.TierPremium {
		return domain.TierFree
	}
	now := s.now()
	end := sub.CurrentPeriodEnd
	switch sub.Status {
	case domain.SubActive:
		if end == nil || now.Before(end.Add(gracePeriod)) {
			return domain.TierPremium
		}
	case domain.SubCancelled:
		if end != nil && now.Before(*end) {
			return domain.TierPremium
		}
	}
	return domain.TierFree
}

func (s *Service) LimitsOf(ctx context.Context, userID string) (domain.Limits, error) {
	t, err := s.TierOf(ctx, userID)
	if err != nil {
		return domain.Limits{}, err
	}
	return domain.LimitsFor(t), nil
}

// Status es lo que ven las apps: nivel, topes, uso actual y, si hay suscripción, si se renueva y hasta cuándo.
type Status struct {
	Tier             domain.Tier
	Limits           domain.Limits
	ActivePlans      int
	Medications      int
	Cancelled        bool // Premium que ya no se renueva
	CurrentPeriodEnd *time.Time
}

func (s *Service) Status(ctx context.Context, userID string) (*Status, error) {
	sub, err := s.repo.ByUser(ctx, userID)
	if err != nil && err != domain.ErrNotFound {
		return nil, err
	}
	st := &Status{Tier: domain.TierFree}
	if sub != nil {
		st.Tier = s.effective(sub)
		if st.Tier == domain.TierPremium {
			st.Cancelled = sub.Status == domain.SubCancelled
			st.CurrentPeriodEnd = sub.CurrentPeriodEnd
		}
	}
	st.Limits = domain.LimitsFor(st.Tier)
	if st.ActivePlans, st.Medications, err = s.usage.Usage(ctx, userID); err != nil {
		return nil, err
	}
	return st, nil
}
