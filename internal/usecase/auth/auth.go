// Package auth autentica con tokens de Firebase Auth. El alta, la verificación
// de email y la recuperación de contraseña las resuelve Firebase; acá solo
// se valida la identidad y se mantiene el usuario del dominio (rol, perfil).
package auth

import (
	"context"
	"strings"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

// Identity es lo que afirma un ID token de Firebase ya validado.
type Identity struct {
	UID           string
	Email         string
	EmailVerified bool
}

// Puertos.

type TokenVerifier interface {
	// Verify devuelve ErrInvalidToken si el token no es válido o venció.
	Verify(ctx context.Context, idToken string) (*Identity, error)
}

type UserRepo interface {
	ByFirebaseUID(ctx context.Context, uid string) (*domain.User, error) // ErrNotFound
	SetTimezone(ctx context.Context, userID, tz string) error            // ErrNotFound
	Create(ctx context.Context, u *domain.User) error                    // completa ID; ErrAlreadyExists si el uid o email ya existen
}

type Service struct {
	verifier TokenVerifier
	users    UserRepo
	now      func() time.Time
}

func NewService(v TokenVerifier, u UserRepo) *Service {
	return &Service{verifier: v, users: u, now: time.Now}
}

// identity valida el token y exige email verificado.
func (s *Service) identity(ctx context.Context, idToken string) (*Identity, error) {
	id, err := s.verifier.Verify(ctx, idToken)
	if err != nil {
		return nil, domain.ErrInvalidToken
	}
	if !id.EmailVerified {
		return nil, domain.ErrEmailNotVerified
	}
	return id, nil
}

// Authenticate devuelve el usuario del token. ErrNotRegistered si todavía no eligió rol.
func (s *Service) Authenticate(ctx context.Context, idToken string) (*domain.User, error) {
	id, err := s.identity(ctx, idToken)
	if err != nil {
		return nil, err
	}
	u, err := s.users.ByFirebaseUID(ctx, id.UID)
	if err == domain.ErrNotFound {
		return nil, domain.ErrNotRegistered
	}
	return u, err
}

// Register crea el usuario del dominio con el rol elegido. Es idempotente:
// si ya existe, devuelve el existente sin cambiar su rol.
func (s *Service) Register(ctx context.Context, idToken string, role domain.Role) (*domain.User, error) {
	if !role.Valid() {
		return nil, domain.ErrInvalidRole
	}
	id, err := s.identity(ctx, idToken)
	if err != nil {
		return nil, err
	}
	if u, err := s.users.ByFirebaseUID(ctx, id.UID); err == nil {
		return u, nil
	} else if err != domain.ErrNotFound {
		return nil, err
	}
	u := &domain.User{FirebaseUID: id.UID, Email: id.Email, Role: role, EmailVerified: true, Timezone: domain.DefaultTimezone, CreatedAt: s.now()}
	if err := s.users.Create(ctx, u); err != nil {
		if err == domain.ErrAlreadyExists { // carrera: lo creó otro request
			return s.users.ByFirebaseUID(ctx, id.UID)
		}
		return nil, err
	}
	return u, nil
}

// SetTimezone guarda la zona horaria (nombre IANA) del usuario.
func (s *Service) SetTimezone(ctx context.Context, actor *domain.User, tz string) (*domain.User, error) {
	if tz == "" || strings.EqualFold(tz, "local") {
		return nil, domain.ErrInvalidTimezone
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return nil, domain.ErrInvalidTimezone
	}
	if err := s.users.SetTimezone(ctx, actor.ID, tz); err != nil {
		return nil, err
	}
	updated := *actor
	updated.Timezone = tz
	return &updated, nil
}
