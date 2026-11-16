package firebase

import (
	"context"

	fb "firebase.google.com/go/v4"
	fbauth "firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"

	"github.com/lea/mediplan-api/internal/domain"
)

// Links genera los enlaces de recuperación y verificación con el Admin SDK. A diferencia de verificar tokens,
// esto SÍ requiere credenciales de una cuenta de servicio (credentialsJSON). Con el emulador de Auth
// (FIREBASE_AUTH_EMULATOR_HOST) no hacen falta.
type Links struct{ client *fbauth.Client }

func NewLinks(ctx context.Context, projectID string, credentialsJSON []byte, emulator bool) (*Links, error) {
	var opts []option.ClientOption
	switch {
	case len(credentialsJSON) > 0:
		opts = append(opts, option.WithCredentialsJSON(credentialsJSON))
	case emulator:
		opts = append(opts, option.WithoutAuthentication())
	}
	app, err := fb.NewApp(ctx, &fb.Config{ProjectID: projectID}, opts...)
	if err != nil {
		return nil, err
	}
	c, err := app.Auth(ctx)
	if err != nil {
		return nil, err
	}
	return &Links{client: c}, nil
}

func (l *Links) PasswordResetLink(ctx context.Context, email string) (string, error) {
	link, err := l.client.PasswordResetLink(ctx, email)
	return link, notFound(err)
}

func (l *Links) VerificationLink(ctx context.Context, email string) (string, error) {
	link, err := l.client.EmailVerificationLink(ctx, email)
	return link, notFound(err)
}

func notFound(err error) error {
	if err != nil && (fbauth.IsUserNotFound(err) || fbauth.IsEmailNotFound(err)) {
		return domain.ErrNotFound
	}
	return err
}
