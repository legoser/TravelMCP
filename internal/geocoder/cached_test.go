package geocoder

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stubGeocoder struct {
	res   *Result
	err   error
	calls int
}

func (s *stubGeocoder) Geocode(ctx context.Context, q string) (*Result, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.res, nil
}

func (s *stubGeocoder) Reverse(ctx context.Context, lat, lon float64) (string, error) {
	return "", errors.New("not implemented")
}

// TTL тестов повторяют дефолты geocode.ttl_verified/ttl_disputed (2160h/168h):
// тесты документируют соответствие кэша конфигу.
const (
	testTTLVerified = 2160 * time.Hour
	testTTLDisputed = 168 * time.Hour
)

func TestCachedHitNoQuota(t *testing.T) {
	inner := &stubGeocoder{res: &Result{Lat: 1, Lon: 2, Name: "X"}}
	cache := NewMapCacheStore()
	quotaCalls := 0
	q := QuotaFunc(func(ctx context.Context, p string, l int) (bool, int, error) {
		quotaCalls++
		return true, 0, nil
	})
	c := NewCachedGeocoder(inner, "nominatim", cache, q, 1000, testTTLVerified, testTTLDisputed)
	if _, err := c.Geocode(context.Background(), "Кемерово"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Geocode(context.Background(), "кемерово "); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 || quotaCalls != 1 {
		t.Fatalf("повтор обязан быть hit без квоты/API: calls=%d quota=%d", inner.calls, quotaCalls)
	}
}

func TestCachedStaleWhileRevalidate(t *testing.T) {
	inner := &stubGeocoder{err: errors.New("down")}
	cache := NewMapCacheStore()
	c := NewCachedGeocoder(inner, "nominatim", cache, nil, 1000, testTTLVerified, testTTLDisputed)
	c.now = func() time.Time { return time.Now().Add(-30 * 24 * time.Hour) }
	cache.Set(NormalizeQuery("Томск"), "nominatim", CacheEntry{Lat: 56, Lon: 85, ObservedAt: c.now(), Origin: "seed"})
	c.now = time.Now
	r, err := c.GeocodeCached(context.Background(), "Томск")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Stale {
		t.Fatalf("протухшая seed-запись при недоступном API обязана вернуться с stale=true")
	}
}

func TestSeedNeverFinalizes(t *testing.T) {
	cache := NewMapCacheStore()
	entries, err := ParseSeedFile("../../data/reestr/nominatim_geo.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Skip("seed-файл пуст, нечего проверять")
	}
	n, err := SeedInto(cache, "nominatim", "../../data/reestr/nominatim_geo.json", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != len(entries) {
		t.Fatalf("seed обязан загрузить все записи: %d != %d", n, len(entries))
	}
	e, ok := cache.Get(entries[0].QueryNorm, "nominatim")
	if !ok || e.Origin != "seed" {
		t.Fatalf("seed-запись обязана нести origin='seed', не голос за finalize: %+v", e)
	}
}

type blockingGeocoder struct {
	calls   atomic.Int32
	release chan struct{}
	res     *Result
}

func (s *blockingGeocoder) Geocode(ctx context.Context, q string) (*Result, error) {
	s.calls.Add(1)
	select {
	case <-s.release:
		return s.res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *blockingGeocoder) Reverse(ctx context.Context, lat, lon float64) (string, error) {
	return "", errors.New("not implemented")
}

func TestCachedConcurrentSingleflight(t *testing.T) {
	inner := &blockingGeocoder{release: make(chan struct{}), res: &Result{Lat: 1, Lon: 2, Name: "X"}}
	c := NewCachedGeocoder(inner, "nominatim", NewMapCacheStore(), nil, 1000, testTTLVerified, testTTLDisputed)
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.Geocode(context.Background(), "Кемерово")
		}(i)
	}
	time.Sleep(100 * time.Millisecond)
	close(inner.release)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("16 параллельных запросов обязаны дать 1 вызов API, получено %d", got)
	}
}

func TestSeedOriginIsSeed(t *testing.T) {
	cache := NewMapCacheStore()
	at := time.Now()
	cache.Set("q", "nominatim", CacheEntry{Lat: 1, Lon: 2, ObservedAt: at, Origin: "seed"})
	e, ok := cache.Get("q", "nominatim")
	if !ok || e.Origin != "seed" {
		t.Fatalf("seed-запись обязана нести origin='seed': %+v", e)
	}
	c := NewCachedGeocoder(&stubGeocoder{res: &Result{}}, "nominatim", cache, nil, 1, testTTLVerified, testTTLDisputed)
	if got := c.ttlFor(e); got != testTTLDisputed {
		t.Fatalf("seed всегда disputed-TTL 7д, получено %v", got)
	}
}
