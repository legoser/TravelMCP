package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"travelmcp/internal/config"
)

func TestRateLimiterAllowsWithinBurst(t *testing.T) {
	rl := NewRateLimiter(config.HTTP{RateLimit: config.RateLimit{RPS: 100, Burst: 5}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	allowed := 0
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			allowed++
		}
	}
	if allowed == 0 || allowed > 5 {
		t.Fatalf("allowed = %d, want within (0, 5]", allowed)
	}
}

func TestRateLimiterCleanupEvictsIdleBuckets(t *testing.T) {
	rl := NewRateLimiter(config.HTTP{RateLimit: config.RateLimit{RPS: 1, Burst: 10}})
	rl.getLimiter("10.0.0.1", "/api/v1/providers")
	idle := rl.getLimiter("10.0.0.2", "/api/v1/providers")

	idle.mu.Lock()
	idle.last = time.Now().Add(-2 * rl.idleTTL)
	idle.mu.Unlock()

	rl.mu.Lock()
	rl.cleanupLocked(time.Now())
	rl.mu.Unlock()

	rl.mu.Lock()
	_, hasIdle := rl.limiters["10.0.0.2|/api/v1/providers"]
	_, hasActive := rl.limiters["10.0.0.1|/api/v1/providers"]
	size := len(rl.limiters)
	rl.mu.Unlock()

	if hasIdle {
		t.Fatal("idle bucket was not evicted")
	}
	if !hasActive {
		t.Fatal("active bucket must survive cleanup")
	}
	if size != 1 {
		t.Fatalf("limiters = %d, want 1", size)
	}
}

func TestRateLimiterBoundedUnderDistinctIPs(t *testing.T) {
	rl := NewRateLimiter(config.HTTP{RateLimit: config.RateLimit{RPS: 1000, Burst: 1000}})
	now := time.Now()
	rl.mu.Lock()
	for i := 0; i < rl.maxTracked()+50; i++ {
		b := newBucket(1000, 1000)
		b.last = now.Add(-2 * rl.idleTTL)
		rl.limiters[string(rune(i))+"|/scan"] = b
	}
	rl.mu.Unlock()

	rl.getLimiter("10.9.9.9", "/scan")

	rl.mu.Lock()
	size := len(rl.limiters)
	rl.mu.Unlock()
	if size > rl.maxTracked() {
		t.Fatalf("limiters = %d, exceeds bound %d", size, rl.maxTracked())
	}
}

func TestRateLimiterConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(config.HTTP{RateLimit: config.RateLimit{RPS: 1000, Burst: 1000}})
	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
				req.RemoteAddr = "10.0.0." + string(rune('1'+n%200)) + ":1000"
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
			}
		}(i)
	}
	wg.Wait()
}
