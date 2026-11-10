package domain

import (
	"errors"
	"time"
)

// ErrInvalidDeviceToken: el token de FCM del celular viene vacío o mal formado (no confundir con el token de sesión).
// ErrInvalidStock: la cantidad a reponer no es válida (debe ser un entero positivo razonable).
var ErrInvalidStock = errors.New("cantidad de stock inválida")

var ErrInvalidDeviceToken = errors.New("token de dispositivo inválido")

const (
	NotifMissedDose = "missed_dose"
	NotifLowStock   = "low_stock" // data.level = "low" (queda poco) | "out" (se agotó)
	NotifPlanSync   = "plan_sync" // mensaje silencioso: el plan cambió, hay que resincronizar y reprogramar alarmas
)

// Notification es un aviso para un usuario (se guarda para mostrarlo en la app y se envía como push).
type Notification struct {
	ID        string
	UserID    string
	Kind      string
	Title     string
	Body      string
	Data      map[string]string
	ReadAt    *time.Time
	CreatedAt time.Time
}
