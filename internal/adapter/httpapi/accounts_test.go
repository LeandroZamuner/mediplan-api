package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/accounts"
	"github.com/lea/mediplan-api/internal/usecase/auth"
)

type fakeLinkMaker struct{}

func (fakeLinkMaker) PasswordResetLink(_ context.Context, email string) (string, error) {
	if email != "ana@mail.com" {
		return "", domain.ErrNotFound
	}
	return "https://p.firebaseapp.com/__/auth/action?mode=resetPassword&oobCode=C", nil
}
func (fakeLinkMaker) VerificationLink(_ context.Context, _ string) (string, error) {
	return "https://p.firebaseapp.com/__/auth/action?mode=verifyEmail&oobCode=C", nil
}

type fakeMail struct {
	sent int
	err  error
}

func (f *fakeMail) Send(context.Context, accounts.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent++
	return nil
}

func accountsRouter(m *fakeMail) http.Handler {
	v := verifier{
		"sinverificar": {UID: "u2", Email: "ana@mail.com", EmailVerified: false},
		"verificado":   {UID: "u1", Email: "ana@mail.com", EmailVerified: true},
	}
	svc := accounts.NewService(v, fakeLinkMaker{}, m, accounts.Config{WebBaseURL: "https://x.github.io/mediplan-web"})
	return NewRouter(Handlers{Auth: NewAuthHandler(auth.NewService(v, repo{})), Accounts: NewAccountsHandler(svc)})
}

func TestHTTPAccounts(t *testing.T) {
	t.Run("dado un email existente, cuando pide recuperar la contraseña sin sesión, entonces responde 202 y envía el correo", func(t *testing.T) {
		m := &fakeMail{}
		w := do(accountsRouter(m), "POST", "/api/v1/auth/password-reset", "", `{"email":"ana@mail.com"}`)
		if w.Code != 202 || m.sent != 1 {
			t.Fatalf("code=%d enviados=%d", w.Code, m.sent)
		}
	})
	t.Run("dado un email inexistente, cuando pide recuperar, entonces responde igual 202 sin enviar", func(t *testing.T) {
		m := &fakeMail{}
		w := do(accountsRouter(m), "POST", "/api/v1/auth/password-reset", "", `{"email":"otra@mail.com"}`)
		if w.Code != 202 || m.sent != 0 {
			t.Fatalf("code=%d enviados=%d", w.Code, m.sent)
		}
	})
	t.Run("dado un email inválido o un cuerpo roto, cuando pide recuperar, entonces responde 422 o 400", func(t *testing.T) {
		h := accountsRouter(&fakeMail{})
		if w := do(h, "POST", "/api/v1/auth/password-reset", "", `{"email":"nope"}`); w.Code != 422 {
			t.Fatalf("code=%d", w.Code)
		}
		if w := do(h, "POST", "/api/v1/auth/password-reset", "", `{`); w.Code != 400 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("dado que falla el envío, cuando pide recuperar, entonces responde 503", func(t *testing.T) {
		w := do(accountsRouter(&fakeMail{err: errors.New("caído")}), "POST", "/api/v1/auth/password-reset", "", `{"email":"ana@mail.com"}`)
		if w.Code != 503 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("dado un usuario sin verificar, cuando pide el correo de verificación, entonces responde 200 y lo envía", func(t *testing.T) {
		m := &fakeMail{}
		w := do(accountsRouter(m), "POST", "/api/v1/auth/email-verification", "sinverificar", "")
		if w.Code != 200 || m.sent != 1 {
			t.Fatalf("code=%d enviados=%d body=%s", w.Code, m.sent, w.Body)
		}
	})
	t.Run("dado un usuario ya verificado, cuando pide el correo, entonces responde 200 sin enviar", func(t *testing.T) {
		m := &fakeMail{}
		w := do(accountsRouter(m), "POST", "/api/v1/auth/email-verification", "verificado", "")
		if w.Code != 200 || m.sent != 0 {
			t.Fatalf("code=%d enviados=%d", w.Code, m.sent)
		}
	})
	t.Run("dado un token inválido, cuando pide el correo, entonces responde 401", func(t *testing.T) {
		if w := do(accountsRouter(&fakeMail{}), "POST", "/api/v1/auth/email-verification", "malo", ""); w.Code != 401 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("dado un pedido repetido enseguida, cuando pide otra verificación, entonces responde 429", func(t *testing.T) {
		h := accountsRouter(&fakeMail{})
		do(h, "POST", "/api/v1/auth/email-verification", "sinverificar", "")
		if w := do(h, "POST", "/api/v1/auth/email-verification", "sinverificar", ""); w.Code != 429 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("dado que los correos propios no están configurados, cuando se pide, entonces la ruta no existe", func(t *testing.T) {
		if w := do(givenRouter(), "POST", "/api/v1/auth/password-reset", "", `{"email":"ana@mail.com"}`); w.Code != 404 && w.Code != 405 {
			t.Fatalf("code=%d", w.Code)
		}
	})
}
