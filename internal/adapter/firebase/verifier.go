// Package firebase valida ID tokens de Firebase Auth. Solo necesita el ID del
// proyecto: la verificación usa las claves públicas de Google. En desarrollo,
// si existe FIREBASE_AUTH_EMULATOR_HOST, el SDK usa el emulador.
package firebase

import (
	"context"

	fb "firebase.google.com/go/v4"
	fbauth "firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"

	"github.com/lea/mediplan-api/internal/usecase/auth"
)

type Verifier struct{ client *fbauth.Client }

func NewVerifier(ctx context.Context, projectID string) (*Verifier, error) {
	// WithoutAuthentication: verificar ID tokens solo usa las claves públicas de Google,
	// así que no hace falta ninguna credencial (ni cuenta de servicio) en el servidor.
	app, err := fb.NewApp(ctx, &fb.Config{ProjectID: projectID}, option.WithoutAuthentication())
	if err != nil {
		return nil, err
	}
	c, err := app.Auth(ctx)
	if err != nil {
		return nil, err
	}
	return &Verifier{client: c}, nil
}

func (v *Verifier) Verify(ctx context.Context, idToken string) (*auth.Identity, error) {
	t, err := v.client.VerifyIDToken(ctx, idToken)
	if err != nil {
		return nil, err
	}
	id := &auth.Identity{UID: t.UID}
	if email, ok := t.Claims["email"].(string); ok {
		id.Email = email
	}
	if ev, ok := t.Claims["email_verified"].(bool); ok {
		id.EmailVerified = ev
	}
	return id, nil
}
