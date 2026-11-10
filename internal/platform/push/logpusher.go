// Package push tiene un Pusher de reemplazo para cuando no hay credenciales de Firebase.
package push

import (
	"context"
	"log"

	"github.com/lea/mediplan-api/internal/usecase/notifications"
)

// LogPusher no envía nada: registra en el log lo que enviaría. Sirve en desarrollo.
type LogPusher struct{}

func (LogPusher) Send(_ context.Context, tokens []string, msg notifications.PushMessage) ([]string, error) {
	log.Printf("[push simulado] a %d dispositivo(s): %q %q silencioso=%v datos=%v", len(tokens), msg.Title, msg.Body, msg.Silent, msg.Data)
	return nil, nil
}
