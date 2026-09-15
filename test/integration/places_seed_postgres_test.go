package integration

import (
	"path/filepath"
	"runtime"
	"testing"

	"travelmcp/internal/geo"
	"travelmcp/internal/store/postgres"
)

// Сид places из миграции обязан давать cold-start газетиру поселения
// без файлов: имена + алиасы автовокзалов резолвятся только из БД.
func TestPlacesSeedColdStart(t *testing.T) {
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
	list, err := ps.ListSettlements(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 100 {
		t.Fatalf("seed обязан дать ~102 поселения, получено %d", len(list))
	}
	gz := geo.EmptyGazetteer()
	for _, s := range list {
		gz.Add(s.Name, s.Lat, s.Lon)
	}
	for _, q := range []string{"Асино", "Автовокзал Асино", "Кемерово"} {
		if _, ok := gz.Resolve(q); !ok {
			t.Fatalf("Resolve(%q) = not found (cold-start без файлов)", q)
		}
	}
}
