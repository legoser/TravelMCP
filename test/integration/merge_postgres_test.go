package integration

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"travelmcp/internal/store/postgres"
)

func mergePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := testDSN()
	if dsn == "" {
		t.Skip("postgres dsn not configured (TRAVELMCP_TEST_DSN/DATABASE_DSN)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func mergeStore(t *testing.T, pool *pgxpool.Pool) *postgres.PostgresStore {
	t.Helper()
	dsn := testDSN()
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
	return ps
}

func seedTerminal(t *testing.T, pool *pgxpool.Pool, name, code string, locked bool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO terminals(geom, is_locked) VALUES(ST_SetSRID(ST_MakePoint(86.06,55.34),4326)::geography,$1) RETURNING id`, locked).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO terminal_names(terminal_id, lang, name) VALUES($1,'ru',$2)`, id, name); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO terminal_identifiers(terminal_id, system, code_type, code) VALUES($1,'osm','osm_id',$2)`, id, code); err != nil {
		t.Fatal(err)
	}
	return id
}

// MergeTerminals на живой БД: репойнт ссылок, тумстоун old, redirect,
// повторный merge и merge несуществующего — ошибки. Регрессия бага
// terminalState: незалоченный терминал не должен считаться несуществующим.
func TestMergeTerminalsPostgres(t *testing.T) {
	pool := mergePool(t)
	ps := mergeStore(t, pool)
	ctx := context.Background()

	oldID := seedTerminal(t, pool, "Дубль", "osm-1", false)
	newID := seedTerminal(t, pool, "Канон", "osm-2", false)
	if _, err := pool.Exec(ctx, `INSERT INTO terminal_aliases(terminal_id, alias, lang) VALUES($1,'Дубль-алиас','ru')`, oldID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO stops_canonical(terminal_id) VALUES($1)`, oldID); err != nil {
		t.Fatal(err)
	}

	if err := ps.MergeTerminals(ctx, oldID, newID, "test", nil); err != nil {
		t.Fatalf("merge unlocked terminals: %v", err)
	}
	var validTo *string
	if err := pool.QueryRow(ctx, `SELECT valid_to::text FROM terminals WHERE id=$1`, oldID).Scan(&validTo); err != nil || validTo == nil {
		t.Fatalf("old must be tombstoned: %v %v", validTo, err)
	}
	var owner int64
	if err := pool.QueryRow(ctx, `SELECT terminal_id FROM terminal_identifiers WHERE code='osm-1'`).Scan(&owner); err != nil || owner != newID {
		t.Fatalf("identifier must repoint to new: owner=%d err=%v", owner, err)
	}
	if err := pool.QueryRow(ctx, `SELECT terminal_id FROM stops_canonical WHERE terminal_id=$1`, newID).Scan(&owner); err != nil {
		t.Fatalf("stop must repoint to new: %v", err)
	}
	got, err := ps.ResolveTerminalID(ctx, oldID)
	if err != nil || got != newID {
		t.Fatalf("resolve(old) = %d,%v want %d", got, err, newID)
	}
	if err := ps.MergeTerminals(ctx, oldID, newID, "test", nil); err == nil {
		t.Fatal("re-merge of tombstoned old must fail")
	}
	if err := ps.MergeTerminals(ctx, 999999, newID, "test", nil); err == nil {
		t.Fatal("merge of missing old must fail")
	}
	if err := ps.MergeTerminals(ctx, newID, newID, "test", nil); err == nil {
		t.Fatal("self-merge must fail")
	}
}

// DeleteTerminal: тумстоун (не удаление), повтор и missing — ошибки,
// залоченный удаляется оператором безусловно.
func TestDeleteTerminalPostgres(t *testing.T) {
	pool := mergePool(t)
	ps := mergeStore(t, pool)
	ctx := context.Background()

	id := seedTerminal(t, pool, "Ликвид", "osm-9", true)
	if err := ps.DeleteTerminal(ctx, id, nil); err != nil {
		t.Fatalf("delete locked terminal: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM terminals WHERE id=$1 AND valid_to IS NOT NULL`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("terminal must be tombstoned, not removed: %v", err)
	}
	if err := ps.DeleteTerminal(ctx, id, nil); err == nil {
		t.Fatal("double delete must fail")
	}
	if err := ps.DeleteTerminal(ctx, 999999, nil); err == nil {
		t.Fatal("delete of missing must fail")
	}
}

// ResetCanonicalData: чистит канон, каркас (users/providers/places) цел.
func TestResetCanonicalPostgres(t *testing.T) {
	pool := mergePool(t)
	ps := mergeStore(t, pool)
	ctx := context.Background()

	seedTerminal(t, pool, "Сброс", "osm-7", false)
	counts, err := ps.ResetCanonicalData(ctx, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	if counts["terminals"] == 0 {
		t.Fatalf("reset must report deleted terminals: %+v", counts)
	}
	var left int
	for _, table := range []string{"terminals", "terminal_names", "terminal_identifiers", "sync_runs", "review_queue"} {
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&left); err != nil || left != 0 {
			t.Fatalf("%s not cleaned: %d %v", table, left, err)
		}
	}
	for _, table := range []string{"providers", "places", "regions"} {
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&left); err != nil || left == 0 {
			t.Fatalf("%s must survive reset: %d %v", table, left, err)
		}
	}
}
