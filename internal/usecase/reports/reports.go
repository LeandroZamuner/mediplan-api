// Package reports arma y envía el resumen semanal por correo (solo Premium y solo a quien lo pidió).
//
// El texto del resumen lo puede redactar un modelo de IA, pero solo si la persona lo aceptó y, aun así, el modelo
// recibe únicamente números anónimos (sin nombre, correo ni nombres de medicamentos). Si la IA falla o no está
// configurada, se usa un texto fijo: el correo sale igual.
package reports

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/accounts"
	"github.com/lea/mediplan-api/internal/usecase/ai"
	"github.com/lea/mediplan-api/internal/usecase/doses"
)

type Recipient struct {
	UserID, Email, Timezone string
	AIConsent               bool
}

// Puertos.

type Recipients interface {
	Recipients(ctx context.Context, now time.Time) ([]Recipient, error)
	MarkSent(ctx context.Context, userID string, at time.Time) error
}

type PlanSource interface {
	ListPlans(ctx context.Context, actor *domain.User) ([]*domain.Plan, error)
}

type History interface {
	History(ctx context.Context, actor *domain.User, planID string, from, to time.Time) ([]doses.Occurrence, error)
}

type Tiers interface {
	TierOf(ctx context.Context, userID string) (domain.Tier, error)
}

type Service struct {
	recipients Recipients
	plans      PlanSource
	history    History
	tiers      Tiers
	mail       accounts.Mailer
	llm        ai.LLM // puede ser nil
	webBase    string
	now        func() time.Time
}

func NewService(r Recipients, p PlanSource, h History, t Tiers, m accounts.Mailer, llm ai.LLM, webBase string) *Service {
	return &Service{recipients: r, plans: p, history: h, tiers: t, mail: m, llm: llm, webBase: strings.TrimRight(webBase, "/"), now: time.Now}
}

// Result resume una corrida de la tarea semanal.
type Result struct{ Sent, Skipped, Failed int }

// Run envía el resumen a cada persona que corresponda. Un error con una persona no frena a las demás.
func (s *Service) Run(ctx context.Context) (Result, error) {
	now := s.now()
	list, err := s.recipients.Recipients(ctx, now)
	if err != nil {
		return Result{}, err
	}
	var res Result
	for _, r := range list {
		switch err := s.sendOne(ctx, r, now); err {
		case nil:
			res.Sent++
		case errSkip:
			res.Skipped++
		default:
			res.Failed++
			log.Printf("resumen semanal: no se pudo enviar a %s: %v", r.UserID, err)
		}
	}
	return res, nil
}

var errSkip = fmt.Errorf("sin datos para informar")

func (s *Service) sendOne(ctx context.Context, r Recipient, now time.Time) error {
	if t, err := s.tiers.TierOf(ctx, r.UserID); err != nil || t != domain.TierPremium {
		return errSkip // el Premium venció desde que se armó la lista
	}
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil {
		loc = time.UTC
	}
	actor := &domain.User{ID: r.UserID, Email: r.Email, Timezone: r.Timezone}
	week, err := s.collect(ctx, actor, now.Add(-7*24*time.Hour), now, loc)
	if err != nil {
		return err
	}
	if week.Expected == 0 {
		return errSkip // no hubo tomas programadas: no hay nada para contar
	}
	prev, _ := s.collect(ctx, actor, now.Add(-14*24*time.Hour), now.Add(-7*24*time.Hour), loc)
	summary := FallbackSummary(week, prev)
	if r.AIConsent && s.llm != nil {
		if text, err := s.llm.Chat(ctx, summarySystem, []ai.Msg{{Role: "user", Text: AnonymousFacts(week, prev)}}, nil); err == nil && strings.TrimSpace(text) != "" {
			summary = strings.TrimSpace(text)
		} else if err != nil {
			log.Printf("resumen semanal: la IA falló, se usa el texto fijo: %v", err)
		}
	}
	msg := Render(week, summary, s.webBase, loc, now)
	msg.To = r.Email
	if err := s.mail.Send(ctx, msg); err != nil {
		return err
	}
	return s.recipients.MarkSent(ctx, r.UserID, now)
}

// MedWeek es lo ocurrido con un medicamento en la semana.
type MedWeek struct {
	Name          string
	Taken, Missed int
	StockUnits    int
	LowStock      bool
}

// Week agrupa los números de una semana.
type Week struct {
	Expected, Taken, Missed, Pending int
	MissedByWeekday                  [7]int // 0 = domingo
	Meds                             []MedWeek
}

// Rate es tomadas / (tomadas + omitidas); -1 si todavía no venció ninguna.
func (w Week) Rate() float64 {
	if d := w.Taken + w.Missed; d > 0 {
		return float64(w.Taken) / float64(d)
	}
	return -1
}

func (s *Service) collect(ctx context.Context, actor *domain.User, from, to time.Time, loc *time.Location) (Week, error) {
	var w Week
	plans, err := s.plans.ListPlans(ctx, actor)
	if err != nil {
		return w, err
	}
	for _, p := range plans {
		if p.Status != domain.PlanActive {
			continue
		}
		occ, err := s.history.History(ctx, actor, p.ID, from, to)
		if err != nil {
			return w, err
		}
		byMed := map[string]*MedWeek{}
		for _, m := range p.Medications {
			byMed[m.ID] = &MedWeek{Name: m.Name, StockUnits: m.StockUnits, LowStock: m.StockAlertAt > 0 && m.StockUnits <= m.StockAlertAt}
		}
		for _, o := range occ {
			w.Expected++
			mw := byMed[o.MedicationID]
			switch o.Status {
			case domain.DoseTaken:
				w.Taken++
				if mw != nil {
					mw.Taken++
				}
			case domain.DoseMissed:
				w.Missed++
				w.MissedByWeekday[o.ScheduledAt.In(loc).Weekday()]++
				if mw != nil {
					mw.Missed++
				}
			default:
				w.Pending++
			}
		}
		for _, m := range p.Medications {
			w.Meds = append(w.Meds, *byMed[m.ID])
		}
	}
	return w, nil
}

func pct(r float64) string { return fmt.Sprintf("%d%%", int(r*100+0.5)) }

var weekdayNames = [7]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}

// FallbackSummary es el texto fijo (sin IA).
func FallbackSummary(w, prev Week) string {
	r := w.Rate()
	if r < 0 {
		return "Esta semana todavía no venció ninguna toma programada."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Esta semana tomaste %d de %d tomas programadas (%s).", w.Taken, w.Taken+w.Missed, pct(r))
	if pr := prev.Rate(); pr >= 0 {
		switch {
		case r > pr+0.02:
			fmt.Fprintf(&b, " Mejoraste respecto de la semana anterior (%s).", pct(pr))
		case r < pr-0.02:
			fmt.Fprintf(&b, " Bajó un poco respecto de la semana anterior (%s).", pct(pr))
		default:
			b.WriteString(" Se mantuvo parecido a la semana anterior.")
		}
	}
	if d, n := worstDay(w); n > 1 {
		fmt.Fprintf(&b, " El día con más tomas olvidadas fue el %s.", d)
	}
	return b.String()
}

func worstDay(w Week) (string, int) {
	best, n := 0, 0
	for i, c := range w.MissedByWeekday {
		if c > n {
			best, n = i, c
		}
	}
	return weekdayNames[best], n
}

const summarySystem = `Sos el asistente de MediPlan, una app de recordatorios de medicación. Con los números de la semana que te paso, escribí un resumen de 2 o 3 oraciones en español rioplatense (tuteo con "vos"), cálido y concreto, que reconozca lo que salió bien y, si hace falta, sugiera una pequeña mejora práctica (por ejemplo usar una alarma a la misma hora). Usá solo los datos recibidos: no inventes nada. No des consejos médicos ni nombres de medicamentos, no cambies dosis ni horarios, no uses listas ni emojis. Devolvé solo el texto del resumen.`

// AnonymousFacts es lo ÚNICO que se le manda al modelo: números, sin nombres ni correos ni medicamentos.
func AnonymousFacts(w, prev Week) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Semana actual: tomas programadas vencidas %d, tomadas %d, omitidas %d", w.Taken+w.Missed, w.Taken, w.Missed)
	if r := w.Rate(); r >= 0 {
		fmt.Fprintf(&b, ", cumplimiento %s", pct(r))
	}
	b.WriteString(".\n")
	if pr := prev.Rate(); pr >= 0 {
		fmt.Fprintf(&b, "Semana anterior: cumplimiento %s.\n", pct(pr))
	}
	if d, n := worstDay(w); n > 0 {
		fmt.Fprintf(&b, "Día con más omisiones: %s (%d).\n", d, n)
	}
	low := 0
	for _, m := range w.Meds {
		if m.LowStock {
			low++
		}
	}
	fmt.Fprintf(&b, "Medicamentos con stock bajo: %d.\n", low)
	return b.String()
}
