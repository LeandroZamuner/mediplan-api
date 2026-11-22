package plans

import (
	"context"
	"errors"
	"testing"

	"github.com/lea/mediplan-api/internal/domain"
)

// fakeLimits devuelve los topes por usuario; quien no figura es del nivel gratis.
type fakeLimits map[string]domain.Limits

func (f fakeLimits) LimitsOf(_ context.Context, id string) (domain.Limits, error) {
	if l, ok := f[id]; ok {
		return l, nil
	}
	return domain.LimitsFor(domain.TierFree), nil
}

func givenLimited() (*Service, *fakePlans) {
	repo := &fakePlans{byID: map[string]*domain.Plan{}}
	s := NewService(repo, fakeLinks{})
	s.SetLimits(fakeLimits{"premium": domain.LimitsFor(domain.TierPremium)})
	return s, repo
}

func meds(n int) []MedicationInput {
	out := make([]MedicationInput, n)
	for i := range out {
		out[i] = validMed()
	}
	return out
}

func wantLimit(t *testing.T, err error, resource string, limit int) {
	t.Helper()
	var pl *domain.PlanLimitError
	if !errors.As(err, &pl) || pl.Resource != resource || pl.Limit != limit || !errors.Is(err, domain.ErrPlanLimit) {
		t.Fatalf("esperaba el límite %s=%d y fue %v", resource, limit, err)
	}
}

func TestLimitsCreatePlan(t *testing.T) {
	ctx := context.Background()
	free := &domain.User{ID: "gratis"}
	prem := &domain.User{ID: "premium"}

	t.Run("dado un usuario gratis, cuando crea su primer plan con 3 medicamentos, entonces se crea", func(t *testing.T) {
		s, _ := givenLimited()
		if _, err := s.CreatePlan(ctx, free, "Plan", meds(3)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("dado un usuario gratis, cuando crea un plan con 4 medicamentos, entonces se rechaza por el límite de medicamentos", func(t *testing.T) {
		s, _ := givenLimited()
		_, err := s.CreatePlan(ctx, free, "Plan", meds(4))
		wantLimit(t, err, domain.ResourceMedications, 3)
	})
	t.Run("dado un usuario gratis con un plan activo, cuando crea otro, entonces se rechaza por el límite de planes", func(t *testing.T) {
		s, _ := givenLimited()
		_, _ = s.CreatePlan(ctx, free, "Uno", meds(1))
		_, err := s.CreatePlan(ctx, free, "Dos", meds(1))
		wantLimit(t, err, domain.ResourcePlans, 1)
	})
	t.Run("dado un usuario gratis cuyo plan anterior ya está completado, cuando crea otro, entonces se crea", func(t *testing.T) {
		s, repo := givenLimited()
		p, _ := s.CreatePlan(ctx, free, "Uno", meds(1))
		repo.byID[p.ID].Status = domain.PlanCompleted
		if _, err := s.CreatePlan(ctx, free, "Dos", meds(1)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("dado un usuario Premium, cuando crea varios planes con muchos medicamentos, entonces no hay límite", func(t *testing.T) {
		s, _ := givenLimited()
		for i := 0; i < 3; i++ {
			if _, err := s.CreatePlan(ctx, prem, "Plan", meds(6)); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("dado un servicio sin topes configurados, cuando crea planes, entonces no se limita (compatibilidad)", func(t *testing.T) {
		s, _ := givenService()
		for i := 0; i < 2; i++ {
			if _, err := s.CreatePlan(ctx, free, "Plan", meds(5)); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestLimitsAddMedication(t *testing.T) {
	ctx := context.Background()
	free := &domain.User{ID: "gratis"}

	t.Run("dado un plan gratis con 3 medicamentos, cuando se agrega otro, entonces se rechaza", func(t *testing.T) {
		s, _ := givenLimited()
		p, _ := s.CreatePlan(ctx, free, "Plan", meds(3))
		_, err := s.AddMedication(ctx, free, p.ID, validMed())
		wantLimit(t, err, domain.ResourceMedications, 3)
	})
	t.Run("dado un plan gratis con 2 medicamentos, cuando se agrega otro, entonces se agrega", func(t *testing.T) {
		s, _ := givenLimited()
		p, _ := s.CreatePlan(ctx, free, "Plan", meds(2))
		if _, err := s.AddMedication(ctx, free, p.ID, validMed()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("dado un plan gratis lleno, cuando una cuidadora intenta agregar un medicamento, entonces rige el nivel del dueño y se rechaza", func(t *testing.T) {
		s, _ := givenLimited()
		links := s.links.(fakeLinks)
		p, _ := s.CreatePlan(ctx, free, "Plan", meds(3))
		links.accept("carla", p.ID)
		_, err := s.AddMedication(ctx, &domain.User{ID: "carla"}, p.ID, validMed())
		wantLimit(t, err, domain.ResourceMedications, 3)
	})
	t.Run("dado un plan cuyo dueño es Premium, cuando una cuidadora gratis agrega medicamentos, entonces se agregan (rige el nivel del dueño)", func(t *testing.T) {
		s, _ := givenLimited()
		links := s.links.(fakeLinks)
		prem := &domain.User{ID: "premium"}
		p, _ := s.CreatePlan(ctx, prem, "Plan", meds(3))
		links.accept("carla", p.ID)
		if _, err := s.AddMedication(ctx, &domain.User{ID: "carla"}, p.ID, validMed()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("dado un usuario que ya superaba el tope (datos anteriores), cuando edita un medicamento existente, entonces se permite: los topes solo frenan crear", func(t *testing.T) {
		s, repo := givenLimited()
		p, _ := s.CreatePlan(ctx, free, "Plan", meds(3))
		repo.byID[p.ID].Medications = append(repo.byID[p.ID].Medications, repo.byID[p.ID].Medications[0]) // 4 cargados de antes
		name := "Otro nombre"
		if _, err := s.PatchMedication(ctx, free, p.ID, p.Medications[0].ID, MedicationPatch{Name: &name}); err != nil {
			t.Fatalf("editar no debe bloquearse: %v", err)
		}
	})
}

func TestUsage(t *testing.T) {
	ctx := context.Background()
	free := &domain.User{ID: "gratis"}
	s, repo := givenLimited()
	_, _ = s.CreatePlan(ctx, free, "Activo", meds(2))
	repo.byID["otro"] = &domain.Plan{ID: "otro", PatientID: "gratis", Status: domain.PlanCompleted, Medications: make([]domain.Medication, 4)}

	t.Run("dado un usuario con un plan activo y uno completado, cuando se cuenta el uso, entonces solo cuentan los activos", func(t *testing.T) {
		plans, medsN, err := s.Usage(ctx, "gratis")
		if err != nil || plans != 1 || medsN != 2 {
			t.Fatalf("planes=%d medicamentos=%d err=%v", plans, medsN, err)
		}
	})
}
