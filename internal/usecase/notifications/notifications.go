// Package notifications maneja los celulares registrados (tokens de FCM) y los avisos a usuarios.
//
// Hay dos tipos de mensaje:
//   - Aviso visible (Notify): se guarda para mostrarlo en la app y se envía como push.
//   - Sincronización silenciosa (Sync): solo un push de datos para que el celular del
//     paciente vuelva a consultar el plan y reprograme sus alarmas locales.
package notifications

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

const listLimit = 50

// PushMessage es lo que se manda a Firebase Cloud Messaging.
type PushMessage struct {
	Title  string
	Body   string
	Data   map[string]string
	Silent bool // solo datos, sin notificación visible
}

// Puertos.

type DeviceRepo interface {
	Upsert(ctx context.Context, userID, token, platform string) error // el token pasa a ser de ese usuario
	Delete(ctx context.Context, userID, token string) error           // solo si es de ese usuario
	DeleteTokens(ctx context.Context, tokens []string) error
	TokensOf(ctx context.Context, userID string) ([]string, error)
}

type NotificationRepo interface {
	Create(ctx context.Context, n *domain.Notification) error                                 // completa ID
	ListByUser(ctx context.Context, userID string, limit int) ([]*domain.Notification, error) // más nuevas primero
	MarkRead(ctx context.Context, userID, id string, at time.Time) error                      // ErrNotFound si no es suyo
}

// Pusher envía a Firebase. Devuelve los tokens que ya no sirven (app desinstalada, etc.).
type Pusher interface {
	Send(ctx context.Context, tokens []string, msg PushMessage) (invalidTokens []string, err error)
}

type Service struct {
	devices DeviceRepo
	notifs  NotificationRepo
	push    Pusher
	now     func() time.Time
}

func NewService(d DeviceRepo, n NotificationRepo, p Pusher) *Service {
	return &Service{devices: d, notifs: n, push: p, now: time.Now}
}

func (s *Service) RegisterDevice(ctx context.Context, actor *domain.User, token, platform string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return domain.ErrInvalidDeviceToken
	}
	if platform = strings.TrimSpace(platform); platform == "" {
		platform = "android"
	}
	return s.devices.Upsert(ctx, actor.ID, token, platform)
}

func (s *Service) UnregisterDevice(ctx context.Context, actor *domain.User, token string) error {
	return s.devices.Delete(ctx, actor.ID, strings.TrimSpace(token))
}

// Notify guarda el aviso y lo envía a los celulares del usuario. Un fallo del push
// no hace fallar la operación: el aviso ya quedó guardado y se verá en la app.
func (s *Service) Notify(ctx context.Context, userID string, n domain.Notification) error {
	n.UserID, n.CreatedAt = userID, s.now()
	if err := s.notifs.Create(ctx, &n); err != nil {
		return err
	}
	data := map[string]string{"kind": n.Kind, "notificationId": n.ID}
	for k, v := range n.Data {
		data[k] = v
	}
	s.send(ctx, userID, PushMessage{Title: n.Title, Body: n.Body, Data: data})
	return nil
}

// Sync manda un push silencioso de sincronización (no se guarda ni se ve en la lista).
func (s *Service) Sync(ctx context.Context, userID string, data map[string]string) error {
	d := map[string]string{"kind": domain.NotifPlanSync}
	for k, v := range data {
		d[k] = v
	}
	s.send(ctx, userID, PushMessage{Data: d, Silent: true})
	return nil
}

func (s *Service) List(ctx context.Context, actor *domain.User) ([]*domain.Notification, error) {
	return s.notifs.ListByUser(ctx, actor.ID, listLimit)
}

func (s *Service) MarkRead(ctx context.Context, actor *domain.User, id string) error {
	return s.notifs.MarkRead(ctx, actor.ID, id, s.now())
}

func (s *Service) send(ctx context.Context, userID string, msg PushMessage) {
	tokens, err := s.devices.TokensOf(ctx, userID)
	if err != nil || len(tokens) == 0 {
		return
	}
	invalid, err := s.push.Send(ctx, tokens, msg)
	if err != nil {
		log.Printf("push a %s falló: %v", userID, err)
	}
	if len(invalid) > 0 {
		_ = s.devices.DeleteTokens(ctx, invalid)
	}
}
