package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"travelmcp/internal/store/postgres"
	"travelmcp/test/common"
)

// Контракт квот на живой Postgres: атомарность счётчика под конкурентной
// нагрузкой и паритет семантики с memory-реализацией. Временные провайдеры
// (закрытое множество — FK) изолируют прогон, cleanup в конце.
func TestQuotaContractPostgres(t *testing.T) {
	dsn := testDSN()
	if dsn == "" {
		t.Skip("postgres dsn not configured (TRAVELMCP_TEST_DSN/DATABASE_DSN)")
	}
	ps, err := postgres.NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	_, thisFile, _, _ := runtime.Caller(0)
	migDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	if err := ps.MigrateDir(t.Context(), migDir); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	// Pool closes via t.Cleanup registered FIRST (LIFO: runs last), so the
	// data cleanup below still sees an open pool.
	t.Cleanup(func() { pool.Close() })
	suffix := time.Now().UnixNano()
	prov := fmt.Sprintf("qcontract_%d", suffix)
	other := fmt.Sprintf("qcontract_o_%d", suffix)
	for _, p := range []string{prov, other} {
		if _, err := pool.Exec(context.Background(), `INSERT INTO providers(code, name) VALUES($1,'contract test') ON CONFLICT DO NOTHING`, p); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		// api_calls holds an FK to providers: delete history first.
		// Errors are logged, not swallowed: silent cleanup failures leak
		// rows that break later runs on the shared scratch DB.
		for _, q := range []string{
			`DELETE FROM api_calls WHERE provider=$1 OR provider=$2`,
			`DELETE FROM api_quotas WHERE provider=$1 OR provider=$2`,
			`DELETE FROM providers WHERE code=$1 OR code=$2`,
		} {
			if _, err := pool.Exec(context.Background(), q, prov, other); err != nil {
				t.Logf("cleanup %s: %v", q, err)
			}
		}
	})
	common.AssertQuotaContract(t, ps, prov, other)
}
