package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lea/mediplan-api/internal/domain"
)

// InviteRepo implementa a la vez caregivers.InviteRepo y plans.CaregiverLinks,
// porque ambos salen de la misma tabla: plan_caregivers.
type InviteRepo struct{ pool *pgxpool.Pool }

func NewInviteRepo(pool *pgxpool.Pool) *InviteRepo { return &InviteRepo{pool: pool} }

const inviteCols = `id::text, plan_id::text, invite_email, COALESCE(caregiver_id::text, ''), status::text, token_hash, expires_at, created_at`

func scanInvite(row pgx.Row) (*domain.PlanCaregiver, error) {
	var c domain.PlanCaregiver
	var status string
	if err := row.Scan(&c.ID, &c.PlanID, &c.InviteEmail, &c.CaregiverID, &status, &c.TokenHash, &c.ExpiresAt, &c.CreatedAt); err != nil {
		return nil, err
	}
	c.Status = domain.InviteStatus(status)
	return &c, nil
}

func (r *InviteRepo) list(ctx context.Context, where string, args ...any) ([]*domain.PlanCaregiver, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+inviteCols+` FROM plan_caregivers `+where+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.PlanCaregiver
	for rows.Next() {
		c, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *InviteRepo) one(ctx context.Context, where string, arg string) (*domain.PlanCaregiver, error) {
	c, err := scanInvite(r.pool.QueryRow(ctx, `SELECT `+inviteCols+` FROM plan_caregivers `+where, arg))
	if err == pgx.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *InviteRepo) Create(ctx context.Context, c *domain.PlanCaregiver) error {
	err := r.pool.QueryRow(ctx,
		`INSERT INTO plan_caregivers (plan_id, invite_email, status, token_hash, expires_at, created_at)
		 VALUES ($1::uuid, $2, $3::link_status, $4, $5, $6) RETURNING id::text`,
		c.PlanID, c.InviteEmail, string(c.Status), c.TokenHash, c.ExpiresAt, c.CreatedAt).Scan(&c.ID)
	if name, dup := uniqueViolation(err); dup && name == "plan_caregivers_active_idx" {
		return domain.ErrAlreadyInvited
	}
	return err
}

func (r *InviteRepo) ByID(ctx context.Context, id string) (*domain.PlanCaregiver, error) {
	if !isUUID(id) {
		return nil, domain.ErrNotFound
	}
	return r.one(ctx, `WHERE id = $1::uuid`, id)
}

func (r *InviteRepo) ByTokenHash(ctx context.Context, hash string) (*domain.PlanCaregiver, error) {
	return r.one(ctx, `WHERE token_hash = $1`, hash)
}

func (r *InviteRepo) ByPlan(ctx context.Context, planID string) ([]*domain.PlanCaregiver, error) {
	if !isUUID(planID) {
		return nil, nil
	}
	return r.list(ctx, `WHERE plan_id = $1::uuid`, planID)
}

func (r *InviteRepo) PendingByEmail(ctx context.Context, email string) ([]*domain.PlanCaregiver, error) {
	return r.list(ctx, `WHERE status = 'pending' AND invite_email = $1`, email)
}

func (r *InviteRepo) Update(ctx context.Context, c *domain.PlanCaregiver) error {
	var caregiver any // NULL mientras no haya cuidador
	if c.CaregiverID != "" {
		caregiver = c.CaregiverID
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE plan_caregivers SET caregiver_id = $2::uuid, status = $3::link_status, expires_at = $4 WHERE id = $1::uuid`,
		c.ID, caregiver, string(c.Status), c.ExpiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *InviteRepo) Delete(ctx context.Context, id string) error {
	if !isUUID(id) {
		return nil
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM plan_caregivers WHERE id = $1::uuid`, id)
	return err
}

// ---- plans.CaregiverLinks ----

func (r *InviteRepo) IsAcceptedCaregiver(ctx context.Context, userID, planID string) (bool, error) {
	if !isUUID(userID) || !isUUID(planID) {
		return false, nil
	}
	var ok bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM plan_caregivers WHERE plan_id = $1::uuid AND caregiver_id = $2::uuid AND status = 'accepted')`,
		planID, userID).Scan(&ok)
	return ok, err
}

func (r *InviteRepo) HasAcceptedCaregiver(ctx context.Context, planID string) (bool, error) {
	if !isUUID(planID) {
		return false, nil
	}
	var ok bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM plan_caregivers WHERE plan_id = $1::uuid AND status = 'accepted')`, planID).Scan(&ok)
	return ok, err
}

func (r *InviteRepo) SupervisedPlanIDs(ctx context.Context, userID string) ([]string, error) {
	if !isUUID(userID) {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT plan_id::text FROM plan_caregivers WHERE caregiver_id = $1::uuid AND status = 'accepted' ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *InviteRepo) AcceptedCaregiverIDs(ctx context.Context, planID string) ([]string, error) {
	if !isUUID(planID) {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT caregiver_id::text FROM plan_caregivers WHERE plan_id = $1::uuid AND status = 'accepted' ORDER BY created_at, id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
