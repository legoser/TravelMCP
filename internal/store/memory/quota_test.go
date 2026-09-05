package memory

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestQuotaRace(t *testing.T) {
	st := NewMemoryStore()
	provider := "yandex"
	limit := 5
	var wg sync.WaitGroup
	success := 0
	var mu sync.Mutex
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, _ := st.TryConsumeQuota(context.Background(), provider, limit)
			if ok {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if success != limit {
		t.Fatalf("expected %d successes, got %d", limit, success)
	}
	q, ok := st.GetQuota(context.Background(), provider, time.Now())
	if !ok {
		t.Fatalf("quota not found")
	}
	if q.Used != limit {
		t.Fatalf("expected used %d, got %d", limit, q.Used)
	}
	ok2, _, _ := st.TryConsumeQuota(context.Background(), provider, limit)
	if ok2 {
		t.Fatalf("expected quota exhausted")
	}
}
