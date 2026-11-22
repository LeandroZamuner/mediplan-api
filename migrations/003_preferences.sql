-- +goose Up
-- Preferencias de correos e inteligencia artificial. Todo apagado por defecto (hay que aceptarlo a propósito).
-- last_report_at evita mandar dos resúmenes semanales en la misma semana si la tarea corre dos veces.
CREATE TABLE IF NOT EXISTS user_prefs (
    user_id        UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    weekly_report  BOOLEAN NOT NULL DEFAULT false,
    ai_consent     BOOLEAN NOT NULL DEFAULT false,
    last_report_at TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS user_prefs;
