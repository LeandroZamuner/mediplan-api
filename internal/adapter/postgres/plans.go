package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lea/mediplan-api/internal/domain"
)

type PlanRepo struct{ pool *pgxpool.Pool }

func NewPlanRepo(pool *pgxpool.Pool) *PlanRepo { return &PlanRepo{pool: pool} }

// querier lo cumplen tanto el pool como una transacción.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (r *PlanRepo) Create(ctx context.Context, p *domain.Plan) error {
	return inTx(ctx, r.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`INSERT INTO treatment_plans (patient_id, name, status, created_at)
			 VALUES ($1::uuid, $2, $3::plan_status, $4) RETURNING id::text`,
			p.PatientID, p.Name, string(p.Status), p.CreatedAt).Scan(&p.ID)
		if err != nil {
			return err
		}
		for i := range p.Medications {
			if err := insertMedication(ctx, tx, p.ID, i, &p.Medications[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// Update reemplaza nombre, estado y medicamentos. Los medicamentos con ID se
// actualizan (conservan su identidad e historial), los sin ID se crean y los
// que ya no están se eliminan. Todo en una transacción.
func (r *PlanRepo) Update(ctx context.Context, p *domain.Plan) error {
	if !isUUID(p.ID) {
		return domain.ErrNotFound
	}
	return inTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE treatment_plans SET name = $2, status = $3::plan_status WHERE id = $1::uuid`,
			p.ID, p.Name, string(p.Status))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		existing, err := medicationIDs(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		keep := map[string]bool{}
		for i := range p.Medications {
			m := &p.Medications[i]
			if m.ID == "" {
				if err := insertMedication(ctx, tx, p.ID, i, m); err != nil {
					return err
				}
			} else {
				if !existing[m.ID] {
					return domain.ErrNotFound
				}
				if err := updateMedication(ctx, tx, i, m); err != nil {
					return err
				}
			}
			keep[m.ID] = true
		}
		for id := range existing {
			if !keep[id] {
				if _, err := tx.Exec(ctx, `DELETE FROM medications WHERE id = $1::uuid`, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// SetMedicationStock fija el stock exacto de un medicamento (ajuste explícito).
func (r *PlanRepo) SetMedicationStock(ctx context.Context, medicationID string, units int) error {
	if !isUUID(medicationID) {
		return domain.ErrNotFound
	}
	tag, err := r.pool.Exec(ctx, `UPDATE medications SET stock_units = $2 WHERE id = $1::uuid`, medicationID, units)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *PlanRepo) Delete(ctx context.Context, id string) error {
	if !isUUID(id) {
		return domain.ErrNotFound
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM treatment_plans WHERE id = $1::uuid`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *PlanRepo) ByID(ctx context.Context, id string) (*domain.Plan, error) {
	if !isUUID(id) {
		return nil, domain.ErrNotFound
	}
	plans, err := r.query(ctx, `WHERE id = $1::uuid`, id)
	if err != nil {
		return nil, err
	}
	if len(plans) == 0 {
		return nil, domain.ErrNotFound
	}
	return plans[0], nil
}

func (r *PlanRepo) ByPatient(ctx context.Context, patientID string) ([]*domain.Plan, error) {
	if !isUUID(patientID) {
		return nil, nil
	}
	return r.query(ctx, `WHERE patient_id = $1::uuid`, patientID)
}

func (r *PlanRepo) query(ctx context.Context, where string, arg string) ([]*domain.Plan, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, patient_id::text, name, status::text, created_at FROM treatment_plans `+where+` ORDER BY created_at, id`, arg)
	if err != nil {
		return nil, err
	}
	var out []*domain.Plan
	for rows.Next() {
		var p domain.Plan
		var status string
		if err := rows.Scan(&p.ID, &p.PatientID, &p.Name, &status, &p.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		p.Status = domain.PlanStatus(status)
		out = append(out, &p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, p := range out {
		if p.Medications, err = loadMedications(ctx, r.pool, p.ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func loadMedications(ctx context.Context, q querier, planID string) ([]domain.Medication, error) {
	rows, err := q.Query(ctx,
		`SELECT id::text, name, dose, start_date, end_date, stock_units, stock_alert_at
		 FROM medications WHERE plan_id = $1::uuid ORDER BY position, id`, planID)
	if err != nil {
		return nil, err
	}
	var meds []domain.Medication
	for rows.Next() {
		var m domain.Medication
		var end *time.Time
		if err := rows.Scan(&m.ID, &m.Name, &m.Dose, &m.StartDate, &end, &m.StockUnits, &m.StockAlertAt); err != nil {
			rows.Close()
			return nil, err
		}
		m.EndDate = end
		meds = append(meds, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range meds {
		if meds[i].Schedules, err = loadSchedules(ctx, q, meds[i].ID); err != nil {
			return nil, err
		}
	}
	return meds, nil
}

func loadSchedules(ctx context.Context, q querier, medID string) ([]domain.Schedule, error) {
	rows, err := q.Query(ctx,
		`SELECT day_of_week, to_char(time_of_day, 'HH24:MI') FROM schedules
		 WHERE medication_id = $1::uuid ORDER BY day_of_week, time_of_day`, medID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Schedule
	for rows.Next() {
		var s domain.Schedule
		if err := rows.Scan(&s.DayOfWeek, &s.TimeOfDay); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func medicationIDs(ctx context.Context, tx pgx.Tx, planID string) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT id::text FROM medications WHERE plan_id = $1::uuid`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

func insertMedication(ctx context.Context, tx pgx.Tx, planID string, pos int, m *domain.Medication) error {
	err := tx.QueryRow(ctx,
		`INSERT INTO medications (plan_id, name, dose, start_date, end_date, stock_units, stock_alert_at, position)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8) RETURNING id::text`,
		planID, m.Name, m.Dose, m.StartDate, m.EndDate, m.StockUnits, m.StockAlertAt, pos).Scan(&m.ID)
	if err != nil {
		return err
	}
	return insertSchedules(ctx, tx, m)
}

func updateMedication(ctx context.Context, tx pgx.Tx, pos int, m *domain.Medication) error {
	if _, err := tx.Exec(ctx,
		// stock_units NO se escribe acá a propósito: lo cambian solo el descuento, la reposición
		// y SetMedicationStock. Así una edición hecha con datos viejos no deshace una toma reciente.
		`UPDATE medications SET name=$2, dose=$3, start_date=$4, end_date=$5, stock_alert_at=$6, position=$7
		 WHERE id = $1::uuid`,
		m.ID, m.Name, m.Dose, m.StartDate, m.EndDate, m.StockAlertAt, pos); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM schedules WHERE medication_id = $1::uuid`, m.ID); err != nil {
		return err
	}
	return insertSchedules(ctx, tx, m)
}

func insertSchedules(ctx context.Context, tx pgx.Tx, m *domain.Medication) error {
	for _, s := range m.Schedules {
		if _, err := tx.Exec(ctx,
			`INSERT INTO schedules (medication_id, day_of_week, time_of_day) VALUES ($1::uuid, $2, $3::time)`,
			m.ID, s.DayOfWeek, s.TimeOfDay); err != nil {
			return err
		}
	}
	return nil
}

func inTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op si ya se hizo commit
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
