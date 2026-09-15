package common

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"travelmcp/internal/store"
)

// AssertQuotaContract — single quota contract for every Store backend:
// the limit holds exactly (including under concurrency), the used counter
// is exact, providers are independent, SetQuotaLimit works, RefundQuota
// returns the budget with a 0 floor. Provider names come from the caller
// (run isolation is their concern: in Postgres the set is closed by an FK
// to providers, so temporary codes are needed).
func AssertQuotaContract(t *testing.T, st store.Store, prov, other string) {
	t.Helper()
	ctx := context.Background()
	const limit = 5

	ok, used, err := st.TryConsumeQuota(ctx, prov, limit)
	if err != nil || !ok || used != 1 {
		t.Fatalf("first consume: ok=%v used=%d err=%v", ok, used, err)
	}
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4*limit; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _, err := st.TryConsumeQuota(ctx, prov, limit); err == nil && ok {
				success.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := success.Load(); got != limit-1 {
		t.Fatalf("concurrent consume: got %d successes, want %d", got, limit-1)
	}
	if ok, _, _ := st.TryConsumeQuota(ctx, prov, limit); ok {
		t.Fatal("exhausted quota must refuse")
	}
	if ok, _, err := st.TryConsumeQuota(ctx, other, limit); err != nil || !ok {
		t.Fatalf("providers must be independent: ok=%v err=%v", ok, err)
	}
	if err := st.SetQuotaLimit(ctx, prov, limit*2); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := st.TryConsumeQuota(ctx, prov, limit*2); err != nil || !ok {
		t.Fatalf("raised limit must allow: ok=%v err=%v", ok, err)
	}
	// Day comes from the store itself (ListQuotas): DB CURRENT_DATE and the
	// local clock may straddle midnight in different timezones.
	day := quotaDay(t, st, prov)
	q, found := st.GetQuota(ctx, prov, day)
	if !found || q.Used != limit+1 || q.Limit != limit*2 {
		t.Fatalf("GetQuota = %+v found=%v, want used=%d limit=%d", q, found, limit+1, limit*2)
	}
	rq, ok := st.(interface {
		RefundQuota(ctx context.Context, provider string) error
	})
	if !ok {
		t.Fatal("store must implement RefundQuota")
	}
	// used is limit+1 here: one refund returns a single unit.
	if err := rq.RefundQuota(ctx, prov); err != nil {
		t.Fatal(err)
	}
	if q, _ := st.GetQuota(ctx, prov, day); q.Used != limit {
		t.Fatalf("after refund used=%d, want %d", q.Used, limit)
	}
	// Drain to the floor: used never goes negative, consume works again.
	for i := 0; i < limit+2; i++ {
		if err := rq.RefundQuota(ctx, prov); err != nil {
			t.Fatal(err)
		}
	}
	if q, _ := st.GetQuota(ctx, prov, day); q.Used != 0 {
		t.Fatalf("after drain used=%d, want 0 floor", q.Used)
	}
	if ok, used, err := st.TryConsumeQuota(ctx, prov, limit); err != nil || !ok || used != 1 {
		t.Fatalf("consume after drain: ok=%v used=%d err=%v", ok, used, err)
	}
}

// quotaDay — the day stamp the store itself uses for prov (via ListQuotas),
// so the contract survives a DB/local midnight straddle across timezones.
func quotaDay(t *testing.T, st store.Store, prov string) time.Time {
	t.Helper()
	list, err := st.ListQuotas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list {
		if r.Provider == prov {
			day, err := time.Parse("2006-01-02", r.Day)
			if err != nil {
				t.Fatalf("bad day %q: %v", r.Day, err)
			}
			return day
		}
	}
	t.Fatalf("ListQuotas misses %s: %+v", prov, list)
	return time.Time{}
}
