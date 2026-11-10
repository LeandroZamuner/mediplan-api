package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lea/mediplan-api/internal/domain"
)

type NotificationRepo struct{ pool *pgxpool.Pool }

func NewNotificationRepo(pool *pgxpool.Pool) *NotificationRepo { return &NotificationRepo{pool: pool} }

// payload es el JSON que se guarda en la columna payload.
type payload struct {
	Title string            `json:"title"`
	Body  string            `json:"body"`
	Data  map[string]string `json:"data"`
}

func (r *NotificationRepo) Create(ctx context.Context, n *domain.Notification) error {
	raw, err := json.Marshal(payload{Title: n.Title, Body: n.Body, Data: n.Data})
	if err != nil {
		return err
	}
	return r.pool.QueryRow(ctx,
		`INSERT INTO notifications (user_id, kind, payload, created_at) VALUES ($1::uuid, $2, $3::jsonb, $4) RETURNING id::text`,
		n.UserID, n.Kind, string(raw), n.CreatedAt).Scan(&n.ID)
}

func (r *NotificationRepo) ListByUser(ctx context.Context, userID string, limit int) ([]*domain.Notification, error) {
	if !isUUID(userID) {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, user_id::text, kind, payload::text, read_at, created_at FROM notifications
		 WHERE user_id = $1::uuid ORDER BY created_at DESC, id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Notification
	for rows.Next() {
		var n domain.Notification
		var raw string
		if err := rows.Scan(&n.ID, &n.UserID, &n.Kind, &raw, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		var p payload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		n.Title, n.Body, n.Data = p.Title, p.Body, p.Data
		out = append(out, &n)
	}
	return out, rows.Err()
}

func (r *NotificationRepo) MarkRead(ctx context.Context, userID, id string, at time.Time) error {
	if !isUUID(userID) || !isUUID(id) {
		return domain.ErrNotFound
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE notifications SET read_at = COALESCE(read_at, $3) WHERE id = $1::uuid AND user_id = $2::uuid`, id, userID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
