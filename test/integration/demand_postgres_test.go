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

type pgDemandSeeder struct {
	pool *pgxpool.Pool
}

func (s *pgDemandSeeder) SeedDemandTrip(t *testing.T, tag string) (int64, string, string, string) {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d_%s", time.Now().UnixNano(), tag)
	prov, routeCode, extCode := "dm_"+suffix, "r_"+suffix, "t_"+suffix
	if _, err := s.pool.Exec(ctx, `INSERT INTO providers(code, name) VALUES($1,'demand test') ON CONFLICT DO NOTHING`, prov); err != nil {
		t.Fatal(err)
	}
	var routeID int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO routes(source_provider, external_route_code, mode) VALUES($1,$2,'bus') RETURNING id`, prov, routeCode).Scan(&routeID); err != nil {
		t.Fatal(err)
	}
	var tripID int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO trips(route_id, provider_id, external_trip_code) VALUES($1,$2,$3) RETURNING id`, routeID, prov, extCode).Scan(&tripID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []struct {
			sql string
			arg any
		}{
			{`DELETE FROM trip_demand WHERE trip_id=$1`, tripID},
			{`DELETE FROM trip_sources WHERE trip_id=$1`, tripID},
			{`DELETE FROM trips WHERE id=$1`, tripID},
			{`DELETE FROM routes WHERE id=$1`, routeID},
			{`DELETE FROM providers WHERE code=$1`, prov},
		} {
			if _, err := s.pool.Exec(context.Background(), q.sql, q.arg); err != nil {
				t.Logf("cleanup %s: %v", q.sql, err)
			}
		}
	})
	return tripID, prov, routeCode, extCode
}

// Demand slice contract on live Postgres: record ticks accumulate on the
// canonical trip, stale-demand query honors source freshness and ordering.
func TestDemandContractPostgres(t *testing.T) {
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
	// Pool closes via t.Cleanup registered FIRST (LIFO: runs last), so data
	// cleanups registered later still see an open pool. A bare
	// `defer pool.Close()` would close it before cleanups run.
	t.Cleanup(func() { pool.Close() })
	common.AssertDemandContract(t, ps, &pgDemandSeeder{pool: pool})
}
