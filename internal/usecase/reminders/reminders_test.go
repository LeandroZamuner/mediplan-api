package reminders

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

// ---- fakes ----

type fakeSource struct{ plans []ActivePlan }

func (f fakeSource) ActivePlansWithCaregivers(context.Context) ([]ActivePlan, error) {
	return f.plans, nil
}

// fakeMarker simula la tabla dose_logs: marca omitida solo si no había registro de esa toma.
type fakeMarker struct {
	rows map[string]domain.DoseStatus
}

func key(medID string, at time.Time) string { return medID + "|" + at.UTC().Format(time.RFC3339) }

func (f *fakeMarker) MarkMissed(_ context.Context, medID string, at time.Time) (bool, error) {
	if _, exists := f.rows[key(medID, at)]; exists {
		return false, nil
	}
	f.rows[key(medID, at)] = domain.DoseMissed
	return true, nil
}

type sentNotif struct {
	userID string
	n      domain.Notification
}

type fakeNotifier struct {
	sent   []sentNotif
	failTo string
}

func (f *fakeNotifier) Notify(_ context.Context, userID string, n domain.Notification) error {
	if userID == f.failTo {
		return errors.New("base caída")
	}
	f.sent = append(f.sent, sentNotif{userID, n})
	return nil
}

// ---- givens ----
// Plan de Ana (Buenos Aires, UTC-3) creado el 1/nov: Enalapril 10 mg, lunes 08:00 y jueves 20:30 (= 23:30 UTC).
// La toma del jueves 12 a las 20:30 se considera omitida desde las 21:00 locales (00:00 UTC del 13).

func utc(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, time.UTC) }

type world struct {
	svc      *Service
	marker   *fakeMarker
	notifier *fakeNotifier
	now      time.Time
}

func givenWorld(caregivers ...string) *world {
	w := &world{marker: &fakeMarker{rows: map[string]domain.DoseStatus{}}, notifier: &fakeNotifier{}, now: utc(13, 0, 5)}
	plan := &domain.Plan{
		ID: "p1", PatientID: "ana", Name: "Hipertensión", Status: domain.PlanActive, CreatedAt: utc(1, 0, 0),
		Medications: []domain.Medication{{
			ID: "m1", Name: "Enalapril", Dose: "10 mg", StartDate: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
			Schedules: []domain.Schedule{{DayOfWeek: 1, TimeOfDay: "08:00"}, {DayOfWeek: 4, TimeOfDay: "20:30"}},
		}},
	}
	src := fakeSource{plans: []ActivePlan{{Plan: plan, PatientEmail: "ana@mail.com", Timezone: "America/Argentina/Buenos_Aires", CaregiverIDs: caregivers}}}
	w.svc = NewService(src, w.marker, w.notifier)
	w.svc.now = func() time.Time { return w.now }
	return w
}

// ---- tests ----

func TestDetectMissed(t *testing.T) {
	ctx := context.Background()

	t.Run("dada la toma del jueves 20:30 sin registro y pasado el margen, cuando corre la detección, entonces se marca omitida y se avisa a la cuidadora", func(t *testing.T) {
		w := givenWorld("carla")
		rep, err := w.svc.DetectMissed(ctx)
		if err != nil || rep.NewMissed != 1 || rep.Notified != 1 || rep.Failed != 0 {
			t.Fatalf("reporte %+v err=%v", rep, err)
		}
		if w.marker.rows[key("m1", utc(12, 23, 30))] != domain.DoseMissed {
			t.Fatal("la toma debería quedar marcada como omitida")
		}
		got := w.notifier.sent[0]
		if got.userID != "carla" || got.n.Kind != domain.NotifMissedDose {
			t.Fatalf("destinatario o tipo incorrecto: %+v", got)
		}
		for _, want := range []string{"ana@mail.com", "Enalapril", "10 mg", "20:30", "Hipertensión"} {
			if !strings.Contains(got.n.Body, want) {
				t.Errorf("el texto %q debería contener %q", got.n.Body, want)
			}
		}
		if got.n.Data["planId"] != "p1" || got.n.Data["medicationId"] != "m1" || got.n.Data["scheduledAt"] != "2026-11-12T23:30:00Z" {
			t.Errorf("datos incorrectos: %+v", got.n.Data)
		}
	})

	t.Run("dada una toma dentro del margen de 30 minutos, cuando corre la detección, entonces todavía no se avisa", func(t *testing.T) {
		w := givenWorld("carla")
		w.now = utc(12, 23, 45) // 15 minutos después de las 20:30 locales
		rep, _ := w.svc.DetectMissed(ctx)
		if rep.NewMissed != 0 || len(w.notifier.sent) != 0 {
			t.Fatalf("no debería haber avisos: %+v", rep)
		}
	})

	t.Run("dada una detección que ya corrió, cuando corre otra vez, entonces no se avisa dos veces", func(t *testing.T) {
		w := givenWorld("carla")
		_, _ = w.svc.DetectMissed(ctx)
		rep, _ := w.svc.DetectMissed(ctx)
		if rep.NewMissed != 0 || len(w.notifier.sent) != 1 {
			t.Fatalf("avisos=%d reporte=%+v", len(w.notifier.sent), rep)
		}
	})

	t.Run("dada una toma que Ana ya registró, cuando corre la detección, entonces no se marca ni se avisa", func(t *testing.T) {
		w := givenWorld("carla")
		w.marker.rows[key("m1", utc(12, 23, 30))] = domain.DoseTaken
		rep, _ := w.svc.DetectMissed(ctx)
		if rep.NewMissed != 0 || len(w.notifier.sent) != 0 {
			t.Fatalf("got %+v", rep)
		}
	})

	t.Run("dado un plan sin cuidadores, cuando corre la detección, entonces no se hace nada (no hay a quién avisar)", func(t *testing.T) {
		w := givenWorld()
		rep, _ := w.svc.DetectMissed(ctx)
		if rep.NewMissed != 0 || len(w.marker.rows) != 0 || len(w.notifier.sent) != 0 {
			t.Fatalf("got %+v filas=%d", rep, len(w.marker.rows))
		}
	})

	t.Run("dado un plan con dos cuidadores, cuando una toma se omite, entonces los dos reciben el aviso", func(t *testing.T) {
		w := givenWorld("carla", "dario")
		rep, _ := w.svc.DetectMissed(ctx)
		if rep.NewMissed != 1 || rep.Notified != 2 || len(w.notifier.sent) != 2 {
			t.Fatalf("got %+v", rep)
		}
	})

	t.Run("dada una falla al avisar a un cuidador, cuando corre la detección, entonces los demás igual reciben el aviso y se informa la falla", func(t *testing.T) {
		w := givenWorld("carla", "dario")
		w.notifier.failTo = "carla"
		rep, err := w.svc.DetectMissed(ctx)
		if err != nil || rep.Notified != 1 || rep.Failed != 1 || len(w.notifier.sent) != 1 || w.notifier.sent[0].userID != "dario" {
			t.Fatalf("got %+v err=%v", rep, err)
		}
	})

	t.Run("dada una toma anterior a la creación del plan, cuando corre la detección, entonces no cuenta como omitida", func(t *testing.T) {
		w := givenWorld("carla")
		w.svc.source = fakeSource{plans: []ActivePlan{func() ActivePlan {
			p := w.svc.source.(fakeSource).plans[0]
			p.Plan.CreatedAt = utc(13, 0, 0) // el plan se creó DESPUÉS de las 20:30 del jueves
			return p
		}()}}
		rep, _ := w.svc.DetectMissed(ctx)
		if rep.NewMissed != 0 {
			t.Fatalf("got %+v", rep)
		}
	})

	t.Run("dada una toma de hace más de 24 horas, cuando corre la detección, entonces no se avisa (evita una avalancha tras una caída)", func(t *testing.T) {
		w := givenWorld("carla")
		w.now = utc(14, 2, 0) // el jueves 20:30 quedó a 26,5 horas
		rep, _ := w.svc.DetectMissed(ctx)
		if rep.NewMissed != 0 || len(w.notifier.sent) != 0 {
			t.Fatalf("got %+v", rep)
		}
	})

	t.Run("dado el reloj en una zona distinta, cuando se arma el texto, entonces la hora es la local del paciente", func(t *testing.T) {
		w := givenWorld("carla")
		_, _ = w.svc.DetectMissed(ctx)
		if !strings.Contains(w.notifier.sent[0].n.Body, "20:30") || strings.Contains(w.notifier.sent[0].n.Body, "23:30") {
			t.Fatalf("la hora debe ser local, no UTC: %q", w.notifier.sent[0].n.Body)
		}
	})
}
