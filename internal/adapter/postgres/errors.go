// Package postgres implementa los repositorios de los casos de uso sobre PostgreSQL (pgx).
package postgres

import (
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5/pgconn"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isUUID evita mandarle a Postgres un id mal formado: para el dominio, un id
// inválido es simplemente "no encontrado".
func isUUID(s string) bool { return uuidRe.MatchString(s) }

// uniqueViolation devuelve el nombre de la restricción si err es una violación de unicidad.
func uniqueViolation(err error) (constraint string, ok bool) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return pg.ConstraintName, true
	}
	return "", false
}
