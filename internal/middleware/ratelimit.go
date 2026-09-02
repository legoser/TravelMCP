package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"

	"travelmcp/internal/config"
)

type bucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
	rps    float64
	burst  float64
}

func newBucket(rps, burst int) *bucket {
	return &bucket{tokens: float64(burst), last: time.Now(), rps: float64(rps), burst: float64(burst)}
}

func (b *bucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * b.rps
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type RateLimiter struct {
	mu        sync.Mutex
	limiters  map[string]*bucket
	defRPS    int
	defBurst  int
	overrides map[string]config.RateLimit
}

func NewRateLimiter(cfg config.HTTP) *RateLimiter {
	defRPS := cfg.RateLimit.RPS
	defBurst := cfg.RateLimit.Burst
	if defRPS <= 0 {
		defRPS = 100
	}
	if defBurst <= 0 {
		defBurst = 200
	}
	return &RateLimiter{
		limiters:  map[string]*bucket{},
		defRPS:    defRPS,
		defBurst:  defBurst,
		overrides: cfg.RateLimitOverrides,
	}
}

func (rl *RateLimiter) getLimiter(key, path string) *bucket {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	k := key + "|" + path
	if lim, ok := rl.limiters[k]; ok {
		return lim
	}
	rps, burst := rl.defRPS, rl.defBurst
	if ov, ok := rl.overrides[path]; ok && (ov.RPS > 0 || ov.Burst > 0) {
		if ov.RPS > 0 {
			rps = ov.RPS
		}
		if ov.Burst > 0 {
			burst = ov.Burst
		}
	}
	lim := newBucket(rps, burst)
	rl.limiters[k] = lim
	return lim
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		ip := clientIP(r)
		lim := rl.getLimiter(ip, r.URL.Path)
		if !lim.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate_limited"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
