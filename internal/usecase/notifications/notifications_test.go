package notifications

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

// ---- fakes ----

type fakeDevices struct{ tokenOwner map[string]string } // token -> userID

func (f *fakeDevices) Upsert(_ context.Context, userID, token, _ string) error {
	f.tokenOwner[token] = userID // un token pertenece a un solo usuario: si cambia de dueño, se reasigna
	return nil
}
func (f *fakeDevices) Delete(_ context.Context, userID, token string) error {
	if f.tokenOwner[token] == userID {
		delete(f.tokenOwner, token)
	}
	return nil
}
func (f *fakeDevices) DeleteTokens(_ context.Context, tokens []string) error {
	for _, t := range tokens {
		delete(f.tokenOwner, t)
	}
	return nil
}
func (f *fakeDevices) TokensOf(_ context.Context, userID string) ([]string, error) {
	var out []string
	for tok, u := range f.tokenOwner {
		if u == userID {
			out = append(out, tok)
		}
	}
	return out, nil
}

type fakeNotifs struct {
	rows []*domain.Notification
}

func (f *fakeNotifs) Create(_ context.Context, n *domain.Notification) error {
	n.ID = fmt.Sprintf("n%d", len(f.rows)+1)
	c := *n
	f.rows = append(f.rows, &c)
	return nil
}
func (f *fakeNotifs) ListByUser(_ context.Context, userID string, limit int) ([]*domain.Notification, error) {
	var out []*domain.Notification
	for i := len(f.rows) - 1; i >= 0; i-- { // más nuevas primero
		if f.rows[i].UserID == userID && len(out) < limit {
			c := *f.rows[i]
			out = append(out, &c)
		}
	}
	return out, nil
}
func (f *fakeNotifs) MarkRead(_ context.Context, userID, id string, at time.Time) error {
	for _, r := range f.rows {
		if r.ID == id && r.UserID == userID {
			r.ReadAt = &at
			return nil
		}
	}
	return domain.ErrNotFound
}

type fakePusher struct {
	sent    []sentPush
	invalid []string
	err     error
}
type sentPush struct {
	tokens []string
	msg    PushMessage
}

func (f *fakePusher) Send(_ context.Context, tokens []string, msg PushMessage) ([]string, error) {
	f.sent = append(f.sent, sentPush{tokens, msg})
	return f.invalid, f.err
}

// ---- givens ----

var (
	ana   = &domain.User{ID: "ana"}
	carla = &domain.User{ID: "carla"}
)

type world struct {
	svc     *Service
	devices *fakeDevices
	notifs  *fakeNotifs
	push    *fakePusher
}

func givenWorld() *world {
	w := &world{devices: &fakeDevices{tokenOwner: map[string]string{}}, notifs: &fakeNotifs{}, push: &fakePusher{}}
	w.svc = NewService(w.devices, w.notifs, w.push)
	return w
}

func aviso() domain.Notification {
	return domain.Notification{Kind: domain.NotifMissedDose, Title: "Dosis no registrada", Body: "Ana no registró Enalapril",
		Data: map[string]string{"planId": "p1"}}
}

// ---- Dispositivos ----

func TestDevices(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un celular, cuando se registra su token, entonces recibe los avisos del usuario; registrarlo dos veces no lo duplica", func(t *testing.T) {
		w := givenWorld()
		if err := w.svc.RegisterDevice(ctx, carla, "tok-1", "android"); err != nil {
			t.Fatal(err)
		}
		_ = w.svc.RegisterDevice(ctx, carla, "tok-1", "android")
		if toks, _ := w.devices.TokensOf(ctx, "carla"); len(toks) != 1 {
			t.Fatalf("esperaba 1 token, got %v", toks)
		}
	})

	t.Run("dado un token vacío, cuando se registra, entonces es rechazado", func(t *testing.T) {
		w := givenWorld()
		if err := w.svc.RegisterDevice(ctx, carla, "  ", "android"); !errors.Is(err, domain.ErrInvalidDeviceToken) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("dado un celular que cambió de usuario, cuando el nuevo usuario lo registra, entonces deja de recibir los avisos del anterior", func(t *testing.T) {
		w := givenWorld()
		_ = w.svc.RegisterDevice(ctx, ana, "tok-1", "android")
		_ = w.svc.RegisterDevice(ctx, carla, "tok-1", "android")
		if toks, _ := w.devices.TokensOf(ctx, "ana"); len(toks) != 0 {
			t.Fatalf("Ana no debería conservar el token: %v", toks)
		}
	})

	t.Run("dado un token registrado, cuando el usuario cierra sesión, entonces se da de baja; no se puede dar de baja el de otro", func(t *testing.T) {
		w := givenWorld()
		_ = w.svc.RegisterDevice(ctx, carla, "tok-1", "android")
		_ = w.svc.UnregisterDevice(ctx, ana, "tok-1")
		if toks, _ := w.devices.TokensOf(ctx, "carla"); len(toks) != 1 {
			t.Fatal("Ana no debería poder borrar el token de Carla")
		}
		_ = w.svc.UnregisterDevice(ctx, carla, "tok-1")
		if toks, _ := w.devices.TokensOf(ctx, "carla"); len(toks) != 0 {
			t.Fatalf("debería haberse dado de baja: %v", toks)
		}
	})
}

// ---- Notify ----

func TestNotify(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un usuario con dos celulares, cuando se le envía un aviso, entonces se guarda y se manda a ambos con título, texto y datos", func(t *testing.T) {
		w := givenWorld()
		_ = w.svc.RegisterDevice(ctx, carla, "tok-1", "android")
		_ = w.svc.RegisterDevice(ctx, carla, "tok-2", "android")
		if err := w.svc.Notify(ctx, "carla", aviso()); err != nil {
			t.Fatal(err)
		}
		if len(w.notifs.rows) != 1 || w.notifs.rows[0].UserID != "carla" || w.notifs.rows[0].Kind != domain.NotifMissedDose {
			t.Fatalf("no se guardó bien: %+v", w.notifs.rows)
		}
		if len(w.push.sent) != 1 || len(w.push.sent[0].tokens) != 2 || w.push.sent[0].msg.Title != "Dosis no registrada" ||
			w.push.sent[0].msg.Data["planId"] != "p1" || w.push.sent[0].msg.Data["kind"] != domain.NotifMissedDose {
			t.Fatalf("push incorrecto: %+v", w.push.sent)
		}
	})

	t.Run("dado un usuario sin celulares registrados, cuando se le envía un aviso, entonces igual queda guardado y no se intenta enviar", func(t *testing.T) {
		w := givenWorld()
		if err := w.svc.Notify(ctx, "carla", aviso()); err != nil {
			t.Fatal(err)
		}
		if len(w.notifs.rows) != 1 || len(w.push.sent) != 0 {
			t.Fatalf("guardadas=%d enviados=%d", len(w.notifs.rows), len(w.push.sent))
		}
	})

	t.Run("dado un token que Firebase informa como inválido, cuando se envía, entonces se elimina para no insistir", func(t *testing.T) {
		w := givenWorld()
		_ = w.svc.RegisterDevice(ctx, carla, "tok-viejo", "android")
		_ = w.svc.RegisterDevice(ctx, carla, "tok-bueno", "android")
		w.push.invalid = []string{"tok-viejo"}
		_ = w.svc.Notify(ctx, "carla", aviso())
		toks, _ := w.devices.TokensOf(ctx, "carla")
		if len(toks) != 1 || toks[0] != "tok-bueno" {
			t.Fatalf("quedó %v", toks)
		}
	})

	t.Run("dado un fallo del servicio de push, cuando se envía un aviso, entonces el aviso queda guardado y no se propaga el error", func(t *testing.T) {
		w := givenWorld()
		_ = w.svc.RegisterDevice(ctx, carla, "tok-1", "android")
		w.push.err = errors.New("FCM caído")
		if err := w.svc.Notify(ctx, "carla", aviso()); err != nil {
			t.Fatalf("no debería fallar: %v", err)
		}
		if len(w.notifs.rows) != 1 {
			t.Fatal("el aviso debería haberse guardado")
		}
	})

	t.Run("dado un mensaje de sincronización, cuando se envía, entonces va como push silencioso y NO se guarda como aviso visible", func(t *testing.T) {
		w := givenWorld()
		_ = w.svc.RegisterDevice(ctx, carla, "tok-1", "android")
		if err := w.svc.Sync(ctx, "carla", map[string]string{"planId": "p1"}); err != nil {
			t.Fatal(err)
		}
		if len(w.notifs.rows) != 0 {
			t.Fatal("la sincronización no debe aparecer en la lista de avisos")
		}
		if len(w.push.sent) != 1 || !w.push.sent[0].msg.Silent || w.push.sent[0].msg.Data["kind"] != domain.NotifPlanSync || w.push.sent[0].msg.Title != "" {
			t.Fatalf("push incorrecto: %+v", w.push.sent)
		}
	})
}

// ---- Lista y lectura ----

func TestListAndRead(t *testing.T) {
	ctx := context.Background()

	t.Run("dados avisos de dos usuarios, cuando Carla lista los suyos, entonces ve solo los suyos, los más nuevos primero", func(t *testing.T) {
		w := givenWorld()
		a1, a2 := aviso(), aviso()
		a1.Body, a2.Body = "primero", "segundo"
		_ = w.svc.Notify(ctx, "carla", a1)
		_ = w.svc.Notify(ctx, "ana", aviso())
		_ = w.svc.Notify(ctx, "carla", a2)
		got, err := w.svc.List(ctx, carla)
		if err != nil || len(got) != 2 || got[0].Body != "segundo" || got[1].Body != "primero" {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("dado un aviso, cuando su dueña lo marca como leído, entonces queda leído; otro usuario no puede", func(t *testing.T) {
		w := givenWorld()
		_ = w.svc.Notify(ctx, "carla", aviso())
		id := w.notifs.rows[0].ID
		if err := w.svc.MarkRead(ctx, ana, id); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("ajeno: got %v", err)
		}
		if err := w.svc.MarkRead(ctx, carla, id); err != nil || w.notifs.rows[0].ReadAt == nil {
			t.Errorf("propio: err=%v", err)
		}
	})
}
