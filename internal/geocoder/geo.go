package geocoder

import (
	"sync"
	"time"

	"travelmcp/internal/model"
)

type GeoCacheEntry struct {
	Records    []model.AdaptedRecord
	ObservedAt time.Time
	Origin     string
}

type GeoCacheStore interface {
	Get(key string) (GeoCacheEntry, bool)
	Set(key string, entry GeoCacheEntry)
}

type MapGeoCacheStore struct {
	mu  sync.RWMutex
	m   map[string]GeoCacheEntry
	ttl time.Duration
}

func NewMapGeoCacheStore(ttl time.Duration) *MapGeoCacheStore {
	return &MapGeoCacheStore{m: map[string]GeoCacheEntry{}, ttl: ttl}
}

func (s *MapGeoCacheStore) Get(key string) (GeoCacheEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[key]
	if !ok {
		return GeoCacheEntry{}, false
	}
	if s.ttl > 0 && time.Since(e.ObservedAt) > s.ttl {
		return GeoCacheEntry{}, false
	}
	return e, true
}

func (s *MapGeoCacheStore) Set(key string, entry GeoCacheEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = entry
}
