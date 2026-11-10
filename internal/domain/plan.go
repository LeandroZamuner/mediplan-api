package domain

import (
	"errors"
	"time"
)

type PlanStatus string

const (
	PlanActive    PlanStatus = "active"
	PlanCompleted PlanStatus = "completed"
	PlanCancelled PlanStatus = "cancelled"
)

// Schedule es un recordatorio semanal: día (0 = domingo ... 6 = sábado) y hora "HH:MM".
type Schedule struct {
	DayOfWeek int
	TimeOfDay string
}

type Medication struct {
	ID           string
	Name         string
	Dose         string
	StartDate    time.Time
	EndDate      *time.Time
	Schedules    []Schedule
	StockUnits   int
	StockAlertAt int
}

type Plan struct {
	ID          string
	PatientID   string
	Name        string
	Status      PlanStatus
	Medications []Medication
	CreatedAt   time.Time
}

var (
	ErrForbidden   = errors.New("sin permiso")
	ErrInvalidPlan = errors.New("plan inválido")
)
