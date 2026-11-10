package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lea/mediplan-api/internal/domain"
)

type StockRepo struct{ pool *pgxpool.Pool }

func NewStockRepo(pool *pgxpool.Pool) *StockRepo { return &StockRepo{pool: pool} }

// Consume descuenta n unidades en UNA sentencia atómica (la fila queda bloqueada hasta
// terminar), así varias tomas simultáneas no se pisan y el stock nunca baja de 0.
func (r *StockRepo) Consume(ctx context.Context, medicationID string, n int) (before, after int, err error) {
	if !isUUID(medicationID) {
		return 0, 0, domain.ErrNotFound
	}
	err = r.pool.QueryRow(ctx,
		`WITH old AS (SELECT id, stock_units FROM medications WHERE id = $1::uuid FOR UPDATE)
		 UPDATE medications m SET stock_units = GREATEST(old.stock_units - $2, 0)
		 FROM old WHERE m.id = old.id
		 RETURNING old.stock_units, m.stock_units`, medicationID, n).Scan(&before, &after)
	if err == pgx.ErrNoRows {
		return 0, 0, domain.ErrNotFound
	}
	return before, after, err
}

func (r *StockRepo) Add(ctx context.Context, medicationID string, n int) (int, error) {
	if !isUUID(medicationID) {
		return 0, domain.ErrNotFound
	}
	var total int
	err := r.pool.QueryRow(ctx,
		`UPDATE medications SET stock_units = stock_units + $2 WHERE id = $1::uuid RETURNING stock_units`, medicationID, n).Scan(&total)
	if err == pgx.ErrNoRows {
		return 0, domain.ErrNotFound
	}
	return total, err
}
