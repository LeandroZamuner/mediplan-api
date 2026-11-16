// Package mail reúne enviadores de correo auxiliares.
package mail

import (
	"context"
	"log"

	"github.com/lea/mediplan-api/internal/usecase/accounts"
)

// LogMailer no envía nada: escribe el correo en el log. Es para desarrollo local (con el emulador de Auth),
// donde se copia el enlace del log en vez de usar una casilla real.
type LogMailer struct{}

func (LogMailer) Send(_ context.Context, m accounts.Message) error {
	log.Printf("correo simulado a %s — %s\n%s", m.To, m.Subject, m.Text)
	return nil
}
