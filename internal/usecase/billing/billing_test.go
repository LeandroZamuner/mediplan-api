package billing

import (
	"context"
	"testing"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

type fakeRepo struct {
	byUser map[string]*domain.Subscription
}

func (f *fakeRepo) ByUser(_ context.Context, id string) (*domain.Subscription, error) {
	if s, ok := f.byUser[id]; ok {
		c := *s
		return &c, nil
	}
	return nil, domain.ErrNotFound
}

type fakeUsage struct{ plans, meds int }

func (f fakeUsage) Usage(context.Context, string) (int, int, error) { return f.plans, f.meds, nil }

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func given(subs ...*domain.Subscription) *Service {
	r := &fakeRepo{byUser: map[string]*domain.Subscription{}}
	for _, s := range subs {
		r.byUser[s.UserID] = s
	}
	s := NewService(r, fakeUsage{plans: 1, meds: 2})
	s.now = func() time.Time { return now }
	return s
}

func at(d time.Duration) *time.Time { t := now.Add(d); return &t }

func TestTierOf(t *testing.T) {
	ctx := context.Background()
	day := 24 * time.Hour
	cases := []struct {
		name string
		sub  *domain.Subscription
		want domain.Tier
	}{
		{"sin suscripción, entonces es gratis", nil, domain.TierFree},
		{"con Premium activo y período vigente, entonces es Premium", &domain.Subscription{UserID: "u", Tier: domain.TierPremium, Status: domain.SubActive, CurrentPeriodEnd: at(10 * day)}, domain.TierPremium},
		{"con Premium activo sin fecha de fin, entonces es Premium", &domain.Subscription{UserID: "u", Tier: domain.TierPremium, Status: domain.SubActive}, domain.TierPremium},
		{"con Premium activo y el período vencido hace 2 días (cobro demorado), entonces sigue Premium por tolerancia", &domain.Subscription{UserID: "u", Tier: domain.TierPremium, Status: domain.SubActive, CurrentPeriodEnd: at(-2 * day)}, domain.TierPremium},
		{"con Premium activo y el período vencido hace 5 días, entonces vuelve a gratis", &domain.Subscription{UserID: "u", Tier: domain.TierPremium, Status: domain.SubActive, CurrentPeriodEnd: at(-5 * day)}, domain.TierFree},
		{"con Premium cancelado pero con período pagado por delante, entonces sigue Premium hasta el final", &domain.Subscription{UserID: "u", Tier: domain.TierPremium, Status: domain.SubCancelled, CurrentPeriodEnd: at(5 * day)}, domain.TierPremium},
		{"con Premium cancelado y el período terminado, entonces es gratis (sin tolerancia)", &domain.Subscription{UserID: "u", Tier: domain.TierPremium, Status: domain.SubCancelled, CurrentPeriodEnd: at(-1 * time.Hour)}, domain.TierFree},
		{"con Premium cancelado sin fecha, entonces es gratis", &domain.Subscription{UserID: "u", Tier: domain.TierPremium, Status: domain.SubCancelled}, domain.TierFree},
	}
	for _, c := range cases {
		t.Run("dado un usuario "+c.name, func(t *testing.T) {
			var s *Service
			if c.sub == nil {
				s = given()
			} else {
				s = given(c.sub)
			}
			got, err := s.TierOf(ctx, "u")
			if err != nil || got != c.want {
				t.Fatalf("tier=%s err=%v, esperaba %s", got, err, c.want)
			}
		})
	}
}

func TestLimitsOf(t *testing.T) {
	ctx := context.Background()
	t.Run("dado un usuario gratis, entonces recibe los topes gratis", func(t *testing.T) {
		l, err := given().LimitsOf(ctx, "u")
		if err != nil || l != domain.LimitsFor(domain.TierFree) {
			t.Fatalf("%+v %v", l, err)
		}
	})
	t.Run("dado un usuario Premium, entonces no tiene topes", func(t *testing.T) {
		s := given(&domain.Subscription{UserID: "u", Tier: domain.TierPremium, Status: domain.SubActive})
		l, _ := s.LimitsOf(ctx, "u")
		if l != domain.LimitsFor(domain.TierPremium) {
			t.Fatalf("%+v", l)
		}
	})
}

func TestStatus(t *testing.T) {
	ctx := context.Background()
	t.Run("dado un usuario gratis, cuando se pide su estado, entonces trae nivel, topes y uso actual", func(t *testing.T) {
		st, err := given().Status(ctx, "u")
		if err != nil {
			t.Fatal(err)
		}
		if st.Tier != domain.TierFree || st.Limits.MaxMedications != 3 || st.ActivePlans != 1 || st.Medications != 2 || st.CurrentPeriodEnd != nil {
			t.Fatalf("estado inesperado: %+v", st)
		}
	})
	t.Run("dado un Premium cancelado con período por delante, cuando se pide su estado, entonces informa que no se renueva y hasta cuándo", func(t *testing.T) {
		end := at(5 * 24 * time.Hour)
		s := given(&domain.Subscription{UserID: "u", Tier: domain.TierPremium, Status: domain.SubCancelled, CurrentPeriodEnd: end})
		st, _ := s.Status(ctx, "u")
		if st.Tier != domain.TierPremium || !st.Cancelled || st.CurrentPeriodEnd == nil || !st.CurrentPeriodEnd.Equal(*end) {
			t.Fatalf("estado inesperado: %+v", st)
		}
	})
}
