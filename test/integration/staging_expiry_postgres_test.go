package integration

import (
	"context"
	"testing"
	"time"

	"travelmcp/internal/sync"
)

func TestStagingExpiryNullAttemptPostgres(t *testing.T) {
	pool := mergePool(t)
	ps := mergeStore(t, pool)
	ctx := context.Background()
	old := time.Now().Add(-30 * 24 * time.Hour)
	if _, err := pool.Exec(ctx, `INSERT INTO staging_trips(source, external_route_code, external_trip_code, state, last_attempt_at) VALUES('gov-registry','zzedge-r-null','zzedge-t-null','incomplete_trip',NULL),('gov-registry','zzedge-r-old','zzedge-t-old','needs_review',$1) ON CONFLICT(source, external_route_code, external_trip_code) DO UPDATE SET state=EXCLUDED.state, last_attempt_at=EXCLUDED.last_attempt_at`, old); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM staging_trips WHERE source='gov-registry' AND external_route_code LIKE 'zzedge-%'`)
	})
	res, err := sync.ExpireStaleStagingTrips(ctx, ps, 14*24*time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Expired < 2 {
		t.Fatalf("expired=%d, want >=2 (null-attempt row must age out too)", res.Expired)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM staging_trips WHERE source='gov-registry' AND external_trip_code='zzedge-t-null'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "expired" {
		t.Fatalf("null-attempt row state=%q, want expired", state)
	}
}
