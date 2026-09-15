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
	defer pool.Close()
	suffix := time.Now().UnixNano()
	prov := fmt.Sprintf("qcontract_%d", suffix)
	other := fmt.Sprintf("qcontract_o_%d", suffix)
	for _, p := range []string{prov, other} {
		if _, err := pool.Exec(context.Background(), `INSERT INTO providers(code, name) VALUES($1,'contract test') ON CONFLICT DO NOTHING`, p); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM api_quotas WHERE provider=$1 OR provider=$2`, prov, other)
		_, _ = pool.Exec(context.Background(), `DELETE FROM providers WHERE code=$1 OR code=$2`, prov, other)
	})
	common.AssertQuotaContract(t, ps, prov, other)
}
