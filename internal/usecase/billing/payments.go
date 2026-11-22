package billing

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

// Provider identifica al medio de pago en la tabla de suscripciones.
const Provider = "mercadopago"

// Estados de una suscripción en Mercado Pago que nos importan.
const (
	mpAuthorized = "authorized"
	mpPending    = "pending"
	mpCancelled  = "cancelled"
	mpPaused     = "paused"
)

// Preapproval es la suscripción tal como la ve el medio de pago.
type Preapproval struct {
	ID                string
	Status            string
	ExternalReference string // id del usuario en MediPlan
	InitPoint         string // enlace donde la persona paga
	AmountARS         float64
	NextPaymentDate   *time.Time
}

type CreateParams struct {
	Reason            string
	ExternalReference string
	PayerEmail        string
	AmountARS         int
	BackURL           string
	NotificationURL   string
}

// Gateway es el puerto hacia Mercado Pago.
type Gateway interface {
	Create(ctx context.Context, p CreateParams) (*Preapproval, error)
	Get(ctx context.Context, id string) (*Preapproval, error) // domain.ErrNotFound si no existe
	// AuthorizedPaymentSubscription devuelve el id de la suscripción a la que pertenece un cobro autorizado.
	AuthorizedPaymentSubscription(ctx context.Context, paymentID string) (string, error)
	Cancel(ctx context.Context, id string) error
}

type Store interface {
	ByUser(ctx context.Context, userID string) (*domain.Subscription, error)
	ByProviderRef(ctx context.Context, provider, ref string) (*domain.Subscription, error)
	Upsert(ctx context.Context, s *domain.Subscription) error
}

type PaymentsConfig struct {
	PriceARS        int
	BackURL         string // adónde vuelve la persona después de pagar (https)
	NotificationURL string // dónde avisa Mercado Pago de cada cambio (opcional)
}

// Payments cobra la suscripción Premium con Mercado Pago. Premium se activa SOLO cuando Mercado Pago confirma
// la suscripción como autorizada: la API nunca confía en lo que dice el aviso, siempre lo consulta de nuevo.
type Payments struct {
	svc   *Service
	store Store
	gw    Gateway
	cfg   PaymentsConfig
	now   func() time.Time

	mu       sync.Mutex
	lastSync map[string]time.Time
}

func NewPayments(svc *Service, store Store, gw Gateway, cfg PaymentsConfig) *Payments {
	return &Payments{svc: svc, store: store, gw: gw, cfg: cfg, now: time.Now, lastSync: map[string]time.Time{}}
}

func (p *Payments) PriceARS() int { return p.cfg.PriceARS }

// Checkout prepara el pago: crea la suscripción pendiente en Mercado Pago y devuelve el enlace para pagar.
// payerEmail es el correo de la cuenta de Mercado Pago de quien paga (si viene vacío, el de MediPlan).
func (p *Payments) Checkout(ctx context.Context, user *domain.User, payerEmail string) (string, error) {
	if payerEmail == "" {
		payerEmail = user.Email
	}
	sub, err := p.store.ByUser(ctx, user.ID)
	if err != nil && err != domain.ErrNotFound {
		return "", err
	}
	if sub != nil {
		if p.svc.effective(sub) == domain.TierPremium {
			return "", domain.ErrAlreadyPremium
		}
		// Si ya había empezado a suscribirse: se reutiliza el enlace mientras siga pendiente (no se acumulan intentos).
		if sub.Provider == Provider && sub.ProviderRef != "" {
			if pa, err := p.gw.Get(ctx, sub.ProviderRef); err == nil {
				if pa.Status == mpAuthorized { // pagó y todavía no lo habíamos registrado
					if err := p.apply(ctx, sub, pa); err != nil {
						return "", err
					}
					return "", domain.ErrAlreadyPremium
				}
				if pa.Status == mpPending && pa.InitPoint != "" && int(pa.AmountARS) == p.cfg.PriceARS {
					return pa.InitPoint, nil
				}
			}
		}
	}
	pa, err := p.gw.Create(ctx, CreateParams{
		Reason:            "MediPlan Premium",
		ExternalReference: user.ID,
		PayerEmail:        payerEmail,
		AmountARS:         p.cfg.PriceARS,
		BackURL:           p.cfg.BackURL,
		NotificationURL:   p.cfg.NotificationURL,
	})
	if err != nil {
		return "", err
	}
	if pa.InitPoint == "" {
		return "", domain.ErrPaymentUnavailable
	}
	// Queda guardada como pendiente: sigue siendo gratis hasta que el pago se confirme.
	if err := p.store.Upsert(ctx, &domain.Subscription{UserID: user.ID, Tier: domain.TierFree, Status: domain.SubActive, Provider: Provider, ProviderRef: pa.ID}); err != nil {
		return "", err
	}
	return pa.InitPoint, nil
}

// Sync consulta a Mercado Pago el estado de la suscripción del usuario y lo refleja (al volver de pagar).
func (p *Payments) Sync(ctx context.Context, userID string) error {
	sub, err := p.store.ByUser(ctx, userID)
	if err == domain.ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if sub.Provider != Provider || sub.ProviderRef == "" {
		return nil
	}
	pa, err := p.gw.Get(ctx, sub.ProviderRef)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.lastSync[userID] = p.now()
	p.mu.Unlock()
	return p.apply(ctx, sub, pa)
}

// RefreshIfNeeded actualiza desde Mercado Pago cuando hay algo pendiente de confirmar o el período está por vencer
// (por si el aviso no llegó). Es silencioso y limitado a una consulta cada 20 segundos por usuario.
func (p *Payments) RefreshIfNeeded(ctx context.Context, userID string) {
	sub, err := p.store.ByUser(ctx, userID)
	if err != nil || sub.Provider != Provider || sub.ProviderRef == "" {
		return
	}
	need := sub.Tier != domain.TierPremium // pendiente de pago
	if sub.Tier == domain.TierPremium && sub.Status == domain.SubActive {
		need = sub.CurrentPeriodEnd == nil || p.now().After(sub.CurrentPeriodEnd.Add(-24*time.Hour))
	}
	if !need {
		return
	}
	p.mu.Lock()
	recent := p.now().Sub(p.lastSync[userID]) < 20*time.Second
	p.mu.Unlock()
	if recent {
		return
	}
	if err := p.Sync(ctx, userID); err != nil {
		log.Printf("pagos: no se pudo actualizar la suscripción de %s: %v", userID, err)
	}
}

// Cancel da de baja la renovación: sigue siendo Premium hasta el fin del período ya pagado.
func (p *Payments) Cancel(ctx context.Context, userID string) error {
	sub, err := p.store.ByUser(ctx, userID)
	if err == domain.ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if sub.Provider != Provider || sub.ProviderRef == "" {
		return nil
	}
	if err := p.gw.Cancel(ctx, sub.ProviderRef); err != nil {
		return err
	}
	if sub.Tier == domain.TierPremium {
		sub.Status = domain.SubCancelled
	} else {
		sub.ProviderRef = "" // solo estaba pendiente: se descarta
	}
	return p.store.Upsert(ctx, sub)
}

// HandleNotification procesa un aviso de Mercado Pago. Lo que dice el aviso no se usa: solo sirve para saber qué
// consultar. Devuelve error solo en fallas pasajeras (para que Mercado Pago reintente).
func (p *Payments) HandleNotification(ctx context.Context, topic, id string) error {
	if id == "" {
		return nil
	}
	subID := ""
	switch topic {
	case "subscription_preapproval":
		subID = id
	case "subscription_authorized_payment":
		var err error
		if subID, err = p.gw.AuthorizedPaymentSubscription(ctx, id); err != nil {
			if err == domain.ErrNotFound {
				return nil
			}
			return err
		}
	default:
		return nil // otros temas no cambian el nivel
	}
	pa, err := p.gw.Get(ctx, subID)
	if err == domain.ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	sub, err := p.store.ByProviderRef(ctx, Provider, pa.ID)
	if err == domain.ErrNotFound {
		return nil // no es una suscripción nuestra
	}
	if err != nil {
		return err
	}
	if pa.ExternalReference != sub.UserID {
		return nil // no coincide con la persona: se ignora
	}
	return p.apply(ctx, sub, pa)
}

// apply traduce el estado de Mercado Pago al nivel de la cuenta.
func (p *Payments) apply(ctx context.Context, sub *domain.Subscription, pa *Preapproval) error {
	before := *sub
	switch pa.Status {
	case mpAuthorized:
		sub.Tier, sub.Status = domain.TierPremium, domain.SubActive
		switch {
		case pa.NextPaymentDate != nil:
			sub.CurrentPeriodEnd = pa.NextPaymentDate
		case sub.CurrentPeriodEnd == nil:
			end := p.now().AddDate(0, 1, 0)
			sub.CurrentPeriodEnd = &end
		}
	case mpCancelled, mpPaused:
		if sub.Tier == domain.TierPremium {
			sub.Status = domain.SubCancelled // conserva lo ya pagado hasta CurrentPeriodEnd
		}
	}
	if sub.Tier == before.Tier && sub.Status == before.Status && equalTime(sub.CurrentPeriodEnd, before.CurrentPeriodEnd) {
		return nil
	}
	return p.store.Upsert(ctx, sub)
}

func equalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
