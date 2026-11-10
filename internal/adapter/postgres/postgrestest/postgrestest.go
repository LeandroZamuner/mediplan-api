// Package postgrestest prepara una base de datos temporal para los tests de
// integración. Cada ejecución de tests crea su propia base (mediplan_test_<pid>),
// aplica la migración y la borra al terminar: nunca toca la base de desarrollo.
//
// Si no hay Postgres disponible, los tests se saltean (t.Skip) en vez de fallar.
// Conexión de administración: TEST_ADMIN_DATABASE_URL (por defecto el Postgres de docker-compose).
package postgrestest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultAdminURL = "postgres://mediplan:mediplan@localhost:5432/postgres?sslmode=disable"

var (
	once     sync.Once
	pool     *pgxpool.Pool
	admin    *pgxpool.Pool
	dbName   = fmt.Sprintf("mediplan_test_%d", os.Getpid())
	setupErr error
)

// Run se usa desde TestMain: ejecuta los tests y después borra la base temporal.
func Run(m *testing.M) int {
	code := m.Run()
	teardown()
	return code
}

// Setup devuelve un pool sobre una base vacía (con el esquema aplicado).
func Setup(t testing.TB) *pgxpool.Pool {
	t.Helper()
	once.Do(func() { setupErr = initDB() })
	if setupErr != nil {
		t.Skipf("Postgres de pruebas no disponible (¿docker compose up -d db?): %v", setupErr)
	}
	if _, err := pool.Exec(context.Background(), "TRUNCATE users CASCADE"); err != nil {
		t.Fatalf("no se pudo limpiar la base: %v", err)
	}
	return pool
}

func initDB() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	adminURL := os.Getenv("TEST_ADMIN_DATABASE_URL")
	if adminURL == "" {
		adminURL = defaultAdminURL
	}
	var err error
	if admin, err = pgxpool.New(ctx, adminURL); err != nil {
		return err
	}
	if err = admin.Ping(ctx); err != nil {
		return err
	}
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		return err
	}
	cfg, err := pgxpool.ParseConfig(adminURL)
	if err != nil {
		return err
	}
	cfg.ConnConfig.Database = dbName
	if pool, err = pgxpool.NewWithConfig(ctx, cfg); err != nil {
		return err
	}
	return applyMigration(ctx)
}

func applyMigration(ctx context.Context) error {
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "migrations", "001_init.sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	up := string(raw)
	if i := strings.Index(up, "-- +goose Down"); i >= 0 {
		up = up[:i]
	}
	_, err = pool.Exec(ctx, up, pgx.QueryExecModeSimpleProtocol) // varias sentencias a la vez
	return err
}

func teardown() {
	if pool != nil {
		pool.Close()
	}
	if admin != nil {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
		admin.Close()
	}
}
