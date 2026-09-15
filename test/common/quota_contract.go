package common

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"travelmcp/internal/store"
)

// AssertQuotaContract — единый контракт квот для всех реализаций Store:
// лимит соблюдается точно (в т.ч. под конкурентной нагрузкой), счётчик
// used точен, провайдеры независимы, SetQuotaLimit работает.
// Имена провайдеров задаёт вызывающий (изоляция прогонов — его забота:
// в Postgres множество закрыто FK на providers, нужны временные коды).
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
	q, found := st.GetQuota(ctx, prov, time.Now())
	if !found || q.Used != limit+1 || q.Limit != limit*2 {
		t.Fatalf("GetQuota = %+v found=%v, want used=%d limit=%d", q, found, limit+1, limit*2)
	}
	list, err := st.ListQuotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, r := range list {
		if r.Provider == prov {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("ListQuotas misses %s: %+v", prov, list)
	}
}
