package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lea/mediplan-api/internal/domain"
)

type SubscriptionRepo struct{ pool *pgxpool.Pool }

func NewSubscriptionRepo(pool *pgxpool.Pool) *SubscriptionRepo { return &SubscriptionRepo{pool: pool} }

const subscriptionCols = `user_id::text, tier, status, provider, provider_ref, current_period_end, updated_at`

func scanSubscription(row pgx.Row) (*domain.Subscription, error) {
	var s domain.Subscription
	var tier, status string
	if err := row.Scan(&s.UserID, &tier, &status, &s.Provider, &s.ProviderRef, &s.CurrentPeriodEnd, &s.UpdatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	s.Tier, s.Status = domain.Tier(tier), domain.SubStatus(status)
	return &s, nil
}

// ByUser devuelve la suscripción del usuario. ErrNotFound si nunca tuvo (plan gratuito).
func (r *SubscriptionRepo) ByUser(ctx context.Context, userID string) (*domain.Subscription, error) {
	if !isUUID(userID) {
		return nil, domain.ErrNotFound
	}
	return scanSubscription(r.pool.QueryRow(ctx, `SELECT `+subscriptionCols+` FROM subscriptions WHERE user_id = $1::uuid`, userID))
}

// ByProviderRef busca por el id de la suscripción en el medio de pago (lo usan los avisos del pago). ErrNotFound.
func (r *SubscriptionRepo) ByProviderRef(ctx context.Context, provider, ref string) (*domain.Subscription, error) {
	return scanSubscription(r.pool.QueryRow(ctx,
		`SELECT `+subscriptionCols+` FROM subscriptions WHERE provider = $1 AND provider_ref = $2 AND provider_ref <> ''`, provider, ref))
}

// Upsert crea o reemplaza la suscripción del usuario.
func (r *SubscriptionRepo) Upsert(ctx context.Context, s *domain.Subscription) error {
	return r.pool.QueryRow(ctx,
		`INSERT INTO subscriptions (user_id, tier, status, provider, provider_ref, current_period_end, updated_at)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, now())
		 ON CONFLICT (user_id) DO UPDATE SET tier = EXCLUDED.tier, status = EXCLUDED.status, provider = EXCLUDED.provider,
		   provider_ref = EXCLUDED.provider_ref, current_period_end = EXCLUDED.current_period_end, updated_at = now()
		 RETURNING updated_at`,
		s.UserID, string(s.Tier), string(s.Status), s.Provider, s.ProviderRef, s.CurrentPeriodEnd).Scan(&s.UpdatedAt)
}
