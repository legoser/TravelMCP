package geocoder

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"travelmcp/internal/model"
)

// pacerMinInterval — минимальный интервал между вызовами Overpass:
// публичный API раздельный, вежливый темп + глобальный windowed-токен
// в api_quotas; меньше 1с — риск 429.
const pacerMinInterval = 1200 * time.Millisecond

func geoCacheKey(lat, lon float64, radius int) string {
	latR := math.Round(lat*1e4) / 1e4
	lonR := math.Round(lon*1e4) / 1e4
	return fmt.Sprintf("overpass:around:%.4f:%.4f:%d", latR, lonR, radius)
}

func bboxCacheKey(minLat, minLon, maxLat, maxLon float64) string {
	return fmt.Sprintf("overpass:bbox:%.4f:%.4f:%.4f:%.4f",
		math.Round(minLat*1e4)/1e4, math.Round(minLon*1e4)/1e4,
		math.Round(maxLat*1e4)/1e4, math.Round(maxLon*1e4)/1e4)
}

type OverpassProvider interface {
	StationsAround(ctx context.Context, lat, lon float64, radiusM int) ([]model.AdaptedRecord, error)
}

// OverpassBBoxProvider — регио-сбор терминальных станций по bbox
// (O-3 терминалы региона): путь тот же cache+quota, что и Around.
type OverpassBBoxProvider interface {
	StationsInBBox(ctx context.Context, minLat, minLon, maxLat, maxLon float64) ([]model.AdaptedRecord, error)
}

type CachedStationsProvider struct {
	inner      OverpassProvider
	cache      GeoCacheStore
	quota      QuotaFunc
	quotaLimit int
	pacerMu    sync.Mutex
	lastCall   time.Time
}

func NewCachedStationsProvider(inner OverpassProvider, cache GeoCacheStore, quota QuotaFunc, quotaLimit int) *CachedStationsProvider {
	return &CachedStationsProvider{
		inner:      inner,
		cache:      cache,
		quota:      quota,
		quotaLimit: quotaLimit,
	}
}

func (c *CachedStationsProvider) StationsAround(ctx context.Context, lat, lon float64, radiusM int) ([]model.AdaptedRecord, error) {
	key := geoCacheKey(lat, lon, radiusM)
	return c.fetch(ctx, key, func() ([]model.AdaptedRecord, error) {
		return c.inner.StationsAround(ctx, lat, lon, radiusM)
	})
}

// StationsInBBox — регио-сбор станций (issue #12): cache+quota путь для
// bbox-запроса, раньше — голый сетевой вызов в обход квот/кэша. Пейсер
// общий с Around: публичный API один, темп один.
func (c *CachedStationsProvider) StationsInBBox(ctx context.Context, minLat, minLon, maxLat, maxLon float64) ([]model.AdaptedRecord, error) {
	bp, ok := c.inner.(OverpassBBoxProvider)
	if !ok {
		return nil, fmt.Errorf("overpass bbox: провайдер не умеет регио-сбор (нет StationsInBBox)")
	}
	key := bboxCacheKey(minLat, minLon, maxLat, maxLon)
	return c.fetch(ctx, key, func() ([]model.AdaptedRecord, error) {
		return bp.StationsInBBox(ctx, minLat, minLon, maxLat, maxLon)
	})
}

func (c *CachedStationsProvider) fetch(ctx context.Context, key string, call func() ([]model.AdaptedRecord, error)) ([]model.AdaptedRecord, error) {
	if c.cache != nil {
		if e, ok := c.cache.Get(key); ok {
			return e.Records, nil
		}
	}

	if c.quota != nil {
		ok, _, err := c.quota(ctx, "osm", c.quotaLimit)
		if err != nil || !ok {
			return nil, context.DeadlineExceeded
		}
	}

	c.pacerMu.Lock()
	wait := pacerMinInterval - time.Since(c.lastCall)
	if wait > 0 {
		time.Sleep(wait)
	}
	c.lastCall = time.Now()
	c.pacerMu.Unlock()

	records, err := call()
	if err != nil {
		return nil, err
	}

	if c.cache != nil {
		c.cache.Set(key, GeoCacheEntry{Records: records, ObservedAt: time.Now(), Origin: "live"})
	}

	return records, nil
}
