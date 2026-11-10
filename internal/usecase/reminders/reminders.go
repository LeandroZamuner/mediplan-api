// Package reminders detecta las tomas omitidas y avisa a los cuidadores del plan.
//
// Se ejecuta de forma periódica (Cloud Scheduler llama a un endpoint). Una toma sin
// registro cuya hora pasó hace más de GracePeriod se marca como omitida y se avisa
// a cada cuidador, una sola vez. Solo se procesan planes con cuidadores: sin ellos no
// hay a quién avisar, y el historial deduce igual las omisiones al consultarlo.
package reminders

import (
	"context"
	"fmt"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

const (
	GracePeriod = 30 * time.Minute // debe coincidir con doses.GracePeriod
	Lookback    = 24 * time.Hour   // no se revisan tomas más viejas (evita avalanchas tras una caída)
)

// ActivePlan es un plan activo que tiene al menos un cuidador aceptado.
type ActivePlan struct {
	Plan         *domain.Plan // con medicamentos y horarios
	PatientEmail string
	Timezone     string   // zona horaria IANA del paciente
	CaregiverIDs []string // cuidadores con vínculo aceptado
}

// Puertos.

type PlanSource interface {
	ActivePlansWithCaregivers(ctx context.Context) ([]ActivePlan, error)
}

// Marker marca la toma como omitida SOLO si no tenía ningún registro (atómico).
// created=false significa que ya estaba registrada (tomada u omitida).
type Marker interface {
	MarkMissed(ctx context.Context, medicationID string, scheduledAt time.Time) (created bool, err error)
}

type Notifier interface {
	Notify(ctx context.Context, userID string, n domain.Notification) error
}

type Report struct {
	NewMissed int // tomas marcadas como omitidas en esta corrida
	Notified  int // avisos entregados
	Failed    int // avisos que fallaron
}

type Service struct {
	source   PlanSource
	marker   Marker
	notifier Notifier
	now      func() time.Time
}

func NewService(s PlanSource, m Marker, n Notifier) *Service {
	return &Service{source: s, marker: m, notifier: n, now: time.Now}
}

func (s *Service) DetectMissed(ctx context.Context) (Report, error) {
	var rep Report
	plans, err := s.source.ActivePlansWithCaregivers(ctx)
	if err != nil {
		return rep, err
	}
	now := s.now()
	from, to := now.Add(-Lookback), now.Add(-GracePeriod).Add(time.Nanosecond) // incluye lo que venció justo ahora

	for _, ap := range plans {
		if len(ap.CaregiverIDs) == 0 {
			continue
		}
		loc, err := time.LoadLocation(ap.Timezone)
		if err != nil {
			loc, _ = time.LoadLocation(domain.DefaultTimezone)
		}
		for _, med := range ap.Plan.Medications {
			for _, at := range domain.Occurrences(med, loc, from, to, ap.Plan.CreatedAt) {
				created, err := s.marker.MarkMissed(ctx, med.ID, at)
				if err != nil {
					return rep, err
				}
				if !created {
					continue
				}
				rep.NewMissed++
				n := missedNotification(ap, med, at, loc)
				for _, caregiver := range ap.CaregiverIDs {
					if err := s.notifier.Notify(ctx, caregiver, n); err != nil {
						rep.Failed++
						continue
					}
					rep.Notified++
				}
			}
		}
	}
	return rep, nil
}

func missedNotification(ap ActivePlan, med domain.Medication, at time.Time, loc *time.Location) domain.Notification {
	return domain.Notification{
		Kind:  domain.NotifMissedDose,
		Title: "Dosis no registrada",
		Body: fmt.Sprintf("%s no registró %s (%s) de las %s — plan %s",
			ap.PatientEmail, med.Name, med.Dose, at.In(loc).Format("15:04"), ap.Plan.Name),
		Data: map[string]string{
			"planId":       ap.Plan.ID,
			"medicationId": med.ID,
			"scheduledAt":  at.UTC().Format(time.RFC3339),
		},
	}
}
