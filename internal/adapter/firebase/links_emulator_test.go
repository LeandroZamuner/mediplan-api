package firebase

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/accounts"
)

// Prueba de integración con el emulador de Auth: se saltea si no está corriendo (FIREBASE_AUTH_EMULATOR_HOST).
func TestLinksConEmulador(t *testing.T) {
	host := os.Getenv("FIREBASE_AUTH_EMULATOR_HOST")
	if host == "" {
		t.Skip("sin emulador de Auth")
	}
	ctx := context.Background()
	// El emulador acepta la API de administración con el token "owner": crea la cuenta en el proyecto del test.
	body := `{"email":"enlaces@mail.com","password":"secreto123","localId":"enlaces-uid"}`
	req, _ := http.NewRequest("POST", "http://"+host+"/identitytoolkit.googleapis.com/v1/projects/demo-mediplan/accounts", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer owner")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("el emulador no responde: %v", err)
	}
	resp.Body.Close()

	l, err := NewLinks(ctx, "demo-mediplan", nil, true)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("dada una cuenta existente, cuando se pide el enlace de recuperación, entonces se puede convertir al de MediPlan", func(t *testing.T) {
		link, err := l.PasswordResetLink(ctx, "enlaces@mail.com")
		if err != nil {
			t.Fatal(err)
		}
		got, err := accounts.ActionURL("https://x.github.io/mediplan-web", link)
		if err != nil || !strings.Contains(got, "mode=resetPassword") || !strings.Contains(got, "oobCode=") {
			t.Fatalf("err=%v enlace=%s (original %s)", err, got, link)
		}
	})
	t.Run("dada una cuenta existente, cuando se pide el enlace de verificación, entonces trae modo verifyEmail", func(t *testing.T) {
		link, err := l.VerificationLink(ctx, "enlaces@mail.com")
		if err != nil || !strings.Contains(link, "mode=verifyEmail") {
			t.Fatalf("err=%v link=%s", err, link)
		}
	})
	t.Run("dada una cuenta inexistente, cuando se pide el enlace, entonces devuelve ErrNotFound", func(t *testing.T) {
		if _, err := l.PasswordResetLink(ctx, "nadie@mail.com"); err != domain.ErrNotFound {
			t.Fatalf("esperaba ErrNotFound y fue %v", err)
		}
	})
}
