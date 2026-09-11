package geocoder

import (
	"context"
	"fmt"
	"sync"
	"time"

	"travelmcp/internal/model"
)

// O-5 (план §10): маршрутные relation'ы Overpass идут тем же путём cache+quota,
// что и станции (O-4) — ключи `overpass:route:*`, квота `api_quotas` на коде
// `osm`, общий пейсер ≥1.2с. Нового пути вызова нет (§3.7).

// RouteQueryParams — параметры route-запроса (детерминированный ключ кэша).
// BBox сериализуется в ключ; nil = глобальный запрос. RelationID > 0
// приоритетнее Ref: точечный запрос по уникальному id (issue #11) —
// ключ overpass:route:relation/<id>, не зависящий от ref/bbox.
type RouteQueryParams struct {
	Ref        string
	RelationID int64
	MinLat     float64
	MinLon     float64
	MaxLat     float64
	MaxLon     float64
	HasBBox    bool
}

func (q RouteQueryParams) cacheKey() string {
	if q.RelationID > 0 {
		return fmt.Sprintf("overpass:route:relation/%d", q.RelationID)
	}
	if !q.HasBBox {
		return fmt.Sprintf("overpass:route:%s", q.Ref)
	}
	return fmt.Sprintf("overpass:route:%s:%.4f:%.4f:%.4f:%.4f", q.Ref, q.MinLat, q.MinLon, q.MaxLat, q.MaxLon)
}

type OverpassRoutesProvider interface {
	// FetchRouteRelations вызывается с внешним описанием bbox (реализация
	// в адаптере принимает его структуру; здесь — обезличенный контракт).
	FetchRoutes(ctx context.Context, q RouteQueryParams) ([]model.AdaptedRecord, error)
}

type CachedRoutesProvider struct {
	inner      OverpassRoutesProvider
	cache      GeoCacheStore
	quota      QuotaFunc
	quotaLimit int
	pacerMu    sync.Mutex
	lastCall   time.Time
}

func NewCachedRoutesProvider(inner OverpassRoutesProvider, cache GeoCacheStore, quota QuotaFunc, quotaLimit int) *CachedRoutesProvider {
	return &CachedRoutesProvider{
		inner:      inner,
		cache:      cache,
		quota:      quota,
		quotaLimit: quotaLimit,
	}
}

func (c *CachedRoutesProvider) FetchRoutes(ctx context.Context, q RouteQueryParams) ([]model.AdaptedRecord, error) {
	key := q.cacheKey()

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

	records, err := c.inner.FetchRoutes(ctx, q)
	if err != nil {
		return nil, err
	}

	if c.cache != nil {
		c.cache.Set(key, GeoCacheEntry{Records: records, ObservedAt: time.Now(), Origin: "live"})
	}

	return records, nil
}
