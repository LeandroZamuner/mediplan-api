package firebase

import (
	"errors"
	"testing"

	"firebase.google.com/go/v4/messaging"

	"github.com/lea/mediplan-api/internal/usecase/notifications"
)

func TestBuildMulticast(t *testing.T) {
	t.Run("dado un aviso visible, cuando se arma el mensaje, entonces lleva título, texto, datos, prioridad alta y el canal de alarmas", func(t *testing.T) {
		m := buildMulticast([]string{"t1", "t2"}, notifications.PushMessage{
			Title: "Dosis no registrada", Body: "Ana no registró Enalapril", Data: map[string]string{"planId": "p1"},
		})
		if len(m.Tokens) != 2 || m.Notification == nil || m.Notification.Title != "Dosis no registrada" || m.Notification.Body != "Ana no registró Enalapril" {
			t.Fatalf("notificación incorrecta: %+v", m)
		}
		if m.Data["planId"] != "p1" || m.Android == nil || m.Android.Priority != "high" ||
			m.Android.Notification == nil || m.Android.Notification.ChannelID != AlertsChannel {
			t.Fatalf("android/datos incorrectos: %+v", m.Android)
		}
	})

	t.Run("dado un mensaje silencioso de sincronización, cuando se arma, entonces no lleva notificación visible pero sí prioridad alta para despertar el celular", func(t *testing.T) {
		m := buildMulticast([]string{"t1"}, notifications.PushMessage{Data: map[string]string{"kind": "plan_sync"}, Silent: true})
		if m.Notification != nil || m.Data["kind"] != "plan_sync" || m.Android == nil || m.Android.Priority != "high" || m.Android.Notification != nil {
			t.Fatalf("incorrecto: %+v", m)
		}
	})
}

func TestInvalidTokens(t *testing.T) {
	t.Run("dada la respuesta de FCM con un token inválido, un fallo transitorio y un éxito, cuando se interpreta, entonces solo el inválido se descarta", func(t *testing.T) {
		tokens := []string{"bueno", "viejo", "falla-temporal"}
		resp := &messaging.BatchResponse{Responses: []*messaging.SendResponse{
			{Success: true},
			{Success: false, Error: errors.New("unregistered")},
			{Success: false, Error: errors.New("unavailable")},
		}}
		got := invalidTokens(tokens, resp, func(err error) bool { return err.Error() == "unregistered" })
		if len(got) != 1 || got[0] != "viejo" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("dada una respuesta nula o de otro largo, cuando se interpreta, entonces no falla", func(t *testing.T) {
		if got := invalidTokens([]string{"a"}, nil, func(error) bool { return true }); len(got) != 0 {
			t.Errorf("nula: %v", got)
		}
		if got := invalidTokens([]string{"a", "b"}, &messaging.BatchResponse{Responses: []*messaging.SendResponse{{Success: false, Error: errors.New("x")}}}, func(error) bool { return true }); len(got) != 1 {
			t.Errorf("más tokens que respuestas: %v", got)
		}
	})
}
