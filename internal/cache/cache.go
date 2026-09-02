package cache

import (
	"context"
	"sync"
	"time"
)

type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

type entry struct {
	val []byte
	exp time.Time
}

type MemoryCache struct {
	mu   sync.RWMutex
	data map[string]entry
}

func NewMemory() *MemoryCache {
	return &MemoryCache{data: map[string]entry{}}
}

func (m *MemoryCache) Get(_ context.Context, key string) ([]byte, bool) {
	m.mu.RLock()
	e, ok := m.data[key]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if !e.exp.IsZero() && time.Now().After(e.exp) {
		m.mu.Lock()
		delete(m.data, key)
		m.mu.Unlock()
		return nil, false
	}
	cp := make([]byte, len(e.val))
	copy(cp, e.val)
	return cp, true
}

func (m *MemoryCache) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(val))
	copy(cp, val)
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	m.data[key] = entry{val: cp, exp: exp}
	return nil
}

func (m *MemoryCache) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.data, key)
	m.mu.Unlock()
	return nil
}
