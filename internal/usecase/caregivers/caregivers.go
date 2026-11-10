// Package caregivers maneja las invitaciones y los vínculos cuidador-plan.
//
// Reglas:
//   - Ser cuidador es una relación con UN plan, no un rol de la cuenta.
//   - Solo el dueño del plan invita y quita cuidadores (siempre puede, aunque ya tenga uno).
//   - La invitación queda atada a un email. Para aceptarla hace falta una cuenta con
//     ese mismo email VERIFICADO: un enlace reenviado por error no da acceso.
//   - Se acepta con el enlace (token) o, sin enlace, desde la lista de pendientes del email.
//   - El token en claro solo existe en el enlace; en la base se guarda su hash.
package caregivers

import (
	"context"
	"net/mail"
	"strings"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

const inviteTTL = 7 * 24 * time.Hour

// Puertos.

type InviteRepo interface {
	Create(ctx context.Context, c *domain.PlanCaregiver) error                   // completa ID
	ByID(ctx context.Context, id string) (*domain.PlanCaregiver, error)          // ErrNotFound
	ByTokenHash(ctx context.Context, hash string) (*domain.PlanCaregiver, error) // ErrNotFound
	ByPlan(ctx context.Context, planID string) ([]*domain.PlanCaregiver, error)
	PendingByEmail(ctx context.Context, email string) ([]*domain.PlanCaregiver, error)
	Update(ctx context.Context, c *domain.PlanCaregiver) error
	Delete(ctx context.Context, id string) error
}

type PlanReader interface {
	ByID(ctx context.Context, id string) (*domain.Plan, error) // ErrNotFound
}

type TokenGen interface {
	New() (raw, hash string, err error)
	Hash(raw string) string
}

type Service struct {
	invites InviteRepo
	plans   PlanReader
	tokens  TokenGen
	now     func() time.Time
}

func NewService(i InviteRepo, p PlanReader, t TokenGen) *Service {
	return &Service{invites: i, plans: p, tokens: t, now: time.Now}
}

// Invite crea una invitación pendiente y devuelve el token en claro para armar el enlace.
func (s *Service) Invite(ctx context.Context, actor *domain.User, planID, email string) (*domain.PlanCaregiver, string, error) {
	plan, err := s.plans.ByID(ctx, planID)
	if err != nil {
		return nil, "", err
	}
	if plan.PatientID != actor.ID {
		return nil, "", domain.ErrForbidden
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if _, err := mail.ParseAddress(email); err != nil || !strings.Contains(email, "@") || email == strings.ToLower(actor.Email) {
		return nil, "", domain.ErrInvalidEmail
	}
	existing, err := s.invites.ByPlan(ctx, planID)
	if err != nil {
		return nil, "", err
	}
	for _, c := range existing {
		if c.InviteEmail == email && c.Status != domain.InviteRejected {
			return nil, "", domain.ErrAlreadyInvited
		}
	}
	raw, hash, err := s.tokens.New()
	if err != nil {
		return nil, "", err
	}
	now := s.now()
	c := &domain.PlanCaregiver{
		PlanID: planID, InviteEmail: email, Status: domain.InvitePending,
		TokenHash: hash, ExpiresAt: now.Add(inviteTTL), CreatedAt: now,
	}
	if err := s.invites.Create(ctx, c); err != nil {
		return nil, "", err
	}
	return c, raw, nil
}

// Preview es lo que ve la página pública del enlace (sin sesión): solo el nombre del plan.
type Preview struct {
	PlanName  string
	ExpiresAt time.Time
}

func (s *Service) Preview(ctx context.Context, rawToken string) (*Preview, error) {
	c, err := s.pendingByToken(ctx, rawToken)
	if err != nil {
		return nil, err
	}
	plan, err := s.plans.ByID(ctx, c.PlanID)
	if err != nil {
		return nil, err
	}
	return &Preview{PlanName: plan.Name, ExpiresAt: c.ExpiresAt}, nil
}

// Accept acepta con el token del enlace.
func (s *Service) Accept(ctx context.Context, actor *domain.User, rawToken string) (*domain.PlanCaregiver, error) {
	c, err := s.pendingByToken(ctx, rawToken)
	if err != nil {
		return nil, err
	}
	return s.accept(ctx, actor, c)
}

// AcceptByID acepta una invitación pendiente dirigida al email del usuario (sin enlace).
func (s *Service) AcceptByID(ctx context.Context, actor *domain.User, id string) (*domain.PlanCaregiver, error) {
	c, err := s.invites.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.checkAddressee(actor, c); err != nil {
		return nil, err
	}
	if c.Status != domain.InvitePending {
		return nil, domain.ErrNotFound
	}
	return s.accept(ctx, actor, c)
}

func (s *Service) Reject(ctx context.Context, actor *domain.User, id string) error {
	c, err := s.invites.ByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.checkAddressee(actor, c); err != nil {
		return err
	}
	if c.Status != domain.InvitePending {
		return domain.ErrNotFound
	}
	c.Status = domain.InviteRejected
	return s.invites.Update(ctx, c)
}

// ListPending devuelve las invitaciones vigentes dirigidas al email verificado del usuario.
func (s *Service) ListPending(ctx context.Context, actor *domain.User) ([]*domain.PlanCaregiver, error) {
	if !actor.EmailVerified {
		return nil, domain.ErrEmailNotVerified
	}
	all, err := s.invites.PendingByEmail(ctx, strings.ToLower(actor.Email))
	if err != nil {
		return nil, err
	}
	var out []*domain.PlanCaregiver
	for _, c := range all {
		if !s.now().After(c.ExpiresAt) {
			out = append(out, c)
		}
	}
	return out, nil
}

// Revoke: el dueño del plan quita un vínculo (o cancela una invitación).
func (s *Service) Revoke(ctx context.Context, actor *domain.User, planID, linkID string) error {
	plan, err := s.plans.ByID(ctx, planID)
	if err != nil {
		return err
	}
	if plan.PatientID != actor.ID {
		return domain.ErrForbidden
	}
	c, err := s.invites.ByID(ctx, linkID)
	if err != nil || c.PlanID != planID {
		return domain.ErrNotFound
	}
	return s.invites.Delete(ctx, c.ID)
}

// Leave: la cuidadora deja de seguir el plan.
func (s *Service) Leave(ctx context.Context, actor *domain.User, planID string) error {
	links, err := s.invites.ByPlan(ctx, planID)
	if err != nil {
		return err
	}
	for _, c := range links {
		if c.Status == domain.InviteAccepted && c.CaregiverID == actor.ID {
			return s.invites.Delete(ctx, c.ID)
		}
	}
	return domain.ErrNotFound
}

// ListCaregivers: el dueño y los cuidadores aceptados del plan ven quién lo supervisa.
func (s *Service) ListCaregivers(ctx context.Context, actor *domain.User, planID string) ([]*domain.PlanCaregiver, error) {
	plan, err := s.plans.ByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	links, err := s.invites.ByPlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	if plan.PatientID != actor.ID {
		member := false
		for _, c := range links {
			if c.Status == domain.InviteAccepted && c.CaregiverID == actor.ID {
				member = true
			}
		}
		if !member {
			return nil, domain.ErrForbidden
		}
	}
	return links, nil
}

// ---- internos ----

// pendingByToken busca por hash; solo sirve si está pendiente y vigente.
func (s *Service) pendingByToken(ctx context.Context, raw string) (*domain.PlanCaregiver, error) {
	c, err := s.invites.ByTokenHash(ctx, s.tokens.Hash(raw))
	if err != nil {
		return nil, err
	}
	if c.Status != domain.InvitePending {
		return nil, domain.ErrNotFound
	}
	if s.now().After(c.ExpiresAt) {
		return nil, domain.ErrInvitationExpired
	}
	return c, nil
}

// checkAddressee exige email verificado y que coincida con el de la invitación.
func (s *Service) checkAddressee(actor *domain.User, c *domain.PlanCaregiver) error {
	if !actor.EmailVerified {
		return domain.ErrEmailNotVerified
	}
	if strings.ToLower(actor.Email) != c.InviteEmail {
		return domain.ErrForbidden
	}
	return nil
}

func (s *Service) accept(ctx context.Context, actor *domain.User, c *domain.PlanCaregiver) (*domain.PlanCaregiver, error) {
	if err := s.checkAddressee(actor, c); err != nil {
		return nil, err
	}
	if s.now().After(c.ExpiresAt) {
		return nil, domain.ErrInvitationExpired
	}
	plan, err := s.plans.ByID(ctx, c.PlanID)
	if err != nil {
		return nil, err
	}
	if plan.PatientID == actor.ID {
		return nil, domain.ErrForbidden // nadie supervisa su propio plan
	}
	c.CaregiverID = actor.ID
	c.Status = domain.InviteAccepted
	if err := s.invites.Update(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}
