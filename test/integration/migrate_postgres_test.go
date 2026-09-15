package integration

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"travelmcp/internal/store/postgres"
)

// Миграция с нуля на живой БД: применяется без ошибок, идемпотентна,
// сиды справочников на месте (регионы, провайдеры, схемы кодов, places).
func TestMigrateFromScratch(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("postgres dsn not configured (TRAVELMCP_TEST_DSN/DATABASE_DSN)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
		t.Fatal(err)
	}
	ps, err := postgres.NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	_, thisFile, _, _ := runtime.Caller(0)
	migDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	if err := ps.MigrateDir(t.Context(), migDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := ps.MigrateDir(t.Context(), migDir); err != nil {
		t.Fatalf("remigrate must be idempotent: %v", err)
	}
	counts := map[string]int{
		"regions": 80, "providers": 10, "identifier_schemes": 13,
		"places": 102, "transport_modes": 10,
	}
	for table, want := range counts {
		var got int
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s: got %d rows, want %d", table, got, want)
		}
	}
	var cancelled bool
	err = pool.QueryRow(context.Background(),
		`SELECT true FROM pg_constraint WHERE conname='jobs_state_check' AND pg_get_constraintdef(oid) LIKE '%cancelled%'`).Scan(&cancelled)
	if err != nil || !cancelled {
		t.Fatalf("jobs CHECK must include cancelled: %v", err)
	}
}
