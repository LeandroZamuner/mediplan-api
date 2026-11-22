package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/reports"
)

type PrefsRepo struct{ pool *pgxpool.Pool }

func NewPrefsRepo(pool *pgxpool.Pool) *PrefsRepo { return &PrefsRepo{pool: pool} }

// Get devuelve las preferencias del usuario; sin fila, todo apagado.
func (r *PrefsRepo) Get(ctx context.Context, userID string) (domain.Preferences, error) {
	var p domain.Preferences
	if !isUUID(userID) {
		return p, nil
	}
	err := r.pool.QueryRow(ctx, `SELECT weekly_report, ai_consent FROM user_prefs WHERE user_id = $1::uuid`, userID).Scan(&p.WeeklyReport, &p.AIConsent)
	if err == pgx.ErrNoRows {
		return domain.Preferences{}, nil
	}
	return p, err
}

func (r *PrefsRepo) Set(ctx context.Context, userID string, p domain.Preferences) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO user_prefs (user_id, weekly_report, ai_consent, updated_at) VALUES ($1::uuid, $2, $3, now())
		 ON CONFLICT (user_id) DO UPDATE SET weekly_report = EXCLUDED.weekly_report, ai_consent = EXCLUDED.ai_consent, updated_at = now()`,
		userID, p.WeeklyReport, p.AIConsent)
	return err
}

// Recipients: quienes pidieron el resumen semanal, tienen Premium en la tabla, correo verificado y todavía no recibieron
// uno en los últimos 6 días. (El nivel vigente exacto lo vuelve a comprobar el servicio.)
func (r *PrefsRepo) Recipients(ctx context.Context, now time.Time) ([]reports.Recipient, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT u.id::text, u.email, u.timezone, p.ai_consent
		   FROM user_prefs p
		   JOIN users u ON u.id = p.user_id
		   JOIN subscriptions s ON s.user_id = u.id AND s.tier = 'premium'
		  WHERE p.weekly_report AND u.email_verified
		    AND (p.last_report_at IS NULL OR p.last_report_at < $1)`, now.Add(-6*24*time.Hour))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reports.Recipient
	for rows.Next() {
		var x reports.Recipient
		if err := rows.Scan(&x.UserID, &x.Email, &x.Timezone, &x.AIConsent); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r *PrefsRepo) MarkSent(ctx context.Context, userID string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE user_prefs SET last_report_at = $2 WHERE user_id = $1::uuid`, userID, at)
	return err
}
