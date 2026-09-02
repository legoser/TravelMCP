package middleware

import (
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
	"travelmcp/internal/config"
)

type RateLimiter struct {
	mu        sync.Mutex
	limiters  map[string]*rate.Limiter
	def       rate.Limit
	burst     int
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
		limiters:  map[string]*rate.Limiter{},
		def:       rate.Limit(defRPS),
		burst:     defBurst,
		overrides: cfg.RateLimitOverrides,
	}
}

func (rl *RateLimiter) getLimiter(key, path string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	k := key + "|" + path
	if lim, ok := rl.limiters[k]; ok {
		return lim
	}
	lim := rate.NewLimiter(rl.def, rl.burst)
	if ov, ok := rl.overrides[path]; ok {
		rps := ov.RPS
		burst := ov.Burst
		if rps > 0 || burst > 0 {
			if rps <= 0 {
				rps = int(rl.def)
			}
			if burst <= 0 {
				burst = rl.burst
			}
			lim = rate.NewLimiter(rate.Limit(rps), burst)
		}
	}
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
