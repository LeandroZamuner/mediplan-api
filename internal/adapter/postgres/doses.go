package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lea/mediplan-api/internal/domain"
)

type DoseRepo struct{ pool *pgxpool.Pool }

func NewDoseRepo(pool *pgxpool.Pool) *DoseRepo { return &DoseRepo{pool: pool} }

const doseCols = `id::text, medication_id::text, scheduled_at, taken_at, status::text`

func scanDose(row pgx.Row) (*domain.DoseLog, error) {
	var l domain.DoseLog
	var taken *time.Time
	var status string
	if err := row.Scan(&l.ID, &l.MedicationID, &l.ScheduledAt, &taken, &status); err != nil {
		return nil, err
	}
	l.TakenAt, l.Status = taken, domain.DoseStatus(status)
	return &l, nil
}

func (r *DoseRepo) Get(ctx context.Context, medicationID string, scheduledAt time.Time) (*domain.DoseLog, error) {
	if !isUUID(medicationID) {
		return nil, domain.ErrNotFound
	}
	l, err := scanDose(r.pool.QueryRow(ctx,
		`SELECT `+doseCols+` FROM dose_logs WHERE medication_id = $1::uuid AND scheduled_at = $2`, medicationID, scheduledAt))
	if err == pgx.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	return l, err
}

// Save inserta o actualiza la toma identificada por (medicamento, hora programada).
func (r *DoseRepo) Save(ctx context.Context, l *domain.DoseLog) error {
	return r.pool.QueryRow(ctx,
		`INSERT INTO dose_logs (medication_id, scheduled_at, taken_at, status)
		 VALUES ($1::uuid, $2, $3, $4::dose_status)
		 ON CONFLICT (medication_id, scheduled_at) DO UPDATE SET taken_at = EXCLUDED.taken_at, status = EXCLUDED.status
		 RETURNING id::text`,
		l.MedicationID, l.ScheduledAt, l.TakenAt, string(l.Status)).Scan(&l.ID)
}

func (r *DoseRepo) Between(ctx context.Context, medicationIDs []string, from, to time.Time) ([]*domain.DoseLog, error) {
	var valid []string
	for _, id := range medicationIDs {
		if isUUID(id) {
			valid = append(valid, id)
		}
	}
	if len(valid) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+doseCols+` FROM dose_logs
		 WHERE medication_id = ANY($1::uuid[]) AND scheduled_at >= $2 AND scheduled_at < $3
		 ORDER BY scheduled_at, id`, valid, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.DoseLog
	for rows.Next() {
		l, err := scanDose(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// MarkMissed registra la toma como omitida SOLO si no tenía registro (atómico: una toma
// que el paciente registró en el mismo instante nunca se pisa). created=false si ya existía.
func (r *DoseRepo) MarkMissed(ctx context.Context, medicationID string, scheduledAt time.Time) (bool, error) {
	if !isUUID(medicationID) {
		return false, domain.ErrNotFound
	}
	tag, err := r.pool.Exec(ctx,
		`INSERT INTO dose_logs (medication_id, scheduled_at, status) VALUES ($1::uuid, $2, 'missed')
		 ON CONFLICT (medication_id, scheduled_at) DO NOTHING`, medicationID, scheduledAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
