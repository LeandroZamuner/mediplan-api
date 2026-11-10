package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DeviceRepo struct{ pool *pgxpool.Pool }

func NewDeviceRepo(pool *pgxpool.Pool) *DeviceRepo { return &DeviceRepo{pool: pool} }

// Upsert asigna el token al usuario. Un token de FCM identifica a UN celular: si otra
// persona inicia sesión en el mismo celular, el token pasa a ser suyo.
func (r *DeviceRepo) Upsert(ctx context.Context, userID, token, platform string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO devices (user_id, fcm_token, platform) VALUES ($1::uuid, $2, $3)
		 ON CONFLICT (fcm_token) DO UPDATE SET user_id = EXCLUDED.user_id, platform = EXCLUDED.platform`,
		userID, token, platform)
	return err
}

func (r *DeviceRepo) Delete(ctx context.Context, userID, token string) error {
	if !isUUID(userID) {
		return nil
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM devices WHERE user_id = $1::uuid AND fcm_token = $2`, userID, token)
	return err
}

func (r *DeviceRepo) DeleteTokens(ctx context.Context, tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM devices WHERE fcm_token = ANY($1)`, tokens)
	return err
}

func (r *DeviceRepo) TokensOf(ctx context.Context, userID string) ([]string, error) {
	if !isUUID(userID) {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT fcm_token FROM devices WHERE user_id = $1::uuid ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
