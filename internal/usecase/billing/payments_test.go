package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

type memStore struct {
	subs map[string]*domain.Subscription
}

func (m *memStore) ByUser(_ context.Context, id string) (*domain.Subscription, error) {
	if s, ok := m.subs[id]; ok {
		c := *s
		return &c, nil
	}
	return nil, domain.ErrNotFound
}
func (m *memStore) ByProviderRef(_ context.Context, _, ref string) (*domain.Subscription, error) {
	for _, s := range m.subs {
		if s.ProviderRef == ref && ref != "" {
			c := *s
			return &c, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (m *memStore) Upsert(_ context.Context, s *domain.Subscription) error {
	c := *s
	m.subs[s.UserID] = &c
	return nil
}

type fakeGW struct {
	subs    map[string]*Preapproval
	created []CreateParams
	cancel  []string
	apPay   map[string]string
	failGet error
}

func (g *fakeGW) Create(_ context.Context, p CreateParams) (*Preapproval, error) {
	g.created = append(g.created, p)
	pa := &Preapproval{ID: "pre-" + string(rune('0'+len(g.created))), Status: "pending", ExternalReference: p.ExternalReference, InitPoint: "https://mp/pagar", AmountARS: float64(p.AmountARS)}
	g.subs[pa.ID] = pa
	return pa, nil
}
func (g *fakeGW) Get(_ context.Context, id string) (*Preapproval, error) {
	if g.failGet != nil {
		return nil, g.failGet
	}
	if pa, ok := g.subs[id]; ok {
		c := *pa
		return &c, nil
	}
	return nil, domain.ErrNotFound
}
func (g *fakeGW) AuthorizedPaymentSubscription(_ context.Context, id string) (string, error) {
	if s, ok := g.apPay[id]; ok {
		return s, nil
	}
	return "", domain.ErrNotFound
}
func (g *fakeGW) Cancel(_ context.Context, id string) error {
	g.cancel = append(g.cancel, id)
	g.subs[id].Status = "cancelled"
	return nil
}

func newPayments(subs ...*domain.Subscription) (*Payments, *memStore, *fakeGW) {
	st := &memStore{subs: map[string]*domain.Subscription{}}
	for _, s := range subs {
		st.subs[s.UserID] = s
	}
	gw := &fakeGW{subs: map[string]*Preapproval{}, apPay: map[string]string{}}
	svc := NewService(st, fakeUsage{})
	svc.now = func() time.Time { return now }
	p := NewPayments(svc, st, gw, PaymentsConfig{PriceARS: 2999, BackURL: "https://web/planes/", NotificationURL: "https://api/hook"})
	p.now = func() time.Time { return now }
	return p, st, gw
}

var ana = &domain.User{ID: "u1", Email: "ana@mail.com"}

func TestCheckout(t *testing.T) {
	ctx := context.Background()
	t.Run("dada una cuenta gratis, cuando pide pagar, entonces recibe el enlace y sigue siendo gratis hasta que se confirme el pago", func(t *testing.T) {
		p, st, gw := newPayments()
		url, err := p.Checkout(ctx, ana, "")
		if err != nil || url != "https://mp/pagar" {
			t.Fatalf("%q %v", url, err)
		}
		c := gw.created[0]
		if c.PayerEmail != "ana@mail.com" || c.ExternalReference != "u1" || c.AmountARS != 2999 || c.BackURL != "https://web/planes/" {
			t.Fatalf("%+v", c)
		}
		if tier, _ := p.svc.TierOf(ctx, "u1"); tier != domain.TierFree {
			t.Fatalf("antes de pagar debe ser gratis, es %s", tier)
		}
		if s := st.subs["u1"]; s.ProviderRef == "" {
			t.Fatalf("debía guardar la suscripción pendiente: %+v", s)
		}
	})
	t.Run("dado otro correo de Mercado Pago, cuando pide pagar, entonces usa ese correo", func(t *testing.T) {
		p, _, gw := newPayments()
		_, _ = p.Checkout(ctx, ana, "ana.mp@mail.com")
		if gw.created[0].PayerEmail != "ana.mp@mail.com" {
			t.Fatalf("%+v", gw.created[0])
		}
	})
	t.Run("dado un pago ya iniciado y sin completar, cuando vuelve a pedir pagar, entonces reutiliza el mismo enlace", func(t *testing.T) {
		p, _, gw := newPayments()
		_, _ = p.Checkout(ctx, ana, "")
		if _, err := p.Checkout(ctx, ana, ""); err != nil || len(gw.created) != 1 {
			t.Fatalf("creó %d suscripciones, err=%v", len(gw.created), err)
		}
	})
	t.Run("dado un pago ya iniciado con otro precio, cuando vuelve a pedir pagar, entonces crea uno nuevo con el precio actual", func(t *testing.T) {
		p, _, gw := newPayments()
		_, _ = p.Checkout(ctx, ana, "")
		p.cfg.PriceARS = 3500
		if _, err := p.Checkout(ctx, ana, ""); err != nil || len(gw.created) != 2 || gw.created[1].AmountARS != 3500 {
			t.Fatalf("%v %+v", err, gw.created)
		}
	})
	t.Run("dada una cuenta Premium vigente, cuando pide pagar, entonces se le avisa que ya es Premium", func(t *testing.T) {
		p, _, gw := newPayments(&domain.Subscription{UserID: "u1", Tier: domain.TierPremium, Status: domain.SubActive, Provider: Provider, ProviderRef: "x", CurrentPeriodEnd: at(10 * 24 * time.Hour)})
		if _, err := p.Checkout(ctx, ana, ""); !errors.Is(err, domain.ErrAlreadyPremium) || len(gw.created) != 0 {
			t.Fatalf("%v", err)
		}
	})
	t.Run("dado que pagó pero el aviso no llegó, cuando vuelve a pedir pagar, entonces se registra Premium y no se cobra de nuevo", func(t *testing.T) {
		p, st, gw := newPayments()
		_, _ = p.Checkout(ctx, ana, "")
		next := now.AddDate(0, 1, 0)
		gw.subs["pre-1"].Status, gw.subs["pre-1"].NextPaymentDate = "authorized", &next
		if _, err := p.Checkout(ctx, ana, ""); !errors.Is(err, domain.ErrAlreadyPremium) || st.subs["u1"].Tier != domain.TierPremium {
			t.Fatalf("%v %+v", err, st.subs["u1"])
		}
	})
}

func TestSyncAndNotifications(t *testing.T) {
	ctx := context.Background()
	next := now.AddDate(0, 1, 0)
	setup := func() (*Payments, *memStore, *fakeGW) {
		p, st, gw := newPayments()
		_, _ = p.Checkout(ctx, ana, "")
		return p, st, gw
	}
	t.Run("dado un pago confirmado, cuando llega el aviso, entonces la cuenta pasa a Premium hasta la próxima fecha de cobro", func(t *testing.T) {
		p, st, gw := setup()
		gw.subs["pre-1"].Status, gw.subs["pre-1"].NextPaymentDate = "authorized", &next
		if err := p.HandleNotification(ctx, "subscription_preapproval", "pre-1"); err != nil {
			t.Fatal(err)
		}
		s := st.subs["u1"]
		if s.Tier != domain.TierPremium || s.Status != domain.SubActive || s.CurrentPeriodEnd == nil || !s.CurrentPeriodEnd.Equal(next) {
			t.Fatalf("%+v", s)
		}
	})
	t.Run("dado un cobro mensual autorizado, cuando llega su aviso, entonces se renueva el período", func(t *testing.T) {
		p, st, gw := setup()
		gw.subs["pre-1"].Status, gw.subs["pre-1"].NextPaymentDate = "authorized", &next
		_ = p.HandleNotification(ctx, "subscription_preapproval", "pre-1")
		later := next.AddDate(0, 1, 0)
		gw.subs["pre-1"].NextPaymentDate = &later
		gw.apPay["cobro-2"] = "pre-1"
		if err := p.HandleNotification(ctx, "subscription_authorized_payment", "cobro-2"); err != nil {
			t.Fatal(err)
		}
		if !st.subs["u1"].CurrentPeriodEnd.Equal(later) {
			t.Fatalf("%+v", st.subs["u1"])
		}
	})
	t.Run("dado un aviso de una suscripción todavía pendiente, cuando llega, entonces sigue siendo gratis", func(t *testing.T) {
		p, st, _ := setup()
		_ = p.HandleNotification(ctx, "subscription_preapproval", "pre-1")
		if st.subs["u1"].Tier != domain.TierFree {
			t.Fatalf("%+v", st.subs["u1"])
		}
	})
	t.Run("dado un aviso de una suscripción que no es nuestra o de otro tema, cuando llega, entonces se ignora sin error", func(t *testing.T) {
		p, _, _ := setup()
		for _, c := range [][2]string{{"subscription_preapproval", "desconocida"}, {"payment", "123"}, {"subscription_preapproval", ""}, {"subscription_authorized_payment", "nada"}} {
			if err := p.HandleNotification(ctx, c[0], c[1]); err != nil {
				t.Fatalf("%v: %v", c, err)
			}
		}
	})
	t.Run("dada una suscripción de otra persona (referencia distinta), cuando llega el aviso, entonces no se da Premium", func(t *testing.T) {
		p, st, gw := setup()
		gw.subs["pre-1"].Status, gw.subs["pre-1"].ExternalReference = "authorized", "otra-persona"
		_ = p.HandleNotification(ctx, "subscription_preapproval", "pre-1")
		if st.subs["u1"].Tier != domain.TierFree {
			t.Fatalf("%+v", st.subs["u1"])
		}
	})
	t.Run("dada una falla pasajera de Mercado Pago, cuando llega el aviso, entonces se devuelve error para que reintente", func(t *testing.T) {
		p, _, gw := setup()
		gw.failGet = domain.ErrPaymentUnavailable
		if err := p.HandleNotification(ctx, "subscription_preapproval", "pre-1"); err == nil {
			t.Fatal("esperaba error")
		}
	})
	t.Run("dado Premium vigente, cuando se cancela la suscripción en Mercado Pago, entonces conserva Premium hasta el fin del período pagado", func(t *testing.T) {
		p, st, gw := setup()
		gw.subs["pre-1"].Status, gw.subs["pre-1"].NextPaymentDate = "authorized", &next
		_ = p.Sync(ctx, "u1")
		gw.subs["pre-1"].Status = "cancelled"
		_ = p.Sync(ctx, "u1")
		s := st.subs["u1"]
		if s.Tier != domain.TierPremium || s.Status != domain.SubCancelled || !s.CurrentPeriodEnd.Equal(next) {
			t.Fatalf("%+v", s)
		}
		if tier, _ := p.svc.TierOf(ctx, "u1"); tier != domain.TierPremium {
			t.Fatal("debe seguir Premium hasta el fin del período")
		}
	})
}

func TestCancelAndRefresh(t *testing.T) {
	ctx := context.Background()
	next := now.AddDate(0, 0, 20)
	t.Run("dado Premium vigente, cuando se da de baja, entonces se cancela en Mercado Pago y queda cancelada con lo pagado vigente", func(t *testing.T) {
		p, st, gw := newPayments(&domain.Subscription{UserID: "u1", Tier: domain.TierPremium, Status: domain.SubActive, Provider: Provider, ProviderRef: "pre-9", CurrentPeriodEnd: &next})
		gw.subs["pre-9"] = &Preapproval{ID: "pre-9", Status: "authorized"}
		if err := p.Cancel(ctx, "u1"); err != nil || len(gw.cancel) != 1 || st.subs["u1"].Status != domain.SubCancelled {
			t.Fatalf("%v %+v", err, st.subs["u1"])
		}
		if tier, _ := p.svc.TierOf(ctx, "u1"); tier != domain.TierPremium {
			t.Fatal("debe seguir Premium hasta el fin del período")
		}
	})
	t.Run("dado un pago pendiente, cuando se da de baja, entonces se descarta el intento", func(t *testing.T) {
		p, st, gw := newPayments()
		_, _ = p.Checkout(ctx, ana, "")
		if err := p.Cancel(ctx, "u1"); err != nil || len(gw.cancel) != 1 || st.subs["u1"].ProviderRef != "" {
			t.Fatalf("%v %+v", err, st.subs["u1"])
		}
	})
	t.Run("dada una cuenta gratis sin pagos, cuando se da de baja, entonces no pasa nada", func(t *testing.T) {
		p, _, gw := newPayments()
		if err := p.Cancel(ctx, "u1"); err != nil || len(gw.cancel) != 0 {
			t.Fatalf("%v", err)
		}
	})
	t.Run("dado un pago pendiente que se confirmó, cuando se pide el estado, entonces se actualiza solo y no consulta más de una vez cada 20 segundos", func(t *testing.T) {
		p, st, gw := newPayments()
		_, _ = p.Checkout(ctx, ana, "")
		n := now.AddDate(0, 1, 0)
		gw.subs["pre-1"].Status, gw.subs["pre-1"].NextPaymentDate = "authorized", &n
		p.RefreshIfNeeded(ctx, "u1")
		if st.subs["u1"].Tier != domain.TierPremium {
			t.Fatalf("%+v", st.subs["u1"])
		}
	})
}
