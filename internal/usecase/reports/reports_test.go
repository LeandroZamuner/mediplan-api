package reports

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/accounts"
	"github.com/lea/mediplan-api/internal/usecase/ai"
	"github.com/lea/mediplan-api/internal/usecase/doses"
)

var now = time.Date(2026, 11, 23, 11, 0, 0, 0, time.UTC) // lunes

type fakeRecipients struct {
	list   []Recipient
	marked []string
}

func (f *fakeRecipients) Recipients(context.Context, time.Time) ([]Recipient, error) {
	return f.list, nil
}
func (f *fakeRecipients) MarkSent(_ context.Context, id string, _ time.Time) error {
	f.marked = append(f.marked, id)
	return nil
}

type fakePlans struct{}

func (fakePlans) ListPlans(context.Context, *domain.User) ([]*domain.Plan, error) {
	return []*domain.Plan{{ID: "p1", Status: domain.PlanActive, Medications: []domain.Medication{
		{ID: "m1", Name: "Enalapril", StockUnits: 2, StockAlertAt: 5},
		{ID: "m2", Name: "Metformina", StockUnits: 40, StockAlertAt: 5},
	}}}, nil
}

type fakeHistory struct{ occ []doses.Occurrence }

func (h fakeHistory) History(_ context.Context, _ *domain.User, _ string, from, _ time.Time) ([]doses.Occurrence, error) {
	if from.Before(now.Add(-8 * 24 * time.Hour)) { // semana anterior
		return []doses.Occurrence{{MedicationID: "m1", Status: domain.DoseTaken, ScheduledAt: from.Add(time.Hour)}, {MedicationID: "m1", Status: domain.DoseMissed, ScheduledAt: from.Add(2 * time.Hour)}}, nil
	}
	return h.occ, nil
}

type tier domain.Tier

func (t tier) TierOf(context.Context, string) (domain.Tier, error) { return domain.Tier(t), nil }

type sink struct{ sent []accounts.Message }

func (s *sink) Send(_ context.Context, m accounts.Message) error {
	s.sent = append(s.sent, m)
	return nil
}

type fakeLLM struct {
	reply   string
	err     error
	prompts []string
}

func (l *fakeLLM) Chat(_ context.Context, _ string, h []ai.Msg, _ []ai.Tool) (string, error) {
	l.prompts = append(l.prompts, h[0].Text)
	return l.reply, l.err
}

func week() []doses.Occurrence {
	d := now.Add(-3 * 24 * time.Hour) // viernes
	return []doses.Occurrence{
		{MedicationID: "m1", Status: domain.DoseTaken, ScheduledAt: d},
		{MedicationID: "m1", Status: domain.DoseTaken, ScheduledAt: d.Add(time.Hour)},
		{MedicationID: "m2", Status: domain.DoseTaken, ScheduledAt: d.Add(2 * time.Hour)},
		{MedicationID: "m2", Status: domain.DoseMissed, ScheduledAt: d.Add(3 * time.Hour)},
	}
}

func setup(t tier, r Recipient, llm ai.LLM) (*Service, *fakeRecipients, *sink) {
	rec := &fakeRecipients{list: []Recipient{r}}
	out := &sink{}
	s := NewService(rec, fakePlans{}, fakeHistory{week()}, t, out, llm, "https://web/mediplan")
	s.now = func() time.Time { return now }
	return s, rec, out
}

var ana = Recipient{UserID: "u1", Email: "ana@mail.com", Timezone: "America/Argentina/Buenos_Aires"}

func TestRun(t *testing.T) {
	ctx := context.Background()
	t.Run("dada una persona Premium sin aceptar la IA, cuando corre la tarea, entonces recibe el resumen con texto fijo y no se llama al modelo", func(t *testing.T) {
		llm := &fakeLLM{reply: "no debería usarse"}
		s, rec, out := setup("premium", ana, llm)
		res, err := s.Run(ctx)
		if err != nil || res.Sent != 1 || len(llm.prompts) != 0 || len(rec.marked) != 1 {
			t.Fatalf("%+v %v prompts=%d", res, err, len(llm.prompts))
		}
		m := out.sent[0]
		if m.To != "ana@mail.com" || !strings.Contains(m.Text, "3 de 4") || !strings.Contains(m.Text, "Enalapril") || !strings.Contains(m.Text, "Stock bajo: Enalapril") || !strings.Contains(m.HTML, "Abrir MediPlan") {
			t.Fatalf("correo inesperado: %+v", m)
		}
	})
	t.Run("dada una persona que aceptó la IA, cuando corre la tarea, entonces el resumen lo redacta el modelo y a él solo le llegan números anónimos", func(t *testing.T) {
		llm := &fakeLLM{reply: "¡Buena semana, seguí así!"}
		r := ana
		r.AIConsent = true
		s, _, out := setup("premium", r, llm)
		if res, _ := s.Run(ctx); res.Sent != 1 {
			t.Fatal("debía enviarse")
		}
		if !strings.Contains(out.sent[0].Text, "¡Buena semana, seguí así!") {
			t.Fatalf("falta el texto de la IA: %s", out.sent[0].Text)
		}
		p := llm.prompts[0]
		for _, secret := range []string{"ana@mail.com", "Enalapril", "Metformina"} {
			if strings.Contains(p, secret) {
				t.Fatalf("al modelo no se le puede mandar %q: %s", secret, p)
			}
		}
		if !strings.Contains(p, "cumplimiento 75%") || !strings.Contains(p, "Medicamentos con stock bajo: 1") {
			t.Fatalf("faltan los números: %s", p)
		}
	})
	t.Run("dada una falla del modelo, cuando corre la tarea, entonces el correo sale igual con el texto fijo", func(t *testing.T) {
		r := ana
		r.AIConsent = true
		s, _, out := setup("premium", r, &fakeLLM{err: errors.New("cuota agotada")})
		if res, _ := s.Run(ctx); res.Sent != 1 || !strings.Contains(out.sent[0].Text, "3 de 4") {
			t.Fatalf("%+v", out.sent)
		}
	})
	t.Run("dada una persona cuyo Premium venció, cuando corre la tarea, entonces no se le envía", func(t *testing.T) {
		s, rec, out := setup("free", ana, nil)
		if res, _ := s.Run(ctx); res.Skipped != 1 || len(out.sent) != 0 || len(rec.marked) != 0 {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("dada una semana sin tomas programadas, cuando corre la tarea, entonces no se envía un correo vacío", func(t *testing.T) {
		s, _, out := setup("premium", ana, nil)
		s.history = fakeHistory{}
		if res, _ := s.Run(ctx); res.Skipped != 1 || len(out.sent) != 0 {
			t.Fatalf("%+v", res)
		}
	})
}

func TestFallbackSummary(t *testing.T) {
	good := Week{Taken: 9, Missed: 1}
	if got := FallbackSummary(good, Week{Taken: 5, Missed: 5}); !strings.Contains(got, "9 de 10 tomas programadas (90%)") || !strings.Contains(got, "Mejoraste") {
		t.Fatal(got)
	}
	w := Week{Taken: 5, Missed: 3}
	w.MissedByWeekday[6] = 2
	w.MissedByWeekday[1] = 1
	if got := FallbackSummary(w, Week{}); !strings.Contains(got, "sábado") {
		t.Fatal(got)
	}
	if got := FallbackSummary(Week{Pending: 4}, Week{}); !strings.Contains(got, "todavía no venció") {
		t.Fatal(got)
	}
}
