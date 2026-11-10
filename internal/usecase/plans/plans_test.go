package plans

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

// ---- fakes ----

type fakePlans struct {
	byID map[string]*domain.Plan
	seq  int
}

func (f *fakePlans) store(p *domain.Plan) {
	for i := range p.Medications {
		if p.Medications[i].ID == "" {
			f.seq++
			p.Medications[i].ID = fmt.Sprintf("m%d", f.seq)
		}
	}
	c := *p
	c.Medications = append([]domain.Medication(nil), p.Medications...)
	f.byID[p.ID] = &c
}
func (f *fakePlans) Create(_ context.Context, p *domain.Plan) error {
	f.seq++
	p.ID = fmt.Sprintf("plan%d", f.seq)
	f.store(p)
	return nil
}

// Update imita al repositorio real: NO toca el stock de los medicamentos que ya existen
// (el stock solo cambia por descuento, reposición o ajuste explícito).
func (f *fakePlans) Update(_ context.Context, p *domain.Plan) error {
	old, ok := f.byID[p.ID]
	if !ok {
		return domain.ErrNotFound
	}
	stored := map[string]int{}
	for _, m := range old.Medications {
		stored[m.ID] = m.StockUnits
	}
	for i := range p.Medications {
		if u, exists := stored[p.Medications[i].ID]; exists {
			p.Medications[i].StockUnits = u
		}
	}
	f.store(p)
	return nil
}

func (f *fakePlans) SetMedicationStock(_ context.Context, medID string, units int) error {
	for _, p := range f.byID {
		for i := range p.Medications {
			if p.Medications[i].ID == medID {
				p.Medications[i].StockUnits = units
				return nil
			}
		}
	}
	return domain.ErrNotFound
}

// descontar simula una toma que baja el stock en la base (sin pasar por el servicio de planes).
func (f *fakePlans) descontar(planID, medID string, n int) {
	for i := range f.byID[planID].Medications {
		if f.byID[planID].Medications[i].ID == medID {
			f.byID[planID].Medications[i].StockUnits -= n
		}
	}
}
func (f *fakePlans) Delete(_ context.Context, id string) error {
	if _, ok := f.byID[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.byID, id)
	return nil
}
func (f *fakePlans) ByID(_ context.Context, id string) (*domain.Plan, error) {
	if p, ok := f.byID[id]; ok {
		c := *p
		c.Medications = append([]domain.Medication(nil), p.Medications...)
		return &c, nil
	}
	return nil, domain.ErrNotFound
}
func (f *fakePlans) ByPatient(_ context.Context, patientID string) ([]*domain.Plan, error) {
	var out []*domain.Plan
	for _, p := range f.byID {
		if p.PatientID == patientID {
			c := *p
			out = append(out, &c)
		}
	}
	return out, nil
}

// fakeLinks guarda los cuidadores aceptados por plan: "userID>planID".
type fakeLinks map[string]bool

func (f fakeLinks) accept(userID, planID string) { f[userID+">"+planID] = true }
func (f fakeLinks) IsAcceptedCaregiver(_ context.Context, userID, planID string) (bool, error) {
	return f[userID+">"+planID], nil
}
func (f fakeLinks) HasAcceptedCaregiver(_ context.Context, planID string) (bool, error) {
	for k := range f {
		if strings.HasSuffix(k, ">"+planID) {
			return true, nil
		}
	}
	return false, nil
}
func (f fakeLinks) AcceptedCaregiverIDs(_ context.Context, planID string) ([]string, error) {
	var ids []string
	for k := range f {
		if strings.HasSuffix(k, ">"+planID) {
			ids = append(ids, strings.TrimSuffix(k, ">"+planID))
		}
	}
	sort.Strings(ids)
	return ids, nil
}
func (f fakeLinks) SupervisedPlanIDs(_ context.Context, userID string) ([]string, error) {
	var ids []string
	for k := range f {
		if strings.HasPrefix(k, userID+">") {
			ids = append(ids, strings.TrimPrefix(k, userID+">"))
		}
	}
	return ids, nil
}

// ---- givens ----
// Ser "cuidador" no es un rol de la cuenta: es estar vinculado a un plan. Todos son usuarios iguales.

var (
	ana   = &domain.User{ID: "ana"}
	lucia = &domain.User{ID: "lucia"}
	carla = &domain.User{ID: "carla"}
	dario = &domain.User{ID: "dario"}
)

func givenService() (*Service, fakeLinks) {
	links := fakeLinks{}
	return NewService(&fakePlans{byID: map[string]*domain.Plan{}}, links), links
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func validMed() MedicationInput {
	return MedicationInput{
		Name: "Enalapril", Dose: "10 mg", StartDate: day(2026, 11, 1),
		Schedules:  []domain.Schedule{{DayOfWeek: 1, TimeOfDay: "08:00"}, {DayOfWeek: 4, TimeOfDay: "20:30"}},
		StockUnits: 30, StockAlertAt: 5,
	}
}

// givenPlanOf crea un plan con dos medicamentos, hecho por su dueño.
func givenPlanOf(t *testing.T, s *Service, owner *domain.User) *domain.Plan {
	t.Helper()
	other := validMed()
	other.Name = "Aspirina"
	p, err := s.CreatePlan(context.Background(), owner, "Hipertensión", []MedicationInput{validMed(), other})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- CreatePlan ----

func TestCreatePlan(t *testing.T) {
	ctx := context.Background()

	t.Run("dado cualquier usuario, cuando crea un plan válido, entonces queda activo y a su nombre", func(t *testing.T) {
		s, _ := givenService()
		for _, u := range []*domain.User{lucia, carla} { // carla también puede tener sus propios planes
			p, err := s.CreatePlan(ctx, u, "Plan", []MedicationInput{validMed()})
			if err != nil || p.PatientID != u.ID || p.Status != domain.PlanActive || len(p.Medications) != 1 {
				t.Fatalf("%s: plan inesperado %+v err=%v", u.ID, p, err)
			}
		}
	})

	invalid := map[string]func(m *MedicationInput){
		"sin nombre":                       func(m *MedicationInput) { m.Name = " " },
		"sin dosis":                        func(m *MedicationInput) { m.Dose = "" },
		"sin horarios":                     func(m *MedicationInput) { m.Schedules = nil },
		"con día de semana fuera de 0 a 6": func(m *MedicationInput) { m.Schedules[0].DayOfWeek = 7 },
		"con hora mal formada":             func(m *MedicationInput) { m.Schedules[0].TimeOfDay = "25:99" },
		"con fin anterior al inicio":       func(m *MedicationInput) { e := day(2026, 10, 1); m.EndDate = &e },
		"con stock negativo":               func(m *MedicationInput) { m.StockUnits = -1 },
	}
	for name, mutate := range invalid {
		t.Run("dado un medicamento "+name+", cuando se crea el plan, entonces es rechazado", func(t *testing.T) {
			s, _ := givenService()
			m := validMed()
			m.Schedules = append([]domain.Schedule(nil), m.Schedules...)
			mutate(&m)
			if _, err := s.CreatePlan(ctx, lucia, "Plan", []MedicationInput{m}); !errors.Is(err, domain.ErrInvalidPlan) {
				t.Fatalf("esperaba ErrInvalidPlan, got %v", err)
			}
		})
	}

	t.Run("dado un plan sin nombre o sin medicamentos, cuando se crea, entonces es rechazado", func(t *testing.T) {
		s, _ := givenService()
		_, e1 := s.CreatePlan(ctx, lucia, " ", []MedicationInput{validMed()})
		_, e2 := s.CreatePlan(ctx, lucia, "Plan", nil)
		if !errors.Is(e1, domain.ErrInvalidPlan) || !errors.Is(e2, domain.ErrInvalidPlan) {
			t.Fatalf("esperaba ErrInvalidPlan en ambos, got %v / %v", e1, e2)
		}
	})
}

// ---- AddMedication: crear está permitido al dueño y a los cuidadores DE ESE plan ----

func TestAddMedication(t *testing.T) {
	ctx := context.Background()
	nuevo := validMed()
	nuevo.Name = "Atorvastatina"

	t.Run("dado un plan, cuando agregan un medicamento su dueño (con o sin cuidador) o su cuidador, entonces queda en el plan", func(t *testing.T) {
		for _, c := range []struct {
			quien       string
			conCuidador bool
			actor       *domain.User
		}{
			{"dueño sin cuidador", false, ana},
			{"dueño con cuidador", true, ana},
			{"cuidador del plan", true, carla},
		} {
			s, links := givenService()
			p := givenPlanOf(t, s, ana)
			if c.conCuidador {
				links.accept("carla", p.ID)
			}
			got, err := s.AddMedication(ctx, c.actor, p.ID, nuevo)
			if err != nil || len(got.Medications) != 3 || got.Medications[2].Name != "Atorvastatina" {
				t.Errorf("%s: got %+v err=%v", c.quien, got, err)
			}
		}
	})

	t.Run("dado un plan de Ana, cuando agrega un usuario sin vínculo a ESE plan, entonces no tiene permiso", func(t *testing.T) {
		s, links := givenService()
		p := givenPlanOf(t, s, ana)
		otro := givenPlanOf(t, s, ana)
		links.accept("carla", otro.ID) // carla cuida otro plan de Ana, no este
		for _, u := range []*domain.User{dario, carla} {
			if _, err := s.AddMedication(ctx, u, p.ID, nuevo); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("%s: esperaba ErrForbidden, got %v", u.ID, err)
			}
		}
	})
}

// ---- Editar y eliminar: cuidador del plan, o el dueño SOLO si ese plan no tiene cuidador ----

type modifyAction struct {
	name string
	run  func(s *Service, actor *domain.User, p *domain.Plan) error
}

var modifyActions = []modifyAction{
	{"renombrar el plan", func(s *Service, a *domain.User, p *domain.Plan) error {
		_, err := s.RenamePlan(context.Background(), a, p.ID, "Nuevo nombre")
		return err
	}},
	{"editar un medicamento", func(s *Service, a *domain.User, p *domain.Plan) error {
		m := validMed()
		m.Dose = "20 mg"
		_, err := s.UpdateMedication(context.Background(), a, p.ID, p.Medications[0].ID, m)
		return err
	}},
	{"editar parcialmente un medicamento", func(s *Service, a *domain.User, p *domain.Plan) error {
		dose := "15 mg"
		_, err := s.PatchMedication(context.Background(), a, p.ID, p.Medications[0].ID, MedicationPatch{Dose: &dose})
		return err
	}},
	{"eliminar un medicamento", func(s *Service, a *domain.User, p *domain.Plan) error {
		_, err := s.RemoveMedication(context.Background(), a, p.ID, p.Medications[0].ID)
		return err
	}},
	{"eliminar el plan", func(s *Service, a *domain.User, p *domain.Plan) error {
		return s.DeletePlan(context.Background(), a, p.ID)
	}},
}

func TestModifyPermissions(t *testing.T) {
	for _, act := range modifyActions {
		t.Run("dado un plan sin cuidador, cuando su dueño quiere "+act.name+", entonces puede", func(t *testing.T) {
			s, _ := givenService()
			p := givenPlanOf(t, s, lucia)
			if err := act.run(s, lucia, p); err != nil {
				t.Fatalf("debería poder: %v", err)
			}
		})
		t.Run("dado un plan con cuidador, cuando su dueño quiere "+act.name+", entonces no tiene permiso (lo asume el cuidador)", func(t *testing.T) {
			s, links := givenService()
			p := givenPlanOf(t, s, ana)
			links.accept("carla", p.ID)
			if err := act.run(s, ana, p); !errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("esperaba ErrForbidden, got %v", err)
			}
		})
		t.Run("dado un plan con cuidador, cuando el cuidador quiere "+act.name+", entonces puede", func(t *testing.T) {
			s, links := givenService()
			p := givenPlanOf(t, s, ana)
			links.accept("carla", p.ID)
			if err := act.run(s, carla, p); err != nil {
				t.Fatalf("debería poder: %v", err)
			}
		})
		t.Run("dado un plan de Ana, cuando alguien sin vínculo a ese plan quiere "+act.name+", entonces no tiene permiso", func(t *testing.T) {
			s, links := givenService()
			p := givenPlanOf(t, s, ana)
			otro := givenPlanOf(t, s, ana)
			links.accept("carla", otro.ID)
			for _, u := range []*domain.User{dario, carla} {
				if err := act.run(s, u, p); !errors.Is(err, domain.ErrForbidden) {
					t.Errorf("%s: esperaba ErrForbidden, got %v", u.ID, err)
				}
			}
		})
	}

	t.Run("dado que Ana tiene un plan con cuidador y otro sin, cuando Ana edita el que no tiene cuidador, entonces puede", func(t *testing.T) {
		s, links := givenService()
		conCuidador := givenPlanOf(t, s, ana)
		sinCuidador := givenPlanOf(t, s, ana)
		links.accept("carla", conCuidador.ID)
		if _, err := s.RenamePlan(context.Background(), ana, sinCuidador.ID, "Mío"); err != nil {
			t.Fatalf("la regla es por plan, debería poder: %v", err)
		}
	})
}

func TestModifyResults(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un plan, cuando se edita un medicamento, entonces cambian sus datos y los demás quedan igual", func(t *testing.T) {
		s, _ := givenService()
		p := givenPlanOf(t, s, lucia)
		m := validMed()
		m.Dose = "20 mg"
		got, err := s.UpdateMedication(ctx, lucia, p.ID, p.Medications[0].ID, m)
		if err != nil || got.Medications[0].Dose != "20 mg" || got.Medications[1].Name != "Aspirina" {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("dado un medicamento inexistente, cuando se edita, entonces no se encuentra", func(t *testing.T) {
		s, _ := givenService()
		p := givenPlanOf(t, s, lucia)
		if _, err := s.UpdateMedication(ctx, lucia, p.ID, "nope", validMed()); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("esperaba ErrNotFound, got %v", err)
		}
	})

	t.Run("dado un plan con un solo medicamento, cuando se elimina ese medicamento, entonces es rechazado", func(t *testing.T) {
		s, _ := givenService()
		p, _ := s.CreatePlan(ctx, lucia, "Plan", []MedicationInput{validMed()})
		if _, err := s.RemoveMedication(ctx, lucia, p.ID, p.Medications[0].ID); !errors.Is(err, domain.ErrInvalidPlan) {
			t.Fatalf("esperaba ErrInvalidPlan, got %v", err)
		}
	})

	t.Run("dado un plan eliminado, cuando se consulta, entonces no se encuentra", func(t *testing.T) {
		s, _ := givenService()
		p := givenPlanOf(t, s, lucia)
		_ = s.DeletePlan(ctx, lucia, p.ID)
		if _, err := s.GetPlan(ctx, lucia, p.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("esperaba ErrNotFound, got %v", err)
		}
	})
}

// ---- GetPlan / ListPlans / ListSupervised ----

func TestView(t *testing.T) {
	ctx := context.Background()

	t.Run("dado el plan de Ana, cuando lo consultan Ana y su cuidadora de ESE plan, entonces lo ven", func(t *testing.T) {
		s, links := givenService()
		p := givenPlanOf(t, s, ana)
		links.accept("carla", p.ID)
		for _, u := range []*domain.User{ana, carla} {
			if got, err := s.GetPlan(ctx, u, p.ID); err != nil || got.ID != p.ID {
				t.Errorf("%s: got %+v err=%v", u.ID, got, err)
			}
		}
	})

	t.Run("dado que Carla cuida un plan de Ana, cuando consulta OTRO plan de Ana, entonces no tiene permiso", func(t *testing.T) {
		s, links := givenService()
		suyo := givenPlanOf(t, s, ana)
		otro := givenPlanOf(t, s, ana)
		links.accept("carla", suyo.ID)
		if _, err := s.GetPlan(ctx, carla, otro.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("esperaba ErrForbidden, got %v", err)
		}
	})

	t.Run("dado el plan de Ana, cuando lo consulta alguien sin vínculo, entonces no tiene permiso", func(t *testing.T) {
		s, _ := givenService()
		p := givenPlanOf(t, s, ana)
		for _, u := range []*domain.User{lucia, dario} {
			if _, err := s.GetPlan(ctx, u, p.ID); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("%s: esperaba ErrForbidden, got %v", u.ID, err)
			}
		}
	})

	t.Run("dado un plan inexistente, cuando se consulta, entonces no se encuentra", func(t *testing.T) {
		s, _ := givenService()
		if _, err := s.GetPlan(ctx, ana, "nope"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("esperaba ErrNotFound, got %v", err)
		}
	})

	t.Run("dado que Ana tiene dos planes, cuando lista sus planes, entonces ve los dos; Carla (sin planes propios) ninguno", func(t *testing.T) {
		s, links := givenService()
		p := givenPlanOf(t, s, ana)
		givenPlanOf(t, s, ana)
		links.accept("carla", p.ID)
		propios, err := s.ListPlans(ctx, ana)
		deCarla, err2 := s.ListPlans(ctx, carla)
		if err != nil || err2 != nil || len(propios) != 2 || len(deCarla) != 0 {
			t.Fatalf("esperaba 2 y 0, got %d (%v) y %d (%v)", len(propios), err, len(deCarla), err2)
		}
	})

	t.Run("dado que Carla cuida un plan de Ana y otro de Lucía, cuando lista los planes que supervisa, entonces ve solo esos dos", func(t *testing.T) {
		s, links := givenService()
		a := givenPlanOf(t, s, ana)
		givenPlanOf(t, s, ana) // no lo cuida
		l := givenPlanOf(t, s, lucia)
		links.accept("carla", a.ID)
		links.accept("carla", l.ID)
		got, err := s.ListSupervised(ctx, carla)
		if err != nil || len(got) != 2 {
			t.Fatalf("esperaba 2, got %d err=%v", len(got), err)
		}
	})
}

// ---- Sincronización de alarmas ----

type syncCall struct {
	users  []string
	planID string
}

type fakeSyncer struct{ calls []syncCall }

func (f *fakeSyncer) Sync(_ context.Context, userIDs []string, planID string) {
	f.calls = append(f.calls, syncCall{userIDs, planID})
}

func givenServiceWithSync() (*Service, fakeLinks, *fakeSyncer) {
	s, links := givenService()
	sync := &fakeSyncer{}
	s.SetSyncer(sync)
	return s, links, sync
}

func TestSyncOnChanges(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un plan de Ana con Carla de cuidadora, cuando Carla edita un medicamento, entonces se avisa a Ana y a Carla para que reprogramen sus alarmas", func(t *testing.T) {
		s, links, sync := givenServiceWithSync()
		p := givenPlanOf(t, s, ana)
		links.accept("carla", p.ID)
		sync.calls = nil // ignora lo de la creación
		m := validMed()
		m.Dose = "20 mg"
		if _, err := s.UpdateMedication(ctx, carla, p.ID, p.Medications[0].ID, m); err != nil {
			t.Fatal(err)
		}
		if len(sync.calls) != 1 || sync.calls[0].planID != p.ID || len(sync.calls[0].users) != 2 || sync.calls[0].users[0] != "ana" || sync.calls[0].users[1] != "carla" {
			t.Fatalf("calls: %+v", sync.calls)
		}
	})

	t.Run("dadas las demás operaciones que cambian horarios (crear, agregar, quitar medicamento, eliminar el plan), cuando ocurren, entonces cada una sincroniza", func(t *testing.T) {
		s, _, sync := givenServiceWithSync()
		p := givenPlanOf(t, s, lucia)
		if len(sync.calls) != 1 {
			t.Fatalf("crear: %+v", sync.calls)
		}
		nuevo := validMed()
		nuevo.Name = "Otro"
		p, _ = s.AddMedication(ctx, lucia, p.ID, nuevo)
		p, _ = s.RemoveMedication(ctx, lucia, p.ID, p.Medications[0].ID)
		if len(sync.calls) != 3 {
			t.Fatalf("agregar y quitar: %+v", sync.calls)
		}
		if err := s.DeletePlan(ctx, lucia, p.ID); err != nil || len(sync.calls) != 4 || sync.calls[3].planID != p.ID {
			t.Fatalf("eliminar: err=%v calls=%+v", err, sync.calls)
		}
	})

	t.Run("dado un plan eliminado por la cuidadora, cuando se sincroniza, entonces igual se avisa a Ana (los participantes se toman antes de borrar)", func(t *testing.T) {
		s, links, sync := givenServiceWithSync()
		p := givenPlanOf(t, s, ana)
		links.accept("carla", p.ID)
		sync.calls = nil
		_ = s.DeletePlan(ctx, carla, p.ID)
		if len(sync.calls) != 1 || len(sync.calls[0].users) != 2 {
			t.Fatalf("calls: %+v", sync.calls)
		}
	})

	t.Run("dada una operación rechazada por permisos o por validación, cuando falla, entonces no se sincroniza nada", func(t *testing.T) {
		s, links, sync := givenServiceWithSync()
		p := givenPlanOf(t, s, ana)
		links.accept("carla", p.ID)
		sync.calls = nil
		_, _ = s.RenamePlan(ctx, ana, p.ID, "no puede")                              // Ana ya no edita
		_, _ = s.UpdateMedication(ctx, dario, p.ID, p.Medications[0].ID, validMed()) // sin vínculo
		bad := validMed()
		bad.Dose = ""
		_, _ = s.AddMedication(ctx, carla, p.ID, bad) // inválido
		if len(sync.calls) != 0 {
			t.Fatalf("no debería haber sincronizaciones: %+v", sync.calls)
		}
	})

	t.Run("dado un servicio sin sincronizador configurado, cuando se edita un plan, entonces funciona igual", func(t *testing.T) {
		s, _ := givenService()
		p := givenPlanOf(t, s, lucia)
		if _, err := s.RenamePlan(ctx, lucia, p.ID, "Nuevo"); err != nil {
			t.Fatal(err)
		}
	})
}

// ---- PatchMedication: cambia solo lo que se manda ----

func ptr[T any](v T) *T { return &v }

// givenPlanForPatch: plan de Lucía (sin cuidador) con Enalapril 10 mg, stock 30, aviso en 5, lunes 08:00 y jueves 20:30, hasta fin de año.
func givenPlanForPatch(t *testing.T) (*Service, *domain.Plan, *fakePlans) {
	t.Helper()
	s, _ := givenService()
	p := givenPlanOf(t, s, lucia)
	return s, p, s.plans.(*fakePlans)
}

func TestPatchMedication(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un medicamento, cuando se cambia solo la dosis, entonces lo demás (nombre, horarios, fechas, stock) queda igual y conserva su id", func(t *testing.T) {
		s, p, _ := givenPlanForPatch(t)
		antes := p.Medications[0]
		got, err := s.PatchMedication(ctx, lucia, p.ID, antes.ID, MedicationPatch{Dose: ptr("20 mg")})
		if err != nil {
			t.Fatal(err)
		}
		m := got.Medications[0]
		if m.ID != antes.ID || m.Dose != "20 mg" || m.Name != antes.Name || len(m.Schedules) != len(antes.Schedules) ||
			!m.StartDate.Equal(antes.StartDate) || m.StockUnits != antes.StockUnits || m.StockAlertAt != antes.StockAlertAt {
			t.Fatalf("antes=%+v después=%+v", antes, m)
		}
		if got.Medications[1].Name != "Aspirina" {
			t.Fatal("el otro medicamento no debería cambiar")
		}
	})

	t.Run("dado un medicamento, cuando se mandan varios campos a la vez, entonces cambian todos y solo esos", func(t *testing.T) {
		s, p, _ := givenPlanForPatch(t)
		got, err := s.PatchMedication(ctx, lucia, p.ID, p.Medications[0].ID, MedicationPatch{
			Name: ptr("Enalapril forte"), StockAlertAt: ptr(8),
			Schedules: &[]domain.Schedule{{DayOfWeek: 6, TimeOfDay: "07:15"}},
		})
		m := got.Medications[0]
		if err != nil || m.Name != "Enalapril forte" || m.StockAlertAt != 8 || len(m.Schedules) != 1 || m.Schedules[0].TimeOfDay != "07:15" || m.Dose != "10 mg" {
			t.Fatalf("got %+v err=%v", m, err)
		}
	})

	t.Run("dado un medicamento con fecha de fin, cuando se manda null, entonces se borra; cuando se manda una fecha, entonces se fija", func(t *testing.T) {
		s, p, _ := givenPlanForPatch(t)
		medID := p.Medications[0].ID
		fin := day(2026, 12, 31)
		got, err := s.PatchMedication(ctx, lucia, p.ID, medID, MedicationPatch{EndDate: &fin})
		if err != nil || got.Medications[0].EndDate == nil || !got.Medications[0].EndDate.Equal(fin) {
			t.Fatalf("fijar: %+v err=%v", got.Medications[0].EndDate, err)
		}
		got, err = s.PatchMedication(ctx, lucia, p.ID, medID, MedicationPatch{ClearEndDate: true})
		if err != nil || got.Medications[0].EndDate != nil {
			t.Fatalf("borrar: %+v err=%v", got.Medications[0].EndDate, err)
		}
	})

	t.Run("dado un valor que dejaría el medicamento inválido, cuando se manda, entonces es rechazado y no cambia nada", func(t *testing.T) {
		s, p, _ := givenPlanForPatch(t)
		medID := p.Medications[0].ID
		antes := day(2026, 10, 1) // antes del inicio (1/nov)
		mal := map[string]MedicationPatch{
			"nombre vacío":           {Name: ptr("  ")},
			"dosis vacía":            {Dose: ptr("")},
			"sin horarios":           {Schedules: &[]domain.Schedule{}},
			"hora mal formada":       {Schedules: &[]domain.Schedule{{DayOfWeek: 1, TimeOfDay: "25:99"}}},
			"fin anterior al inicio": {EndDate: &antes},
			"stock negativo":         {StockUnits: ptr(-1)},
			"umbral negativo":        {StockAlertAt: ptr(-1)},
			"fin y borrar fin":       {EndDate: ptr(day(2026, 12, 1)), ClearEndDate: true},
			"parche vacío":           {},
		}
		for name, patch := range mal {
			if _, err := s.PatchMedication(ctx, lucia, p.ID, medID, patch); !errors.Is(err, domain.ErrInvalidPlan) {
				t.Errorf("%s: esperaba ErrInvalidPlan, got %v", name, err)
			}
		}
		if got, _ := s.GetPlan(ctx, lucia, p.ID); got.Medications[0].Dose != "10 mg" || got.Medications[0].StockUnits != 30 {
			t.Fatalf("no debería haber cambiado: %+v", got.Medications[0])
		}
	})

	t.Run("dado un medicamento inexistente, cuando se edita parcialmente, entonces no se encuentra", func(t *testing.T) {
		s, p, _ := givenPlanForPatch(t)
		if _, err := s.PatchMedication(ctx, lucia, p.ID, "nope", MedicationPatch{Dose: ptr("1 mg")}); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestPatchDoesNotClobberStock(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un descuento de stock que ocurre entre que se lee y se guarda, cuando se edita otro campo, entonces el descuento no se pierde", func(t *testing.T) {
		s, p, repo := givenPlanForPatch(t)
		medID := p.Medications[0].ID
		// la app leyó el plan con stock 30; mientras tanto se tomó una dosis (30 → 29)
		repo.descontar(p.ID, medID, 1)
		if _, err := s.PatchMedication(ctx, lucia, p.ID, medID, MedicationPatch{Dose: ptr("20 mg")}); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.GetPlan(ctx, lucia, p.ID); got.Medications[0].StockUnits != 29 {
			t.Fatalf("el stock debería seguir en 29, está en %d", got.Medications[0].StockUnits)
		}
	})

	t.Run("dado un ajuste explícito de stock, cuando se manda stockUnits, entonces se fija ese valor exacto", func(t *testing.T) {
		s, p, _ := givenPlanForPatch(t)
		got, err := s.PatchMedication(ctx, lucia, p.ID, p.Medications[0].ID, MedicationPatch{StockUnits: ptr(12)})
		if err != nil || got.Medications[0].StockUnits != 12 || got.Medications[0].Dose != "10 mg" {
			t.Fatalf("got %+v err=%v", got.Medications[0], err)
		}
	})

	t.Run("dada la edición completa (PUT), cuando se manda el stock, entonces también se aplica de forma explícita", func(t *testing.T) {
		s, p, _ := givenPlanForPatch(t)
		m := validMed()
		m.StockUnits = 7
		got, err := s.UpdateMedication(ctx, lucia, p.ID, p.Medications[0].ID, m)
		if err != nil || got.Medications[0].StockUnits != 7 {
			t.Fatalf("got %+v err=%v", got.Medications[0], err)
		}
	})
}

func TestPatchSync(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un cambio que afecta lo que muestra la alarma (nombre, dosis, horarios o fechas), cuando se edita, entonces se sincroniza; si solo cambia el stock, no", func(t *testing.T) {
		s, _, sync := givenServiceWithSync()
		p := givenPlanOf(t, s, lucia)
		medID := p.Medications[0].ID
		sync.calls = nil
		_, _ = s.PatchMedication(ctx, lucia, p.ID, medID, MedicationPatch{Dose: ptr("20 mg")})
		_, _ = s.PatchMedication(ctx, lucia, p.ID, medID, MedicationPatch{Schedules: &[]domain.Schedule{{DayOfWeek: 2, TimeOfDay: "09:00"}}})
		if len(sync.calls) != 2 {
			t.Fatalf("esperaba 2 sincronizaciones, got %d", len(sync.calls))
		}
		_, _ = s.PatchMedication(ctx, lucia, p.ID, medID, MedicationPatch{StockUnits: ptr(50)})
		_, _ = s.PatchMedication(ctx, lucia, p.ID, medID, MedicationPatch{StockAlertAt: ptr(9)})
		if len(sync.calls) != 2 {
			t.Fatalf("el stock no debería sincronizar: %d", len(sync.calls))
		}
	})
}
