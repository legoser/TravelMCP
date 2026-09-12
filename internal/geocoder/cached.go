package geocoder

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type CacheEntry struct {
	Lat        float64
	Lon        float64
	Name       string
	ObservedAt time.Time
	Origin     string
	Verified   bool
}

type CacheStore interface {
	Get(queryNorm, provider string) (CacheEntry, bool)
	Set(queryNorm, provider string, e CacheEntry)
}

type QuotaFunc func(ctx context.Context, provider string, limit int) (bool, int, error)

type CachedResult struct {
	Result
	Stale  bool
	Origin string
}

type CachedGeocoder struct {
	inner       Geocoder
	provider    string
	cache       CacheStore
	quota       QuotaFunc
	quotaLimit  int
	ttlVerified time.Duration
	ttlDisputed time.Duration
	now         func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func NewCachedGeocoder(inner Geocoder, provider string, cache CacheStore, quota QuotaFunc, quotaLimit int, ttlVerified, ttlDisputed time.Duration) *CachedGeocoder {
	return &CachedGeocoder{
		inner: inner, provider: provider, cache: cache,
		quota: quota, quotaLimit: quotaLimit,
		ttlVerified: ttlVerified, ttlDisputed: ttlDisputed,
		now: time.Now, locks: map[string]*sync.Mutex{},
	}
}

func NormalizeQuery(q string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(q))), " ")
}

func (c *CachedGeocoder) ttlFor(e CacheEntry) time.Duration {
	if e.Origin == "seed" || !e.Verified {
		return c.ttlDisputed
	}
	return c.ttlVerified
}

func (c *CachedGeocoder) keyLock(key string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.locks[key]; ok {
		return m
	}
	m := &sync.Mutex{}
	c.locks[key] = m
	return m
}

func (c *CachedGeocoder) GeocodeCached(ctx context.Context, query string) (*CachedResult, error) {
	norm := NormalizeQuery(query)
	if e, ok := c.cache.Get(norm, c.provider); ok {
		if c.now().Sub(e.ObservedAt) < c.ttlFor(e) {
			return &CachedResult{Result: Result{Lat: e.Lat, Lon: e.Lon, Name: e.Name}, Origin: e.Origin}, nil
		}
		if stale, err := c.revalidate(ctx, norm, query); err == nil {
			return stale, nil
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return &CachedResult{Result: Result{Lat: e.Lat, Lon: e.Lon, Name: e.Name}, Stale: true, Origin: e.Origin}, nil
	}
	return c.revalidate(ctx, norm, query)
}

func (c *CachedGeocoder) revalidate(ctx context.Context, norm, query string) (*CachedResult, error) {
	l := c.keyLock(norm + "|" + c.provider)
	l.Lock()
	defer l.Unlock()
	if e, ok := c.cache.Get(norm, c.provider); ok {
		if c.now().Sub(e.ObservedAt) < c.ttlFor(e) {
			return &CachedResult{Result: Result{Lat: e.Lat, Lon: e.Lon, Name: e.Name}, Origin: e.Origin}, nil
		}
	}
	if c.quota != nil {
		ok, used, err := c.quota(ctx, c.provider, c.quotaLimit)
		if err != nil || !ok {
			if e, found := c.cache.Get(norm, c.provider); found {
				return &CachedResult{Result: Result{Lat: e.Lat, Lon: e.Lon, Name: e.Name}, Stale: true, Origin: e.Origin}, nil
			}
			if err != nil {
				return nil, fmt.Errorf("geocode %s: quota: %w", c.provider, err)
			}
			return nil, fmt.Errorf("geocode %s: 429 quota exhausted (limit %d, used %d)", c.provider, c.quotaLimit, used)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res, err := c.inner.Geocode(ctx, query)
	if err != nil {
		if e, found := c.cache.Get(norm, c.provider); found {
			return &CachedResult{Result: Result{Lat: e.Lat, Lon: e.Lon, Name: e.Name}, Stale: true, Origin: e.Origin}, nil
		}
		return nil, err
	}
	c.cache.Set(norm, c.provider, CacheEntry{Lat: res.Lat, Lon: res.Lon, Name: res.Name, ObservedAt: c.now(), Origin: "live"})
	return &CachedResult{Result: *res, Origin: "live"}, nil
}

func (c *CachedGeocoder) Geocode(ctx context.Context, query string) (*Result, error) {
	r, err := c.GeocodeCached(ctx, query)
	if err != nil {
		return nil, err
	}
	out := r.Result
	return &out, nil
}

func (c *CachedGeocoder) Reverse(ctx context.Context, lat, lon float64) (string, error) {
	return c.inner.Reverse(ctx, lat, lon)
}

type MapCacheStore struct {
	mu sync.RWMutex
	m  map[string]CacheEntry
}

func NewMapCacheStore() *MapCacheStore { return &MapCacheStore{m: map[string]CacheEntry{}} }

func (s *MapCacheStore) Get(queryNorm, provider string) (CacheEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[queryNorm+"|"+provider]
	return e, ok
}

func (s *MapCacheStore) Set(queryNorm, provider string, e CacheEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[queryNorm+"|"+provider] = e
}
