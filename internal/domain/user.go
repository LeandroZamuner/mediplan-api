// Package domain contiene las entidades y errores del negocio. No importa nada externo.
package domain

import (
	"errors"
	"time"
)

// Role es solo una preferencia de pantalla inicial al registrarse. NO otorga permisos:
// ser cuidador es un vínculo con un plan concreto (ver PlanCaregiver).
type Role string

const (
	RolePatient   Role = "patient"
	RoleCaregiver Role = "caregiver"
)

func (r Role) Valid() bool { return r == RolePatient || r == RoleCaregiver }

type User struct {
	ID            string
	FirebaseUID   string
	Email         string
	Role          Role
	EmailVerified bool
	// Timezone (IANA, ej. America/Argentina/Buenos_Aires): los horarios de los planes son hora local.
	Timezone  string
	CreatedAt time.Time
}

const DefaultTimezone = "America/Argentina/Buenos_Aires"

var (
	ErrNotFound         = errors.New("no encontrado")
	ErrAlreadyExists    = errors.New("ya existe")
	ErrInvalidRole      = errors.New("rol inválido")
	ErrInvalidToken     = errors.New("token inválido o vencido")
	ErrEmailNotVerified = errors.New("email sin verificar")
	ErrNotRegistered    = errors.New("usuario sin registrar: falta elegir rol")
	ErrInvalidTimezone  = errors.New("zona horaria inválida")
	// ErrTooManyRequests: se pidió demasiadas veces seguidas (límite de correos de cuenta).
	ErrTooManyRequests = errors.New("demasiados pedidos: esperá un minuto y probá de nuevo")
	// ErrMailUnavailable: no se pudo preparar o enviar el correo (el cliente puede usar el correo estándar de Firebase).
	ErrMailUnavailable = errors.New("no pudimos enviar el correo ahora")
)
