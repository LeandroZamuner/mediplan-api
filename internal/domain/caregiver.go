package domain

import (
	"errors"
	"time"
)

type InviteStatus string

const (
	InvitePending  InviteStatus = "pending"
	InviteAccepted InviteStatus = "accepted"
	InviteRejected InviteStatus = "rejected"
)

// PlanCaregiver vincula a una persona con UN plan, para supervisarlo.
// Mientras la invitación está pendiente solo se conoce el email (CaregiverID vacío).
type PlanCaregiver struct {
	ID          string
	PlanID      string
	InviteEmail string
	CaregiverID string
	Status      InviteStatus
	TokenHash   string // hash del token del enlace; el token en claro nunca se guarda
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

var (
	ErrAlreadyInvited    = errors.New("ya hay una invitación o un vínculo para ese email en este plan")
	ErrInvitationExpired = errors.New("la invitación venció")
	ErrInvalidEmail      = errors.New("email inválido")
)
