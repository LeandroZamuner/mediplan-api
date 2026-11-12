package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/lea/mediplan-api/internal/domain"
)

// ---- fakes ----

type fakeVerifier map[string]*Identity

func (f fakeVerifier) Verify(_ context.Context, tok string) (*Identity, error) {
	if id, ok := f[tok]; ok {
		return id, nil
	}
	return nil, errors.New("firma inválida")
}

type fakeUsers struct{ byUID map[string]*domain.User }

func (f *fakeUsers) ByFirebaseUID(_ context.Context, uid string) (*domain.User, error) {
	if u, ok := f.byUID[uid]; ok {
		c := *u
		return &c, nil
	}
	return nil, domain.ErrNotFound
}

func (f *fakeUsers) SetTimezone(_ context.Context, userID, tz string) error {
	for _, u := range f.byUID {
		if u.ID == userID {
			u.Timezone = tz
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeUsers) Delete(_ context.Context, userID string) error {
	for uid, u := range f.byUID {
		if u.ID == userID {
			delete(f.byUID, uid)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeUsers) Create(_ context.Context, u *domain.User) error {
	if _, ok := f.byUID[u.FirebaseUID]; ok {
		return domain.ErrAlreadyExists
	}
	u.ID = "id-" + u.FirebaseUID
	c := *u
	f.byUID[u.FirebaseUID] = &c
	return nil
}

// givenService arma el servicio con un token verificado ("ok") y uno sin email verificado ("sinverificar").
func givenService() *Service {
	v := fakeVerifier{
		"ok":           {UID: "u1", Email: "ana@mail.com", EmailVerified: true},
		"sinverificar": {UID: "u2", Email: "beto@mail.com", EmailVerified: false},
	}
	return NewService(v, &fakeUsers{byUID: map[string]*domain.User{}})
}

// ---- Authenticate ----

func TestAuthenticate(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un token válido sin usuario registrado, cuando se autentica, entonces pide elegir rol", func(t *testing.T) {
		// Given
		s := givenService()
		// When
		_, err := s.Authenticate(ctx, "ok")
		// Then
		if !errors.Is(err, domain.ErrNotRegistered) {
			t.Fatalf("esperaba ErrNotRegistered, got %v", err)
		}
	})

	t.Run("dado un usuario registrado, cuando se autentica, entonces devuelve ese usuario", func(t *testing.T) {
		// Given
		s := givenService()
		registrado, _ := s.Register(ctx, "ok", domain.RoleCaregiver)
		// When
		u, err := s.Authenticate(ctx, "ok")
		// Then
		if err != nil || u.ID != registrado.ID {
			t.Fatalf("esperaba el usuario registrado, got %+v err=%v", u, err)
		}
	})

	t.Run("dado un token inválido, cuando se autentica, entonces es rechazado", func(t *testing.T) {
		// Given
		s := givenService()
		// When
		_, err := s.Authenticate(ctx, "basura")
		// Then
		if !errors.Is(err, domain.ErrInvalidToken) {
			t.Fatalf("esperaba ErrInvalidToken, got %v", err)
		}
	})

	t.Run("dado un token con email sin verificar, cuando se autentica, entonces es rechazado", func(t *testing.T) {
		// Given
		s := givenService()
		// When
		_, err := s.Authenticate(ctx, "sinverificar")
		// Then
		if !errors.Is(err, domain.ErrEmailNotVerified) {
			t.Fatalf("esperaba ErrEmailNotVerified, got %v", err)
		}
	})
}

// ---- Register ----

func TestRegister(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un token válido, cuando elige rol cuidador, entonces se crea el usuario con ese rol y el email del token", func(t *testing.T) {
		// Given
		s := givenService()
		// When
		u, err := s.Register(ctx, "ok", domain.RoleCaregiver)
		// Then
		if err != nil || u.Role != domain.RoleCaregiver || u.Email != "ana@mail.com" || !u.EmailVerified {
			t.Fatalf("usuario inesperado: %+v err=%v", u, err)
		}
	})

	t.Run("dado un usuario ya registrado como paciente, cuando se registra de nuevo como cuidador, entonces conserva el rol original", func(t *testing.T) {
		// Given
		s := givenService()
		_, _ = s.Register(ctx, "ok", domain.RolePatient)
		// When
		u, err := s.Register(ctx, "ok", domain.RoleCaregiver)
		// Then
		if err != nil || u.Role != domain.RolePatient {
			t.Fatalf("debería conservar el rol paciente: %+v err=%v", u, err)
		}
	})

	t.Run("dado un rol inexistente, cuando se registra, entonces es rechazado", func(t *testing.T) {
		// Given
		s := givenService()
		// When
		_, err := s.Register(ctx, "ok", "admin")
		// Then
		if !errors.Is(err, domain.ErrInvalidRole) {
			t.Fatalf("esperaba ErrInvalidRole, got %v", err)
		}
	})

	t.Run("dado un email sin verificar, cuando se registra, entonces es rechazado y no se crea el usuario", func(t *testing.T) {
		// Given
		s := givenService()
		// When
		_, err := s.Register(ctx, "sinverificar", domain.RolePatient)
		// Then
		if !errors.Is(err, domain.ErrEmailNotVerified) {
			t.Fatalf("esperaba ErrEmailNotVerified, got %v", err)
		}
		if _, err := s.users.ByFirebaseUID(ctx, "u2"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("no debería existir el usuario, got %v", err)
		}
	})
}

// ---- Zona horaria ----

func TestTimezone(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un usuario recién registrado, cuando se consulta su zona horaria, entonces tiene la de Argentina por defecto", func(t *testing.T) {
		s := givenService()
		u, _ := s.Register(ctx, "ok", domain.RolePatient)
		if u.Timezone != domain.DefaultTimezone {
			t.Fatalf("esperaba %q, got %q", domain.DefaultTimezone, u.Timezone)
		}
	})

	t.Run("dado un usuario, cuando informa una zona horaria válida, entonces se guarda", func(t *testing.T) {
		s := givenService()
		u, _ := s.Register(ctx, "ok", domain.RolePatient)
		got, err := s.SetTimezone(ctx, u, "America/Montevideo")
		if err != nil || got.Timezone != "America/Montevideo" {
			t.Fatalf("got %+v err=%v", got, err)
		}
		if again, _ := s.Authenticate(ctx, "ok"); again.Timezone != "America/Montevideo" {
			t.Fatalf("no quedó guardada: %q", again.Timezone)
		}
	})

	t.Run("dado un usuario, cuando informa una zona inválida, entonces es rechazada y no cambia", func(t *testing.T) {
		s := givenService()
		u, _ := s.Register(ctx, "ok", domain.RolePatient)
		for _, tz := range []string{"", "Marte/Olympus", "Local", "GMT-3 raro"} {
			if _, err := s.SetTimezone(ctx, u, tz); !errors.Is(err, domain.ErrInvalidTimezone) {
				t.Errorf("%q: esperaba ErrInvalidTimezone, got %v", tz, err)
			}
		}
		if again, _ := s.Authenticate(ctx, "ok"); again.Timezone != domain.DefaultTimezone {
			t.Fatalf("no debería haber cambiado: %q", again.Timezone)
		}
	})
}

func TestDeleteAccount(t *testing.T) {
	t.Run("dado un usuario registrado, cuando elimina su cuenta, entonces ya no se lo encuentra", func(t *testing.T) {
		s := givenService()
		u, err := s.Register(context.Background(), "ok", domain.RolePatient)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteAccount(context.Background(), u); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := s.Authenticate(context.Background(), "ok"); !errors.Is(err, domain.ErrNotRegistered) {
			t.Fatalf("esperaba ErrNotRegistered tras borrar, got %v", err)
		}
	})

	t.Run("dado un usuario que ya no existe, cuando intenta eliminarlo de nuevo, entonces no se encuentra", func(t *testing.T) {
		s := givenService()
		if err := s.DeleteAccount(context.Background(), &domain.User{ID: "fantasma"}); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
	})
}
