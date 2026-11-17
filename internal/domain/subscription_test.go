package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestLimitsFor(t *testing.T) {
	t.Run("dado el nivel gratis, entonces tiene topes de 1 plan, 3 medicamentos y 1 cuidador por plan", func(t *testing.T) {
		l := LimitsFor(TierFree)
		if l.MaxActivePlans != 1 || l.MaxMedications != 3 || l.MaxCaregiversPerPlan != 1 {
			t.Fatalf("topes inesperados: %+v", l)
		}
	})
	t.Run("dado Premium, entonces no tiene topes", func(t *testing.T) {
		l := LimitsFor(TierPremium)
		if l != (Limits{Unlimited, Unlimited, Unlimited}) {
			t.Fatalf("topes inesperados: %+v", l)
		}
	})
}

func TestExceeds(t *testing.T) {
	cases := []struct {
		used, adding, max int
		want              bool
	}{
		{2, 1, 3, false}, {3, 1, 3, true}, {0, 4, 3, true}, {0, 3, 3, false}, {99, 99, Unlimited, false},
	}
	for _, c := range cases {
		if got := Exceeds(c.used, c.adding, c.max); got != c.want {
			t.Errorf("Exceeds(%d,%d,%d)=%v", c.used, c.adding, c.max, got)
		}
	}
}

func TestPlanLimitError(t *testing.T) {
	err := error(&PlanLimitError{Resource: ResourceMedications, Limit: 3})
	if !errors.Is(err, ErrPlanLimit) {
		t.Fatal("debe reconocerse como ErrPlanLimit")
	}
	if !strings.Contains(err.Error(), "3 medicamentos") || !strings.Contains(err.Error(), "Premium") {
		t.Fatalf("mensaje inesperado: %s", err)
	}
}
