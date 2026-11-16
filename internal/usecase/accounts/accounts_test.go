package accounts

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"testing"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/auth"
)

// ---- fakes ----

type fakeVerifier map[string]*auth.Identity

func (f fakeVerifier) Verify(_ context.Context, tok string) (*auth.Identity, error) {
	if id, ok := f[tok]; ok {
		return id, nil
	}
	return nil, errors.New("firma inválida")
}

// fakeLinks genera enlaces como los de Firebase (a su propia página) solo para las cuentas que existen.
type fakeLinks struct {
	accounts map[string]bool
	fail     error
}

func (f fakeLinks) link(mode, email string) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	if !f.accounts[email] {
		return "", domain.ErrNotFound
	}
	return "https://mediplan-4f001.firebaseapp.com/__/auth/action?mode=" + mode + "&oobCode=COD%2B1&apiKey=k&lang=en", nil
}
func (f fakeLinks) PasswordResetLink(_ context.Context, email string) (string, error) {
	return f.link("resetPassword", email)
}
func (f fakeLinks) VerificationLink(_ context.Context, email string) (string, error) {
	return f.link("verifyEmail", email)
}

type fakeMailer struct {
	sent []Message
	err  error
}

func (f *fakeMailer) Send(_ context.Context, m Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

func given(links fakeLinks) (*Service, *fakeMailer) {
	m := &fakeMailer{}
	v := fakeVerifier{
		"sinverificar": {UID: "u1", Email: "ana@mail.com", EmailVerified: false},
		"verificado":   {UID: "u2", Email: "beto@mail.com", EmailVerified: true},
		"sinemail":     {UID: "u3"},
	}
	s := NewService(v, links, m, Config{WebBaseURL: "https://leandrozamuner.github.io/mediplan-web/"})
	return s, m
}

var existing = fakeLinks{accounts: map[string]bool{"ana@mail.com": true, "beto@mail.com": true}}

// ---- ActionURL ----

func TestActionURL(t *testing.T) {
	t.Run("dado un enlace de Firebase, cuando se reescribe, entonces apunta a la página de MediPlan con modo y código", func(t *testing.T) {
		got, err := ActionURL("https://x.github.io/mediplan-web/", "https://p.firebaseapp.com/__/auth/action?mode=resetPassword&oobCode=A%2BB&apiKey=k&lang=en")
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(got)
		if u.Host != "x.github.io" || u.Path != "/mediplan-web/accion/" {
			t.Fatalf("destino inesperado: %s", got)
		}
		if u.Query().Get("mode") != "resetPassword" || u.Query().Get("oobCode") != "A+B" || u.Query().Get("lang") != "es" {
			t.Fatalf("parámetros inesperados: %s", got)
		}
		if strings.Contains(got, "apiKey") {
			t.Fatalf("no debe arrastrar la apiKey: %s", got)
		}
	})
	t.Run("dado un enlace sin código, cuando se reescribe, entonces falla", func(t *testing.T) {
		if _, err := ActionURL("https://x.io", "https://p.firebaseapp.com/__/auth/action?mode=resetPassword"); err == nil {
			t.Fatal("esperaba error")
		}
	})
}

// ---- Recuperar contraseña ----

func TestSendPasswordReset(t *testing.T) {
	ctx := context.Background()

	t.Run("dada una cuenta existente, cuando pide recuperar, entonces recibe un correo de MediPlan con el enlace a la página de acción", func(t *testing.T) {
		// Given
		s, m := given(existing)
		// When
		err := s.SendPasswordReset(ctx, "  Ana@Mail.com ", "1.1.1.1")
		// Then
		if err != nil || len(m.sent) != 1 {
			t.Fatalf("err=%v, enviados=%d", err, len(m.sent))
		}
		msg := m.sent[0]
		want := "https://leandrozamuner.github.io/mediplan-web/accion/?lang=es&mode=resetPassword&oobCode=COD%2B1"
		if msg.To != "ana@mail.com" || !strings.Contains(html.UnescapeString(msg.HTML), want) || !strings.Contains(msg.Text, want) {
			t.Fatalf("correo inesperado: to=%s\n%s", msg.To, msg.HTML)
		}
		if strings.Contains(msg.HTML, "firebaseapp.com") || !strings.Contains(strings.ToLower(msg.Subject), "contraseña") {
			t.Fatalf("asunto o enlace incorrectos: %s", msg.Subject)
		}
	})

	t.Run("dada una cuenta inexistente, cuando pide recuperar, entonces responde igual y no envía nada", func(t *testing.T) {
		s, m := given(existing)
		if err := s.SendPasswordReset(ctx, "nadie@mail.com", "1.1.1.1"); err != nil || len(m.sent) != 0 {
			t.Fatalf("err=%v, enviados=%d", err, len(m.sent))
		}
	})

	t.Run("dado un email mal escrito, cuando pide recuperar, entonces se rechaza", func(t *testing.T) {
		s, _ := given(existing)
		for _, bad := range []string{"", "no-es-mail", "a@b", "a b@c.com"} {
			if err := s.SendPasswordReset(ctx, bad, "1.1.1.1"); !errors.Is(err, domain.ErrInvalidEmail) {
				t.Fatalf("%q: esperaba ErrInvalidEmail y fue %v", bad, err)
			}
		}
	})

	t.Run("dado un pedido reciente del mismo email, cuando se repite, entonces no se envía otro correo", func(t *testing.T) {
		s, m := given(existing)
		_ = s.SendPasswordReset(ctx, "ana@mail.com", "1.1.1.1")
		err := s.SendPasswordReset(ctx, "ana@mail.com", "2.2.2.2")
		if err != nil || len(m.sent) != 1 {
			t.Fatalf("err=%v, enviados=%d", err, len(m.sent))
		}
	})

	t.Run("dada una misma IP que pide demasiado, cuando supera el límite, entonces se rechaza con demasiados pedidos", func(t *testing.T) {
		s, _ := given(existing)
		var err error
		for i := 0; i < 12; i++ {
			err = s.SendPasswordReset(ctx, fmt.Sprintf("u%d@mail.com", i), "9.9.9.9")
		}
		if !errors.Is(err, domain.ErrTooManyRequests) {
			t.Fatalf("esperaba ErrTooManyRequests y fue %v", err)
		}
	})

	t.Run("dado que falla el envío, cuando pide recuperar, entonces informa que el correo no está disponible", func(t *testing.T) {
		s, m := given(existing)
		m.err = errors.New("brevo caído")
		if err := s.SendPasswordReset(ctx, "ana@mail.com", "1.1.1.1"); !errors.Is(err, domain.ErrMailUnavailable) {
			t.Fatalf("esperaba ErrMailUnavailable y fue %v", err)
		}
	})

	t.Run("dado que no se puede generar el enlace, cuando pide recuperar, entonces informa que el correo no está disponible", func(t *testing.T) {
		s, _ := given(fakeLinks{fail: errors.New("sin credenciales")})
		if err := s.SendPasswordReset(ctx, "ana@mail.com", "1.1.1.1"); !errors.Is(err, domain.ErrMailUnavailable) {
			t.Fatalf("esperaba ErrMailUnavailable y fue %v", err)
		}
	})
}

// ---- Verificar correo ----

func TestSendVerification(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un usuario sin verificar, cuando pide el correo, entonces recibe el enlace de verificación de MediPlan", func(t *testing.T) {
		s, m := given(existing)
		sent, err := s.SendVerification(ctx, "sinverificar")
		if err != nil || !sent || len(m.sent) != 1 {
			t.Fatalf("sent=%v err=%v enviados=%d", sent, err, len(m.sent))
		}
		if m.sent[0].To != "ana@mail.com" || !strings.Contains(html.UnescapeString(m.sent[0].HTML), "mode=verifyEmail") || !strings.Contains(m.sent[0].HTML, "/mediplan-web/accion/") {
			t.Fatalf("correo inesperado: %+v", m.sent[0])
		}
	})

	t.Run("dado un usuario ya verificado, cuando pide el correo, entonces no se envía nada", func(t *testing.T) {
		s, m := given(existing)
		sent, err := s.SendVerification(ctx, "verificado")
		if err != nil || sent || len(m.sent) != 0 {
			t.Fatalf("sent=%v err=%v enviados=%d", sent, err, len(m.sent))
		}
	})

	t.Run("dado un token inválido o sin email, cuando pide el correo, entonces se rechaza", func(t *testing.T) {
		s, _ := given(existing)
		for _, tok := range []string{"malo", "sinemail", ""} {
			if _, err := s.SendVerification(ctx, tok); !errors.Is(err, domain.ErrInvalidToken) {
				t.Fatalf("%q: esperaba ErrInvalidToken y fue %v", tok, err)
			}
		}
	})

	t.Run("dado un pedido reciente, cuando se repite enseguida, entonces pide esperar", func(t *testing.T) {
		s, m := given(existing)
		_, _ = s.SendVerification(ctx, "sinverificar")
		if _, err := s.SendVerification(ctx, "sinverificar"); !errors.Is(err, domain.ErrTooManyRequests) {
			t.Fatalf("esperaba ErrTooManyRequests y fue %v", err)
		}
		if len(m.sent) != 1 {
			t.Fatalf("enviados=%d", len(m.sent))
		}
	})

	t.Run("dado que falla el envío, cuando pide el correo, entonces informa que no está disponible y no cuenta el intento", func(t *testing.T) {
		s, m := given(existing)
		m.err = errors.New("brevo caído")
		if _, err := s.SendVerification(ctx, "sinverificar"); !errors.Is(err, domain.ErrMailUnavailable) {
			t.Fatalf("esperaba ErrMailUnavailable y fue %v", err)
		}
		m.err = nil
		if sent, err := s.SendVerification(ctx, "sinverificar"); err != nil || !sent {
			t.Fatalf("el reintento debía enviarse: sent=%v err=%v", sent, err)
		}
	})
}
