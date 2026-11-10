package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lea/mediplan-api/internal/domain"
)

type UserRepo struct{ pool *pgxpool.Pool }

func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

func (r *UserRepo) ByFirebaseUID(ctx context.Context, uid string) (*domain.User, error) {
	var u domain.User
	var role string
	err := r.pool.QueryRow(ctx,
		`SELECT id::text, firebase_uid, email, role::text, email_verified, timezone, created_at FROM users WHERE firebase_uid = $1`, uid).
		Scan(&u.ID, &u.FirebaseUID, &u.Email, &role, &u.EmailVerified, &u.Timezone, &u.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Role = domain.Role(role)
	return &u, nil
}

func (r *UserRepo) Create(ctx context.Context, u *domain.User) error {
	err := r.pool.QueryRow(ctx,
		`INSERT INTO users (firebase_uid, email, role, email_verified, timezone, created_at)
		 VALUES ($1, $2, $3::user_role, $4, COALESCE(NULLIF($5, ''), 'America/Argentina/Buenos_Aires'), $6) RETURNING id::text, timezone`,
		u.FirebaseUID, u.Email, string(u.Role), u.EmailVerified, u.Timezone, u.CreatedAt).Scan(&u.ID, &u.Timezone)
	if _, dup := uniqueViolation(err); dup {
		return domain.ErrAlreadyExists
	}
	return err
}

func (r *UserRepo) SetTimezone(ctx context.Context, userID, tz string) error {
	if !isUUID(userID) {
		return domain.ErrNotFound
	}
	tag, err := r.pool.Exec(ctx, `UPDATE users SET timezone = $2 WHERE id = $1::uuid`, userID, tz)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *UserRepo) TimezoneOf(ctx context.Context, userID string) (string, error) {
	if !isUUID(userID) {
		return "", domain.ErrNotFound
	}
	var tz string
	err := r.pool.QueryRow(ctx, `SELECT timezone FROM users WHERE id = $1::uuid`, userID).Scan(&tz)
	if err == pgx.ErrNoRows {
		return "", domain.ErrNotFound
	}
	return tz, err
}
