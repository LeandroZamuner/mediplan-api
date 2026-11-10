package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lea/mediplan-api/internal/usecase/reminders"
)

type ReminderRepo struct{ pool *pgxpool.Pool }

func NewReminderRepo(pool *pgxpool.Pool) *ReminderRepo { return &ReminderRepo{pool: pool} }

// ActivePlansWithCaregivers devuelve los planes activos que tienen al menos un
// cuidador aceptado, con lo necesario para calcular y avisar tomas omitidas.
func (r *ReminderRepo) ActivePlansWithCaregivers(ctx context.Context) ([]reminders.ActivePlan, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT p.id::text, u.email, u.timezone,
		        array_agg(pc.caregiver_id::text ORDER BY pc.created_at, pc.id)
		 FROM treatment_plans p
		 JOIN users u ON u.id = p.patient_id
		 JOIN plan_caregivers pc ON pc.plan_id = p.id AND pc.status = 'accepted'
		 WHERE p.status = 'active'
		 GROUP BY p.id, u.email, u.timezone
		 ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	type row struct {
		planID, email, tz string
		caregivers        []string
	}
	var found []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.planID, &x.email, &x.tz, &x.caregivers); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	plans := NewPlanRepo(r.pool)
	out := make([]reminders.ActivePlan, 0, len(found))
	for _, x := range found {
		plan, err := plans.ByID(ctx, x.planID)
		if err != nil {
			return nil, err
		}
		out = append(out, reminders.ActivePlan{Plan: plan, PatientEmail: x.email, Timezone: x.tz, CaregiverIDs: x.caregivers})
	}
	return out, nil
}
