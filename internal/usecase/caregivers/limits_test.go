package caregivers

import (
	"context"
	"errors"
	"testing"

	"github.com/lea/mediplan-api/internal/domain"
)

type fakeLimits map[string]domain.Limits

func (f fakeLimits) LimitsOf(_ context.Context, id string) (domain.Limits, error) {
	if l, ok := f[id]; ok {
		return l, nil
	}
	return domain.LimitsFor(domain.TierFree), nil
}

func TestInviteLimits(t *testing.T) {
	ctx := context.Background()

	t.Run("dada una dueña gratis con un cuidador en el plan, cuando invita a otro, entonces se rechaza por el límite de cuidadores", func(t *testing.T) {
		w := givenWorld()
		w.svc.SetLimits(fakeLimits{})
		w.invitar(t, "p1", "uno@mail.com")
		_, _, err := w.svc.Invite(ctx, ana, "p1", "dos@mail.com")
		var pl *domain.PlanLimitError
		if !errors.As(err, &pl) || pl.Resource != domain.ResourceCaregivers || pl.Limit != 1 {
			t.Fatalf("esperaba el límite de cuidadores y fue %v", err)
		}
	})
	t.Run("dada una dueña gratis, cuando invita a un cuidador en OTRO plan, entonces se permite (el tope es por plan)", func(t *testing.T) {
		w := givenWorld()
		w.svc.SetLimits(fakeLimits{})
		w.invitar(t, "p1", "uno@mail.com")
		if _, _, err := w.svc.Invite(ctx, ana, "p2", "dos@mail.com"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("dada una invitación rechazada, cuando la dueña gratis invita a otra persona, entonces se permite (las rechazadas no cuentan)", func(t *testing.T) {
		w := givenWorld()
		w.svc.SetLimits(fakeLimits{})
		inv, _ := w.invitar(t, "p1", "uno@mail.com")
		inv.Status = domain.InviteRejected
		_ = w.invites.Update(ctx, inv)
		if _, _, err := w.svc.Invite(ctx, ana, "p1", "dos@mail.com"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("dada una dueña Premium, cuando invita a varios cuidadores, entonces no hay límite", func(t *testing.T) {
		w := givenWorld()
		w.svc.SetLimits(fakeLimits{"ana": domain.LimitsFor(domain.TierPremium)})
		for _, e := range []string{"a@mail.com", "b@mail.com", "c@mail.com"} {
			w.invitar(t, "p1", e)
		}
	})
	t.Run("dado un servicio sin topes configurados, cuando se invita a varios, entonces no se limita (compatibilidad)", func(t *testing.T) {
		w := givenWorld()
		w.invitar(t, "p1", "uno@mail.com")
		w.invitar(t, "p1", "dos@mail.com")
	})
}
