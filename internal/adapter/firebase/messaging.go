package firebase

import (
	"context"

	fb "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"

	"github.com/lea/mediplan-api/internal/usecase/notifications"
)

// AlertsChannel es el canal de notificaciones de Android para avisos importantes
// (la app Expo lo crea con prioridad alta y sonido).
const AlertsChannel = "dose-alerts"

// Pusher envía notificaciones push con Firebase Cloud Messaging.
// A diferencia de verificar tokens, ENVIAR sí requiere credenciales: en Cloud Run se
// toman solas de la cuenta de servicio del servicio; en local, de GOOGLE_APPLICATION_CREDENTIALS.
type Pusher struct{ client *messaging.Client }

func NewPusher(ctx context.Context, projectID string) (*Pusher, error) {
	app, err := fb.NewApp(ctx, &fb.Config{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	c, err := app.Messaging(ctx)
	if err != nil {
		return nil, err
	}
	return &Pusher{client: c}, nil
}

// Send manda el mensaje a todos los tokens y devuelve los que FCM informa como ya inválidos.
func (p *Pusher) Send(ctx context.Context, tokens []string, msg notifications.PushMessage) ([]string, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	resp, err := p.client.SendEachForMulticast(ctx, buildMulticast(tokens, msg))
	if err != nil {
		return nil, err
	}
	return invalidTokens(tokens, resp, messaging.IsUnregistered), nil
}

func buildMulticast(tokens []string, msg notifications.PushMessage) *messaging.MulticastMessage {
	m := &messaging.MulticastMessage{
		Tokens: tokens,
		Data:   msg.Data,
		// Prioridad alta: sin ella, Android puede demorar el mensaje cuando el celular está en reposo.
		Android: &messaging.AndroidConfig{Priority: "high"},
	}
	if !msg.Silent {
		m.Notification = &messaging.Notification{Title: msg.Title, Body: msg.Body}
		m.Android.Notification = &messaging.AndroidNotification{ChannelID: AlertsChannel}
	}
	return m
}

// invalidTokens elige los tokens cuyo envío falló porque ya no sirven (app desinstalada, etc.).
// isInvalid se inyecta para poder probarlo sin errores internos de FCM.
func invalidTokens(tokens []string, resp *messaging.BatchResponse, isInvalid func(error) bool) []string {
	if resp == nil {
		return nil
	}
	var out []string
	for i, r := range resp.Responses {
		if i < len(tokens) && !r.Success && r.Error != nil && isInvalid(r.Error) {
			out = append(out, tokens[i])
		}
	}
	return out
}
