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

// issue #10: квоты Яндекс Расписания и Яндекс Геокодера — независимые
// строки. Исчерпание rasp-квоты не должно блокировать геокодинг.
func TestYandexQuotaSplit(t *testing.T) {
	st := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		ok, _, _ := st.TryConsumeQuota(ctx, "yandex_rasp", 5)
		if !ok {
			t.Fatalf("yandex_rasp call %d must pass", i)
		}
	}
	if ok, _, _ := st.TryConsumeQuota(ctx, "yandex_rasp", 5); ok {
		t.Fatal("yandex_rasp quota must be exhausted after 5 calls")
	}
	// геокодер жив независимо
	ok, _, _ := st.TryConsumeQuota(ctx, "yandex_geocode", 1000)
	if !ok {
		t.Fatal("yandex_geocode must be unaffected by exhausted yandex_rasp")
	}
	q, found := st.GetQuota(ctx, "yandex_geocode", time.Now())
	if !found || q.Used != 1 {
		t.Fatalf("yandex_geocode used=%d found=%v, want 1/true", q.Used, found)
	}
	q, found = st.GetQuota(ctx, "yandex_rasp", time.Now())
	if !found || q.Used != 5 {
		t.Fatalf("yandex_rasp used=%d found=%v, want 5/true", q.Used, found)
	}
}
