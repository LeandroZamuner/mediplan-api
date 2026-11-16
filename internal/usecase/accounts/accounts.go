// Package accounts envía los correos de cuenta (recuperar contraseña y verificar correo) con el diseño de MediPlan.
//
// Firebase solo genera el enlace seguro (código de un solo uso); el correo lo armamos y mandamos nosotros, y el
// enlace apunta a la página /accion de la web de MediPlan en vez de a la pantalla genérica de Firebase.
package accounts

import (
	"context"
	"errors"
	"log"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/platform/ratelimit"
	"github.com/lea/mediplan-api/internal/usecase/auth"
)

// Puertos.

// LinkMaker genera los enlaces de acción de Firebase (Admin SDK).
type LinkMaker interface {
	// PasswordResetLink devuelve domain.ErrNotFound si no hay una cuenta con ese correo.
	PasswordResetLink(ctx context.Context, email string) (string, error)
	VerificationLink(ctx context.Context, email string) (string, error)
}

type Message struct{ To, Subject, HTML, Text string }

type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// Config: WebBaseURL es la raíz de la web (ej. https://usuario.github.io/mediplan-web); LogoURL es opcional.
type Config struct{ WebBaseURL, LogoURL string }

const (
	perAddressWindow = time.Minute // un correo por dirección por minuto
	perIPMax         = 10          // y como mucho 10 pedidos de recuperación por IP cada 15 minutos
	perIPWindow      = 15 * time.Minute
)

type Service struct {
	verifier auth.TokenVerifier
	links    LinkMaker
	mail     Mailer
	cfg      Config
	limiter  *ratelimit.Limiter
}

func NewService(v auth.TokenVerifier, l LinkMaker, m Mailer, cfg Config) *Service {
	cfg.WebBaseURL = strings.TrimRight(cfg.WebBaseURL, "/")
	if cfg.LogoURL == "" {
		cfg.LogoURL = cfg.WebBaseURL + "/brand/email-logo.png"
	}
	return &Service{verifier: v, links: l, mail: m, cfg: cfg, limiter: ratelimit.New()}
}

// SendPasswordReset manda el correo de recuperación. Responde igual exista o no la cuenta (y cuando ya se
// envió uno hace instantes), para que nadie pueda averiguar qué correos están registrados.
func (s *Service) SendPasswordReset(ctx context.Context, email, ip string) error {
	addr, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil || !strings.Contains(addr.Address[strings.Index(addr.Address, "@"):], ".") || addr.Name != "" {
		return domain.ErrInvalidEmail
	}
	to := strings.ToLower(addr.Address)
	if !s.limiter.Allow("reset-ip:"+ip, perIPMax, perIPWindow) {
		return domain.ErrTooManyRequests
	}
	key := "reset:" + to
	if !s.limiter.Allow(key, 1, perAddressWindow) {
		return nil // ya le enviamos uno hace un momento
	}
	link, err := s.links.PasswordResetLink(ctx, to)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		s.limiter.Undo(key)
		log.Printf("accounts: no se pudo generar el enlace de recuperación: %v", err)
		return domain.ErrMailUnavailable
	}
	return s.deliver(ctx, key, to, resetMessage, link)
}

// SendVerification manda el correo de verificación al usuario del token (que todavía no verificó su correo).
// Devuelve false, sin enviar nada, si ya estaba verificado.
func (s *Service) SendVerification(ctx context.Context, idToken string) (bool, error) {
	id, err := s.verifier.Verify(ctx, idToken)
	if err != nil || id.Email == "" {
		return false, domain.ErrInvalidToken
	}
	if id.EmailVerified {
		return false, nil
	}
	key := "verify:" + id.UID
	if !s.limiter.Allow(key, 1, perAddressWindow) {
		return false, domain.ErrTooManyRequests
	}
	link, err := s.links.VerificationLink(ctx, id.Email)
	if err != nil {
		s.limiter.Undo(key)
		log.Printf("accounts: no se pudo generar el enlace de verificación: %v", err)
		return false, domain.ErrMailUnavailable
	}
	if err := s.deliver(ctx, key, id.Email, verifyMessage, link); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) deliver(ctx context.Context, key, to string, build func(content) Message, firebaseLink string) error {
	action, err := ActionURL(s.cfg.WebBaseURL, firebaseLink)
	if err != nil {
		s.limiter.Undo(key)
		log.Printf("accounts: enlace inesperado de Firebase: %v", err)
		return domain.ErrMailUnavailable
	}
	m := build(content{ActionURL: action, LogoURL: s.cfg.LogoURL})
	m.To = to
	if err := s.mail.Send(ctx, m); err != nil {
		s.limiter.Undo(key)
		log.Printf("accounts: falló el envío del correo: %v", err)
		return domain.ErrMailUnavailable
	}
	return nil
}

// ActionURL convierte un enlace de Firebase (a su pantalla genérica) en uno a la página /accion de MediPlan,
// conservando solo el modo y el código de un solo uso.
func ActionURL(webBase, firebaseLink string) (string, error) {
	u, err := url.Parse(firebaseLink)
	if err != nil {
		return "", err
	}
	q := u.Query()
	mode, code := q.Get("mode"), q.Get("oobCode")
	if mode == "" || code == "" {
		return "", errors.New("el enlace no trae modo o código")
	}
	out := url.Values{"mode": {mode}, "oobCode": {code}, "lang": {"es"}}
	return strings.TrimRight(webBase, "/") + "/accion/?" + out.Encode(), nil
}
