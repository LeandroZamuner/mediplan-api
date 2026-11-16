package mail

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/lea/mediplan-api/internal/usecase/accounts"
)

func TestLogMailer(t *testing.T) {
	t.Run("dado un mensaje, cuando se envía, entonces queda en el log con el destinatario y el texto (con el enlace)", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(nil)
		err := LogMailer{}.Send(context.Background(), accounts.Message{To: "ana@mail.com", Subject: "Hola", Text: "abrí https://x/accion/?oobCode=1"})
		if err != nil || !strings.Contains(buf.String(), "ana@mail.com") || !strings.Contains(buf.String(), "oobCode=1") {
			t.Fatalf("err=%v log=%q", err, buf.String())
		}
	})
}
