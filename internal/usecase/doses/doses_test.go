package doses

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

// ---- fakes ----

// fakePlans hace de plans.Service.GetPlan: ve el plan su dueño y los cuidadores listados.
type fakePlans struct {
	plans      map[string]*domain.Plan
	caregivers map[string]bool // "userID>planID"
}

func (f fakePlans) GetPlan(_ context.Context, actor *domain.User, planID string) (*domain.Plan, error) {
	p, ok := f.plans[planID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if actor.ID != p.PatientID && !f.caregivers[actor.ID+">"+planID] {
		return nil, domain.ErrForbidden
	}
	return p, nil
}

type fakeZones map[string]string

func (f fakeZones) TimezoneOf(_ context.Context, userID string) (string, error) {
	if tz, ok := f[userID]; ok {
		return tz, nil
	}
	return "", domain.ErrNotFound
}

type fakeLogs struct{ rows map[string]*domain.DoseLog } // clave: medID|instante

func key(medID string, at time.Time) string { return medID + "|" + at.UTC().Format(time.RFC3339) }

func (f *fakeLogs) Get(_ context.Context, medID string, at time.Time) (*domain.DoseLog, error) {
	if l, ok := f.rows[key(medID, at)]; ok {
		c := *l
		return &c, nil
	}
	return nil, domain.ErrNotFound
}
func (f *fakeLogs) Save(_ context.Context, l *domain.DoseLog) error {
	l.ID = "log-" + key(l.MedicationID, l.ScheduledAt)
	c := *l
	f.rows[key(l.MedicationID, l.ScheduledAt)] = &c
	return nil
}
func (f *fakeLogs) Between(_ context.Context, medIDs []string, from, to time.Time) ([]*domain.DoseLog, error) {
	var out []*domain.DoseLog
	for _, l := range f.rows {
		for _, id := range medIDs {
			if l.MedicationID == id && !l.ScheduledAt.Before(from) && l.ScheduledAt.Before(to) {
				c := *l
				out = append(out, &c)
			}
		}
	}
	return out, nil
}

// ---- givens ----
// Plan de Ana (Buenos Aires, UTC-3) creado el 1/nov: Enalapril, lunes 08:00 y jueves 20:30.
// "Ahora" es jueves 12/nov 12:00 hora local (15:00 UTC): el lunes 9 ya pasó y el jueves a las 20:30 todavía no.

func utc(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, time.UTC) }

var (
	ana   = &domain.User{ID: "ana"}
	carla = &domain.User{ID: "carla"}
	dario = &domain.User{ID: "dario"}
)

type world struct {
	svc  *Service
	logs *fakeLogs
	now  time.Time
}

func givenWorld() *world {
	w := &world{logs: &fakeLogs{rows: map[string]*domain.DoseLog{}}, now: utc(12, 15, 0)}
	plan := &domain.Plan{
		ID: "p1", PatientID: "ana", Name: "Hipertensión", Status: domain.PlanActive, CreatedAt: utc(1, 0, 0),
		Medications: []domain.Medication{{
			ID: "m1", Name: "Enalapril", StartDate: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
			Schedules: []domain.Schedule{{DayOfWeek: 1, TimeOfDay: "08:00"}, {DayOfWeek: 4, TimeOfDay: "20:30"}},
		}},
	}
	plans := fakePlans{plans: map[string]*domain.Plan{"p1": plan}, caregivers: map[string]bool{"carla>p1": true}}
	w.svc = NewService(plans, fakeZones{"ana": "America/Argentina/Buenos_Aires"}, w.logs)
	w.svc.now = func() time.Time { return w.now }
	return w
}

// ---- RecordTaken ----

func TestRecordTaken(t *testing.T) {
	ctx := context.Background()
	lunes := utc(9, 11, 0) // lunes 9 08:00 local

	t.Run("dada la toma del lunes 08:00, cuando Ana la registra, entonces queda tomada con la hora actual", func(t *testing.T) {
		w := givenWorld()
		l, err := w.svc.RecordTaken(ctx, ana, "p1", "m1", lunes, nil)
		if err != nil || l.Status != domain.DoseTaken || l.TakenAt == nil || !l.TakenAt.Equal(w.now) || !l.ScheduledAt.Equal(lunes) {
			t.Fatalf("got %+v err=%v", l, err)
		}
	})

	t.Run("dada una toma hecha sin internet, cuando la app la sincroniza con la hora real, entonces se respeta esa hora", func(t *testing.T) {
		w := givenWorld()
		real := utc(9, 12, 10)
		l, err := w.svc.RecordTaken(ctx, ana, "p1", "m1", lunes, &real)
		if err != nil || !l.TakenAt.Equal(real) {
			t.Fatalf("got %+v err=%v", l, err)
		}
	})

	t.Run("dada una toma ya registrada, cuando se registra de nuevo, entonces no cambia (idempotente)", func(t *testing.T) {
		w := givenWorld()
		primera, _ := w.svc.RecordTaken(ctx, ana, "p1", "m1", lunes, nil)
		w.now = w.now.Add(time.Hour)
		segunda, err := w.svc.RecordTaken(ctx, ana, "p1", "m1", lunes, nil)
		if err != nil || segunda.ID != primera.ID || !segunda.TakenAt.Equal(*primera.TakenAt) || len(w.logs.rows) != 1 {
			t.Fatalf("debería conservar la primera: %+v err=%v filas=%d", segunda, err, len(w.logs.rows))
		}
	})

	t.Run("dada una toma marcada como omitida, cuando se registra tarde, entonces pasa a tomada", func(t *testing.T) {
		w := givenWorld()
		_ = w.logs.Save(ctx, &domain.DoseLog{MedicationID: "m1", ScheduledAt: lunes, Status: domain.DoseMissed})
		l, err := w.svc.RecordTaken(ctx, ana, "p1", "m1", lunes, nil)
		if err != nil || l.Status != domain.DoseTaken || l.TakenAt == nil {
			t.Fatalf("got %+v err=%v", l, err)
		}
	})

	t.Run("dada una cuidadora del plan, cuando registra la toma por el paciente, entonces se acepta", func(t *testing.T) {
		w := givenWorld()
		if _, err := w.svc.RecordTaken(ctx, carla, "p1", "m1", lunes, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("dado alguien sin vínculo o un plan/medicamento inexistente, cuando registra, entonces 403 / no encontrado", func(t *testing.T) {
		w := givenWorld()
		if _, err := w.svc.RecordTaken(ctx, dario, "p1", "m1", lunes, nil); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("sin vínculo: got %v", err)
		}
		if _, err := w.svc.RecordTaken(ctx, ana, "nope", "m1", lunes, nil); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("plan inexistente: got %v", err)
		}
		if _, err := w.svc.RecordTaken(ctx, ana, "p1", "nope", lunes, nil); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("medicamento inexistente: got %v", err)
		}
	})

	t.Run("dada una hora que no corresponde a ninguna toma del plan, cuando se registra, entonces es rechazada", func(t *testing.T) {
		w := givenWorld()
		for name, at := range map[string]time.Time{
			"5 minutos corrida":      utc(9, 11, 5),
			"un martes":              utc(10, 11, 0),
			"antes de que empiece":   utc(2, 11, 0).AddDate(0, 0, -14), // lunes 19/oct, antes del 1/nov
			"hora UTC sin convertir": utc(9, 8, 0),
		} {
			if _, err := w.svc.RecordTaken(ctx, ana, "p1", "m1", at, nil); !errors.Is(err, domain.ErrInvalidDose) {
				t.Errorf("%s: esperaba ErrInvalidDose, got %v", name, err)
			}
		}
	})

	t.Run("dada una toma de más de 2 horas en el futuro, cuando se registra, entonces es rechazada; dentro de las 2 horas, se acepta", func(t *testing.T) {
		w := givenWorld()
		jueves := utc(12, 23, 30) // ahora son las 15:00 UTC: faltan 8,5 horas
		if _, err := w.svc.RecordTaken(ctx, ana, "p1", "m1", jueves, nil); !errors.Is(err, domain.ErrInvalidDose) {
			t.Errorf("lejana: got %v", err)
		}
		w.now = utc(12, 22, 0) // falta 1,5 horas
		if _, err := w.svc.RecordTaken(ctx, ana, "p1", "m1", jueves, nil); err != nil {
			t.Errorf("cercana: %v", err)
		}
	})

	t.Run("dada una hora de toma en el futuro, cuando se informa como tomada, entonces es rechazada", func(t *testing.T) {
		w := givenWorld()
		futuro := w.now.Add(time.Hour)
		if _, err := w.svc.RecordTaken(ctx, ana, "p1", "m1", lunes, &futuro); !errors.Is(err, domain.ErrInvalidDose) {
			t.Fatalf("got %v", err)
		}
	})
}

// ---- History ----

func TestHistory(t *testing.T) {
	ctx := context.Background()

	t.Run("dada la semana 9-16, cuando se pide el historial, entonces el lunes figura tomado, el jueves pendiente y el lunes 16 queda fuera", func(t *testing.T) {
		w := givenWorld()
		_, _ = w.svc.RecordTaken(ctx, ana, "p1", "m1", utc(9, 11, 0), nil)
		got, err := w.svc.History(ctx, ana, "p1", utc(9, 0, 0), utc(16, 0, 0))
		if err != nil || len(got) != 2 {
			t.Fatalf("got %+v err=%v", got, err)
		}
		if got[0].Status != domain.DoseTaken || got[0].MedicationName != "Enalapril" || !got[0].ScheduledAt.Equal(utc(9, 11, 0)) || got[0].TakenAt == nil {
			t.Errorf("lunes: %+v", got[0])
		}
		if got[1].Status != domain.DosePending || !got[1].ScheduledAt.Equal(utc(12, 23, 30)) {
			t.Errorf("jueves: %+v", got[1])
		}
	})

	t.Run("dada una toma vencida sin registro, cuando pasó el margen de 30 minutos, entonces figura omitida aunque el proceso no haya corrido", func(t *testing.T) {
		w := givenWorld()
		got, _ := w.svc.History(ctx, ana, "p1", utc(9, 0, 0), utc(10, 0, 0)) // lunes sin registro
		if len(got) != 1 || got[0].Status != domain.DoseMissed {
			t.Fatalf("got %+v", got)
		}
		w.now = utc(9, 11, 20) // 20 minutos después de la hora: todavía dentro del margen
		got, _ = w.svc.History(ctx, ana, "p1", utc(9, 0, 0), utc(10, 0, 0))
		if got[0].Status != domain.DosePending {
			t.Fatalf("dentro del margen debería seguir pendiente: %+v", got[0])
		}
	})

	t.Run("dado el historial, cuando lo pide la cuidadora, entonces lo ve; alguien sin vínculo, no", func(t *testing.T) {
		w := givenWorld()
		if _, err := w.svc.History(ctx, carla, "p1", utc(9, 0, 0), utc(16, 0, 0)); err != nil {
			t.Errorf("cuidadora: %v", err)
		}
		if _, err := w.svc.History(ctx, dario, "p1", utc(9, 0, 0), utc(16, 0, 0)); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("sin vínculo: got %v", err)
		}
	})

	t.Run("dado un rango vacío, invertido o de más de 93 días, cuando se pide, entonces es rechazado", func(t *testing.T) {
		w := givenWorld()
		for name, r := range map[string][2]time.Time{
			"vacío":     {utc(9, 0, 0), utc(9, 0, 0)},
			"invertido": {utc(16, 0, 0), utc(9, 0, 0)},
			"enorme":    {utc(1, 0, 0), utc(1, 0, 0).AddDate(0, 0, 94)},
		} {
			if _, err := w.svc.History(ctx, ana, "p1", r[0], r[1]); !errors.Is(err, domain.ErrInvalidRange) {
				t.Errorf("%s: got %v", name, err)
			}
		}
	})
}

// ---- Stats ----

func TestStats(t *testing.T) {
	ctx := context.Background()

	t.Run("dadas 4 tomas del 1 al 16 (2 tomadas, 1 sin registro vencida, 1 futura), cuando se piden las estadísticas, entonces el cumplimiento es 2 de 3", func(t *testing.T) {
		w := givenWorld()
		_, _ = w.svc.RecordTaken(ctx, ana, "p1", "m1", utc(2, 11, 0), nil) // lunes 2
		_, _ = w.svc.RecordTaken(ctx, ana, "p1", "m1", utc(9, 11, 0), nil) // lunes 9
		// jueves 5 sin registro (omitida); jueves 12 20:30 todavía no llegó (pendiente)
		st, err := w.svc.Stats(ctx, carla, "p1", utc(1, 0, 0), utc(16, 0, 0))
		if err != nil {
			t.Fatal(err)
		}
		if st.Expected != 4 || st.Taken != 2 || st.Missed != 1 || st.Pending != 1 {
			t.Fatalf("conteos incorrectos: %+v", st)
		}
		if st.AdherenceRate == nil || math.Abs(*st.AdherenceRate-2.0/3.0) > 1e-9 {
			t.Fatalf("cumplimiento incorrecto: %v", st.AdherenceRate)
		}
	})

	t.Run("dado un rango sin tomas vencidas, cuando se piden las estadísticas, entonces el cumplimiento no se puede calcular (nil)", func(t *testing.T) {
		w := givenWorld()
		st, err := w.svc.Stats(ctx, ana, "p1", utc(12, 0, 0), utc(13, 0, 0)) // solo el jueves 20:30, aún pendiente
		if err != nil || st.Expected != 1 || st.Pending != 1 || st.AdherenceRate != nil {
			t.Fatalf("got %+v err=%v", st, err)
		}
	})

	t.Run("dadas estadísticas, cuando las pide alguien sin vínculo, entonces 403", func(t *testing.T) {
		w := givenWorld()
		if _, err := w.svc.Stats(ctx, dario, "p1", utc(1, 0, 0), utc(16, 0, 0)); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("got %v", err)
		}
	})
}

// ---- Descuento de stock ----

type fakeStock struct {
	calls []string // ids de medicamento
	err   error
}

func (f *fakeStock) OnDoseTaken(_ context.Context, _ *domain.Plan, med domain.Medication) error {
	f.calls = append(f.calls, med.ID)
	return f.err
}

func TestRecordTakenConsumesStock(t *testing.T) {
	ctx := context.Background()
	lunes := utc(9, 11, 0)

	givenWithStock := func() (*world, *fakeStock) {
		w := givenWorld()
		st := &fakeStock{}
		w.svc.SetStock(st)
		return w, st
	}

	t.Run("dada una toma nueva, cuando se registra, entonces se descuenta una unidad de ese medicamento", func(t *testing.T) {
		w, st := givenWithStock()
		if _, err := w.svc.RecordTaken(ctx, ana, "p1", "m1", lunes, nil); err != nil {
			t.Fatal(err)
		}
		if len(st.calls) != 1 || st.calls[0] != "m1" {
			t.Fatalf("calls: %v", st.calls)
		}
	})

	t.Run("dada una toma ya registrada, cuando se registra de nuevo, entonces NO se descuenta otra vez", func(t *testing.T) {
		w, st := givenWithStock()
		_, _ = w.svc.RecordTaken(ctx, ana, "p1", "m1", lunes, nil)
		_, _ = w.svc.RecordTaken(ctx, carla, "p1", "m1", lunes, nil)
		if len(st.calls) != 1 {
			t.Fatalf("debería descontar una sola vez: %v", st.calls)
		}
	})

	t.Run("dada una toma marcada como omitida, cuando se registra tarde, entonces se descuenta (sí se tomó)", func(t *testing.T) {
		w, st := givenWithStock()
		_ = w.logs.Save(ctx, &domain.DoseLog{MedicationID: "m1", ScheduledAt: lunes, Status: domain.DoseMissed})
		_, _ = w.svc.RecordTaken(ctx, ana, "p1", "m1", lunes, nil)
		if len(st.calls) != 1 {
			t.Fatalf("calls: %v", st.calls)
		}
	})

	t.Run("dada una toma rechazada por inválida o sin permiso, cuando falla, entonces no se descuenta nada", func(t *testing.T) {
		w, st := givenWithStock()
		_, _ = w.svc.RecordTaken(ctx, ana, "p1", "m1", utc(9, 11, 5), nil) // hora que no corresponde
		_, _ = w.svc.RecordTaken(ctx, dario, "p1", "m1", lunes, nil)       // sin vínculo
		if len(st.calls) != 0 {
			t.Fatalf("calls: %v", st.calls)
		}
	})

	t.Run("dada una falla al descontar el stock, cuando se registra la toma, entonces la toma igual queda registrada", func(t *testing.T) {
		w, st := givenWithStock()
		st.err = errors.New("base lenta")
		l, err := w.svc.RecordTaken(ctx, ana, "p1", "m1", lunes, nil)
		if err != nil || l.Status != domain.DoseTaken {
			t.Fatalf("la toma no debería fallar por el stock: %+v err=%v", l, err)
		}
	})
}
