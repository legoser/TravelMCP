package geocoder

import (
	"context"
	"sync"
	"testing"
	"time"

	"travelmcp/internal/model"
)

type fakeStationsProvider struct {
	calls int
	mu    sync.Mutex
	resp  []model.AdaptedRecord
	err   error
}

func (f *fakeStationsProvider) StationsAround(ctx context.Context, lat, lon float64, radiusM int) ([]model.AdaptedRecord, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.resp, f.err
}

func (f *fakeStationsProvider) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestGeoCacheKey(t *testing.T) {
	base := geoCacheKey(55.35412345, 86.08765432, 500)
	if base != "overpass:around:55.3541:86.0877:500" {
		t.Errorf("unexpected key: %s", base)
	}

	same := geoCacheKey(55.35414, 86.08774, 500)
	if same != base {
		t.Errorf("keys should be equal for same bucket: %s vs %s", same, base)
	}

	diff := geoCacheKey(55.3542, 86.0878, 500)
	if diff == base {
		t.Errorf("keys should differ for different bucket: %s vs %s", diff, base)
	}
}

func TestMapGeoCacheStore(t *testing.T) {
	s := NewMapGeoCacheStore(time.Hour)
	records := []model.AdaptedRecord{{NameRu: "Тест"}}
	now := time.Now()

	s.Set("key1", GeoCacheEntry{Records: records, ObservedAt: now, Origin: "live"})
	got, ok := s.Get("key1")
	if !ok {
		t.Fatal("want hit")
	}
	if len(got.Records) != 1 || got.Records[0].NameRu != "Тест" {
		t.Errorf("got %+v", got)
	}

	_, ok = s.Get("missing")
	if ok {
		t.Error("want miss for missing key")
	}
}

func TestMapGeoCacheStoreExpires(t *testing.T) {
	// testShortTTL — короткий TTL для проверки протухания без ожидания суток.
	const testShortTTL = 50 * time.Millisecond
	s := NewMapGeoCacheStore(testShortTTL)
	s.Set("key1", GeoCacheEntry{ObservedAt: time.Now().Add(-time.Hour)})

	_, ok := s.Get("key1")
	if ok {
		t.Error("want expired entry to be a miss")
	}
}

func TestCachedStationsProviderCacheHit(t *testing.T) {
	fake := &fakeStationsProvider{resp: []model.AdaptedRecord{{NameRu: "Кеш"}}}
	cache := NewMapGeoCacheStore(time.Hour)
	cached := NewCachedStationsProvider(fake, cache, nil, 0)

	ctx := context.Background()
	_, _ = cached.StationsAround(ctx, 55.0, 86.0, 500)
	_, _ = cached.StationsAround(ctx, 55.0, 86.0, 500)

	if fake.Calls() != 1 {
		t.Errorf("want 1 inner call (second from cache), got %d", fake.Calls())
	}
}

func TestCachedStationsProviderCacheMiss(t *testing.T) {
	fake := &fakeStationsProvider{resp: []model.AdaptedRecord{{NameRu: "Сеть"}}}
	cache := NewMapGeoCacheStore(time.Hour)
	cached := NewCachedStationsProvider(fake, cache, nil, 0)

	ctx := context.Background()
	r1, err := cached.StationsAround(ctx, 55.0, 86.0, 500)
	if err != nil {
		t.Fatalf("want no error, got %v", err)
	}
	if r1[0].NameRu != "Сеть" {
		t.Errorf("got %+v", r1)
	}
	if fake.Calls() != 1 {
		t.Errorf("want 1 call on cache miss, got %d", fake.Calls())
	}
}

func TestCachedStationsProviderQuotaExceeded(t *testing.T) {
	fake := &fakeStationsProvider{resp: []model.AdaptedRecord{{NameRu: "Не должен"}}}
	cache := NewMapGeoCacheStore(time.Hour)
	quota := func(ctx context.Context, provider string, limit int) (bool, int, error) {
		return false, 0, nil
	}
	cached := NewCachedStationsProvider(fake, cache, quota, 100)

	ctx := context.Background()
	_, err := cached.StationsAround(ctx, 55.0, 86.0, 500)
	if err == nil {
		t.Fatal("want error when quota exceeded")
	}
	if fake.Calls() != 0 {
		t.Errorf("want 0 calls when quota exceeded, got %d", fake.Calls())
	}
}

func TestCachedStationsProviderNoQuotaFunc(t *testing.T) {
	fake := &fakeStationsProvider{resp: []model.AdaptedRecord{{NameRu: "OK"}}}
	cache := NewMapGeoCacheStore(time.Hour)
	cached := NewCachedStationsProvider(fake, cache, nil, 0)

	ctx := context.Background()
	_, err := cached.StationsAround(ctx, 55.0, 86.0, 500)
	if err != nil {
		t.Fatalf("want no error with nil quota, got %v", err)
	}
	if fake.Calls() != 1 {
		t.Errorf("want 1 call, got %d", fake.Calls())
	}
}

func TestCachedStationsProviderPacer(t *testing.T) {
	fake := &fakeStationsProvider{resp: []model.AdaptedRecord{{NameRu: "Быстро"}}}
	cache := NewMapGeoCacheStore(time.Hour)
	cached := NewCachedStationsProvider(fake, cache, nil, 0)

	ctx := context.Background()
	start := time.Now()
	_, _ = cached.StationsAround(ctx, 55.0, 86.0, 500)
	_, _ = cached.StationsAround(ctx, 55.1, 86.1, 500)
	elapsed := time.Since(start)

	if elapsed < pacerMinInterval {
		t.Errorf("want at least %v between rapid calls, got %v", pacerMinInterval, elapsed)
	}
}

func TestCachedStationsProviderStoresOnSuccess(t *testing.T) {
	fake := &fakeStationsProvider{resp: []model.AdaptedRecord{{NameRu: "Записать"}}}
	cache := NewMapGeoCacheStore(time.Hour)
	cached := NewCachedStationsProvider(fake, cache, nil, 0)

	ctx := context.Background()
	_, _ = cached.StationsAround(ctx, 55.0, 86.0, 500)

	got, ok := cache.Get(geoCacheKey(55.0, 86.0, 500))
	if !ok {
		t.Fatal("want cache entry after success")
	}
	if len(got.Records) != 1 || got.Records[0].NameRu != "Записать" {
		t.Errorf("cached records mismatch: %+v", got)
	}
	if got.Origin != "live" {
		t.Errorf("origin = %q, want live", got.Origin)
	}
}

// fakeBBoxStations — регио-источник для bbox-пути (issue #12).
type fakeBBoxStations struct {
	fakeStationsProvider
	bboxCalls int
}

func (f *fakeBBoxStations) StationsInBBox(ctx context.Context, minLat, minLon, maxLat, maxLon float64) ([]model.AdaptedRecord, error) {
	f.bboxCalls++
	return f.resp, nil
}

// TestCachedStationsProviderBBoxCacheQuota — bbox-запрос идёт тем же путём
// cache+quota, что и Around (issue #12: раньше — голый сетевой вызов).
func TestCachedStationsProviderBBoxCacheQuota(t *testing.T) {
	fake := &fakeBBoxStations{fakeStationsProvider: fakeStationsProvider{resp: []model.AdaptedRecord{{NameRu: "Горно-Алтайск"}}}}
	cache := NewMapGeoCacheStore(time.Hour)
	calls := 0
	quota := func(ctx context.Context, provider string, limit int) (bool, int, error) {
		calls++
		return true, calls, nil
	}
	cached := NewCachedStationsProvider(fake, cache, quota, 10)

	ctx := context.Background()
	r1, err := cached.StationsInBBox(ctx, 49.0, 83.5, 52.7, 89.5)
	if err != nil {
		t.Fatal(err)
	}
	if len(r1) != 1 || fake.bboxCalls != 1 || calls != 1 {
		t.Fatalf("first: records=%d bbox_calls=%d quota_calls=%d", len(r1), fake.bboxCalls, calls)
	}
	// повтор — кэш-хит без квоты и сети
	if _, err := cached.StationsInBBox(ctx, 49.0, 83.5, 52.7, 89.5); err != nil {
		t.Fatal(err)
	}
	if fake.bboxCalls != 1 || calls != 1 {
		t.Fatalf("cache hit: bbox_calls=%d quota_calls=%d (обязаны остаться 1/1)", fake.bboxCalls, calls)
	}
	// другой bbox — новый ключ
	if _, err := cached.StationsInBBox(ctx, 51.0, 78.5, 54.5, 86.5); err != nil {
		t.Fatal(err)
	}
	if fake.bboxCalls != 2 || calls != 2 {
		t.Fatalf("new bbox: bbox_calls=%d quota_calls=%d", fake.bboxCalls, calls)
	}
}

// TestCachedStationsProviderBBoxQuotaExceeded — вежливый отказ без сети.
func TestCachedStationsProviderBBoxQuotaExceeded(t *testing.T) {
	fake := &fakeBBoxStations{fakeStationsProvider: fakeStationsProvider{resp: nil}}
	quota := func(ctx context.Context, provider string, limit int) (bool, int, error) {
		return false, 0, nil
	}
	cached := NewCachedStationsProvider(fake, NewMapGeoCacheStore(time.Hour), quota, 10)
	if _, err := cached.StationsInBBox(context.Background(), 49, 83, 52, 89); err == nil {
		t.Fatal("квота исчерпана — обязана быть ошибка")
	}
	if fake.bboxCalls != 0 {
		t.Fatalf("сеть не должна зваться, calls=%d", fake.bboxCalls)
	}
}

// TestCachedStationsProviderBBoxNoInner — провайдер без StationsInBBox:
// явная ошибка, не тихий обход.
func TestCachedStationsProviderBBoxNoInner(t *testing.T) {
	cached := NewCachedStationsProvider(&fakeStationsProvider{}, NewMapGeoCacheStore(0), nil, 0)
	if _, err := cached.StationsInBBox(context.Background(), 49, 83, 52, 89); err == nil {
		t.Fatal("провайдер без bbox-контракта обязан давать ошибку")
	}
}
