-- +goose Up
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TYPE user_role AS ENUM ('patient', 'caregiver');
CREATE TYPE link_status AS ENUM ('pending', 'accepted', 'rejected');
CREATE TYPE plan_status AS ENUM ('active', 'completed', 'cancelled');
CREATE TYPE dose_status AS ENUM ('pending', 'taken', 'missed');

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT NOT NULL UNIQUE,
    firebase_uid    TEXT NOT NULL UNIQUE, -- identidad en Firebase Auth
    -- Preferencia de pantalla inicial al registrarse. NO otorga permisos: los permisos salen de plan_caregivers.
    role            user_role NOT NULL DEFAULT 'patient',
    email_verified  BOOLEAN NOT NULL DEFAULT FALSE,
    -- Zona horaria IANA: los horarios de los planes son hora local del paciente.
    timezone        TEXT NOT NULL DEFAULT 'America/Argentina/Buenos_Aires',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE profiles (
    user_id        UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    full_name      TEXT NOT NULL DEFAULT '',
    age            INT,
    language       TEXT NOT NULL DEFAULT 'es',
    email_alerts   BOOLEAN NOT NULL DEFAULT TRUE,
    onboarded      BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE treatment_plans (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    patient_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    status      plan_status NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Cuidadores POR PLAN: ser cuidador es una relación con un plan, no un rol de la cuenta.
-- Mientras la invitación está pendiente solo se conoce el email (caregiver_id NULL).
CREATE TABLE plan_caregivers (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id       UUID NOT NULL REFERENCES treatment_plans(id) ON DELETE CASCADE,
    invite_email  TEXT NOT NULL,
    caregiver_id  UUID REFERENCES users(id) ON DELETE CASCADE,
    status        link_status NOT NULL DEFAULT 'pending',
    token_hash    TEXT NOT NULL UNIQUE, -- SHA-256 del token del enlace; nunca el token
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Un email no puede tener dos vínculos vigentes (pendiente/aceptado) en el mismo plan.
CREATE UNIQUE INDEX plan_caregivers_active_idx ON plan_caregivers (plan_id, invite_email) WHERE status <> 'rejected';
CREATE INDEX plan_caregivers_caregiver_idx ON plan_caregivers (caregiver_id) WHERE status = 'accepted';
CREATE INDEX plan_caregivers_email_idx ON plan_caregivers (invite_email) WHERE status = 'pending';

CREATE TABLE medications (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id           UUID NOT NULL REFERENCES treatment_plans(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    dose              TEXT NOT NULL,
    frequency_hours   INT,
    start_date        DATE NOT NULL DEFAULT CURRENT_DATE,
    end_date          DATE,
    stock_units       INT NOT NULL DEFAULT 0,
    stock_alert_at    INT NOT NULL DEFAULT 5,
    position          INT NOT NULL DEFAULT 0 -- orden dentro del plan
);

-- Recordatorios: día de semana (0-6) y hora
CREATE TABLE schedules (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    medication_id  UUID NOT NULL REFERENCES medications(id) ON DELETE CASCADE,
    day_of_week    SMALLINT NOT NULL CHECK (day_of_week BETWEEN 0 AND 6),
    time_of_day    TIME NOT NULL
);

CREATE TABLE dose_logs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    medication_id  UUID NOT NULL REFERENCES medications(id) ON DELETE CASCADE,
    scheduled_at   TIMESTAMPTZ NOT NULL,
    taken_at       TIMESTAMPTZ,
    status         dose_status NOT NULL DEFAULT 'pending'
);
-- Una sola toma registrada por medicamento y hora programada (hace idempotente el registro).
CREATE UNIQUE INDEX dose_logs_med_time_idx ON dose_logs (medication_id, scheduled_at);

CREATE TABLE devices (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    fcm_token  TEXT NOT NULL UNIQUE,
    platform   TEXT NOT NULL DEFAULT 'android'
);

CREATE TABLE notifications (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL, -- 'missed_dose' | 'low_stock' | 'invite'
    payload     JSONB NOT NULL DEFAULT '{}',
    read_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE contact_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    email       TEXT NOT NULL,
    message     TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS contact_messages, notifications, devices, dose_logs, schedules,
    medications, treatment_plans, plan_caregivers,
    profiles, users CASCADE;
DROP TYPE IF EXISTS dose_status, plan_status, link_status, user_role;
