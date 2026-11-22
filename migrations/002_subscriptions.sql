-- +goose Up
-- Suscripción Premium de cada usuario. Sin fila = plan gratuito.
-- provider/provider_ref identifican la suscripción en el medio de pago (Mercado Pago: id del "preapproval").
CREATE TABLE IF NOT EXISTS subscriptions (
    user_id             UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    tier                TEXT NOT NULL DEFAULT 'free' CHECK (tier IN ('free', 'premium')),
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled')),
    provider            TEXT NOT NULL DEFAULT '',
    provider_ref        TEXT NOT NULL DEFAULT '',
    current_period_end  TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS subscriptions_provider_ref_idx ON subscriptions (provider_ref) WHERE provider_ref <> '';

-- +goose Down
DROP TABLE IF EXISTS subscriptions;
