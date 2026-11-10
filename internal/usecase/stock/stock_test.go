package stock

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lea/mediplan-api/internal/domain"
)

// ---- fakes ----

type fakeRepo struct{ units map[string]int }

func (f *fakeRepo) Consume(_ context.Context, medID string, n int) (before, after int, err error) {
	u, ok := f.units[medID]
	if !ok {
		return 0, 0, domain.ErrNotFound
	}
	after = u - n
	if after < 0 {
		after = 0
	}
	f.units[medID] = after
	return u, after, nil
}
func (f *fakeRepo) Add(_ context.Context, medID string, n int) (int, error) {
	u, ok := f.units[medID]
	if !ok {
		return 0, domain.ErrNotFound
	}
	f.units[medID] = u + n
	return u + n, nil
}

type sent struct {
	userID string
	n      domain.Notification
}

type fakeNotifier struct {
	sent   []sent
	failTo string
}

func (f *fakeNotifier) Notify(_ context.Context, userID string, n domain.Notification) error {
	if userID == f.failTo {
		return errors.New("falla")
	}
	f.sent = append(f.sent, sent{userID, n})
	return nil
}

type fakeParticipants []string

func (f fakeParticipants) ParticipantIDs(context.Context, *domain.Plan) ([]string, error) {
	return f, nil
}

// fakeAccess hace de plans.Service.GetPlan: ve el plan su dueño y Carla (cuidadora).
type fakeAccess struct{ plan *domain.Plan }

func (f fakeAccess) GetPlan(_ context.Context, actor *domain.User, planID string) (*domain.Plan, error) {
	if planID != f.plan.ID {
		return nil, domain.ErrNotFound
	}
	if actor.ID != "ana" && actor.ID != "carla" {
		return nil, domain.ErrForbidden
	}
	return f.plan, nil
}

// ---- givens ----

var (
	ana   = &domain.User{ID: "ana"}
	carla = &domain.User{ID: "carla"}
	dario = &domain.User{ID: "dario"}
)

type world struct {
	svc      *Service
	repo     *fakeRepo
	notifier *fakeNotifier
	plan     *domain.Plan
	med      domain.Medication
}

// givenWorld: Enalapril 10 mg con `units` en stock y aviso a partir de `alertAt`; participantes: Ana y Carla.
func givenWorld(units, alertAt int) *world {
	med := domain.Medication{ID: "m1", Name: "Enalapril", Dose: "10 mg", StockUnits: units, StockAlertAt: alertAt}
	plan := &domain.Plan{ID: "p1", PatientID: "ana", Name: "Hipertensión", Medications: []domain.Medication{med}}
	w := &world{repo: &fakeRepo{units: map[string]int{"m1": units}}, notifier: &fakeNotifier{}, plan: plan, med: med}
	w.svc = NewService(w.repo, fakeAccess{plan}, fakeParticipants{"ana", "carla"}, w.notifier)
	return w
}

func (w *world) recipients() []string {
	var ids []string
	for _, s := range w.notifier.sent {
		ids = append(ids, s.userID)
	}
	return ids
}

// ---- OnDoseTaken ----

func TestOnDoseTaken(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un stock de 6 con aviso en 5, cuando se toma una dosis, entonces queda en 5 y se avisa 'queda poco' al paciente y a la cuidadora", func(t *testing.T) {
		w := givenWorld(6, 5)
		if err := w.svc.OnDoseTaken(ctx, w.plan, w.med); err != nil {
			t.Fatal(err)
		}
		if w.repo.units["m1"] != 5 {
			t.Fatalf("stock = %d", w.repo.units["m1"])
		}
		if r := w.recipients(); len(r) != 2 || r[0] != "ana" || r[1] != "carla" {
			t.Fatalf("destinatarios: %v", r)
		}
		n := w.notifier.sent[0].n
		if n.Kind != domain.NotifLowStock || n.Data["level"] != "low" || n.Data["remaining"] != "5" || n.Data["planId"] != "p1" || n.Data["medicationId"] != "m1" {
			t.Fatalf("aviso incorrecto: %+v", n)
		}
		for _, want := range []string{"Enalapril", "10 mg", "5", "Hipertensión"} {
			if !strings.Contains(n.Body, want) {
				t.Errorf("el texto %q debería contener %q", n.Body, want)
			}
		}
	})

	t.Run("dado un stock por encima del umbral o ya por debajo, cuando se toma una dosis, entonces no se avisa (solo al cruzar)", func(t *testing.T) {
		for name, c := range map[string][2]int{"7 con umbral 5 (queda 6)": {7, 5}, "5 con umbral 5 (queda 4, ya estaba abajo)": {5, 5}, "20 con umbral 5": {20, 5}} {
			w := givenWorld(c[0], c[1])
			if err := w.svc.OnDoseTaken(ctx, w.plan, w.med); err != nil || len(w.notifier.sent) != 0 {
				t.Errorf("%s: err=%v avisos=%d", name, err, len(w.notifier.sent))
			}
		}
	})

	t.Run("dado un stock de 1, cuando se toma la última dosis, entonces se avisa 'se agotó' (una sola vez, sin repetir el de 'queda poco')", func(t *testing.T) {
		w := givenWorld(1, 5)
		_ = w.svc.OnDoseTaken(ctx, w.plan, w.med)
		if len(w.notifier.sent) != 2 || w.notifier.sent[0].n.Data["level"] != "out" || !strings.Contains(w.notifier.sent[0].n.Body, "agotó") {
			t.Fatalf("avisos: %+v", w.notifier.sent)
		}
		if w.repo.units["m1"] != 0 {
			t.Fatalf("stock = %d", w.repo.units["m1"])
		}
	})

	t.Run("dado un umbral de 0, cuando se toma la última dosis, entonces se avisa que se agotó", func(t *testing.T) {
		w := givenWorld(1, 0)
		_ = w.svc.OnDoseTaken(ctx, w.plan, w.med)
		if len(w.notifier.sent) != 2 || w.notifier.sent[0].n.Data["level"] != "out" {
			t.Fatalf("avisos: %+v", w.notifier.sent)
		}
	})

	t.Run("dado un stock en 0, cuando se toma otra dosis, entonces sigue en 0, no hay error y no se avisa de nuevo", func(t *testing.T) {
		w := givenWorld(0, 5)
		if err := w.svc.OnDoseTaken(ctx, w.plan, w.med); err != nil || w.repo.units["m1"] != 0 || len(w.notifier.sent) != 0 {
			t.Fatalf("err=%v stock=%d avisos=%d", err, w.repo.units["m1"], len(w.notifier.sent))
		}
	})

	t.Run("dada una falla al avisar a uno de los destinatarios, cuando se cruza el umbral, entonces el otro igual recibe el aviso y no se propaga el error", func(t *testing.T) {
		w := givenWorld(6, 5)
		w.notifier.failTo = "ana"
		if err := w.svc.OnDoseTaken(ctx, w.plan, w.med); err != nil {
			t.Fatalf("no debería fallar: %v", err)
		}
		if r := w.recipients(); len(r) != 1 || r[0] != "carla" {
			t.Fatalf("destinatarios: %v", r)
		}
	})

	t.Run("dado un medicamento inexistente en la base, cuando se toma una dosis, entonces devuelve el error", func(t *testing.T) {
		w := givenWorld(6, 5)
		w.med.ID = "otro"
		if err := w.svc.OnDoseTaken(ctx, w.plan, w.med); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
	})
}

// ---- Refill ----

func TestRefill(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un medicamento con 3 unidades, cuando el paciente o su cuidadora reponen 30, entonces suma y devuelve el total", func(t *testing.T) {
		for _, u := range []*domain.User{ana, carla} {
			w := givenWorld(3, 5)
			got, err := w.svc.Refill(ctx, u, "p1", "m1", 30)
			if err != nil || got != 33 || w.repo.units["m1"] != 33 {
				t.Errorf("%s: got %d err=%v", u.ID, got, err)
			}
		}
	})

	t.Run("dado alguien sin vínculo con el plan, cuando intenta reponer, entonces 403 y el stock no cambia", func(t *testing.T) {
		w := givenWorld(3, 5)
		if _, err := w.svc.Refill(ctx, dario, "p1", "m1", 30); !errors.Is(err, domain.ErrForbidden) || w.repo.units["m1"] != 3 {
			t.Fatalf("err=%v stock=%d", err, w.repo.units["m1"])
		}
	})

	t.Run("dadas cantidades inválidas (cero, negativa o exagerada), cuando se repone, entonces es rechazado", func(t *testing.T) {
		w := givenWorld(3, 5)
		for _, n := range []int{0, -5, 100001} {
			if _, err := w.svc.Refill(ctx, ana, "p1", "m1", n); !errors.Is(err, domain.ErrInvalidStock) {
				t.Errorf("%d: got %v", n, err)
			}
		}
		if w.repo.units["m1"] != 3 {
			t.Fatalf("el stock no debería cambiar: %d", w.repo.units["m1"])
		}
	})

	t.Run("dado un plan o medicamento inexistente, cuando se repone, entonces no se encuentra", func(t *testing.T) {
		w := givenWorld(3, 5)
		if _, err := w.svc.Refill(ctx, ana, "nope", "m1", 5); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("plan: %v", err)
		}
		if _, err := w.svc.Refill(ctx, ana, "p1", "nope", 5); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("medicamento: %v", err)
		}
	})
}
