package domain

import (
	"errors"
	"sort"
	"time"
)

type DoseStatus string

const (
	DosePending DoseStatus = "pending"
	DoseTaken   DoseStatus = "taken"
	DoseMissed  DoseStatus = "missed"
)

// DoseLog es el registro de una toma concreta (un medicamento a una hora programada).
// Solo se guardan las tomas hechas ("taken") y las omitidas ("missed"); "pending" es
// una toma futura que todavía no tiene registro.
type DoseLog struct {
	ID           string
	MedicationID string
	ScheduledAt  time.Time // instante exacto de la toma programada
	TakenAt      *time.Time
	Status       DoseStatus
}

var (
	ErrInvalidDose  = errors.New("toma inválida")
	ErrInvalidRange = errors.New("rango de fechas inválido")
)

// Occurrences devuelve los instantes en que toca tomar el medicamento dentro de
// [from, to), ordenados. Los horarios son hora local de `loc` (la zona del paciente).
// Respeta la fecha de inicio y la de fin (inclusiva) del medicamento, y descarta lo
// anterior a notBefore (por ejemplo, la creación del plan). notBefore cero = sin límite.
func Occurrences(m Medication, loc *time.Location, from, to, notBefore time.Time) []time.Time {
	if !from.Before(to) || len(m.Schedules) == 0 {
		return nil
	}
	startY, startM, startD := m.StartDate.Date()
	startDay := time.Date(startY, startM, startD, 0, 0, 0, 0, loc)

	var out []time.Time
	// Un día de margen a cada lado: el día local puede empezar antes/después del instante UTC.
	for day := from.In(loc).AddDate(0, 0, -1); !day.After(to.In(loc).AddDate(0, 0, 1)); day = day.AddDate(0, 0, 1) {
		y, mo, d := day.Date()
		if time.Date(y, mo, d, 0, 0, 0, 0, loc).Before(startDay) {
			continue
		}
		if m.EndDate != nil {
			ey, em, ed := m.EndDate.Date()
			if time.Date(y, mo, d, 0, 0, 0, 0, loc).After(time.Date(ey, em, ed, 0, 0, 0, 0, loc)) {
				continue
			}
		}
		for _, s := range m.Schedules {
			if int(day.Weekday()) != s.DayOfWeek {
				continue
			}
			clock, err := time.Parse("15:04", s.TimeOfDay)
			if err != nil {
				continue
			}
			at := time.Date(y, mo, d, clock.Hour(), clock.Minute(), 0, 0, loc)
			if at.Before(from) || !at.Before(to) || at.Before(notBefore) {
				continue
			}
			out = append(out, at)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
