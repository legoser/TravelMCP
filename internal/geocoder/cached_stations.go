package geocoder

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"travelmcp/internal/model"
)

const pacerMinInterval = 1200 * time.Millisecond

func geoCacheKey(lat, lon float64, radius int) string {
	latR := math.Round(lat*1e4) / 1e4
	lonR := math.Round(lon*1e4) / 1e4
	return fmt.Sprintf("overpass:around:%.4f:%.4f:%d", latR, lonR, radius)
}

type OverpassProvider interface {
	StationsAround(ctx context.Context, lat, lon float64, radiusM int) ([]model.AdaptedRecord, error)
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

	records, err := c.inner.StationsAround(ctx, lat, lon, radiusM)
	if err != nil {
		return nil, err
	}

	if c.cache != nil {
		c.cache.Set(key, GeoCacheEntry{Records: records, ObservedAt: time.Now(), Origin: "live"})
	}

	return records, nil
}
