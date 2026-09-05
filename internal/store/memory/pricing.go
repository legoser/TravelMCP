package memory

import (
	"context"
	"log/slog"
	"sync"

	"travelmcp/internal/pricing"
)

type fareMemory struct {
	mu        sync.RWMutex
	zones     map[string]ZoneRow
	fares     map[string]FareAttributeRow
	rules     []FareRuleRow
	stopZones map[int64]string
}

var globalFareMem = &fareMemory{
	zones:     map[string]ZoneRow{},
	fares:     map[string]FareAttributeRow{},
	stopZones: map[int64]string{},
}

func (m *MemoryStore) UpsertZone(ctx context.Context, z ZoneRow) error {
	globalFareMem.mu.Lock()
	globalFareMem.zones[z.ZoneID] = z
	globalFareMem.mu.Unlock()
	return nil
}

func (m *MemoryStore) UpsertFareAttribute(ctx context.Context, f FareAttributeRow) error {
	f.Currency = pricing.CurrencyOrDefault(f.Currency)
	if err := pricing.ValidateCurrency(f.Currency); err != nil {
		slog.Warn("store/memory: invalid currency, using default", "fare", f.FareID, "currency", f.Currency, "err", err)
		f.Currency = string(pricing.DefaultCurrency())
	}
	if f.Basis == "" {
		f.Basis = "fare"
	}
	globalFareMem.mu.Lock()
	globalFareMem.fares[f.FareID] = f
	globalFareMem.mu.Unlock()
	return nil
}

func (m *MemoryStore) UpsertFareRule(ctx context.Context, r FareRuleRow) error {
	globalFareMem.mu.Lock()
	globalFareMem.rules = append(globalFareMem.rules, r)
	globalFareMem.mu.Unlock()
	return nil
}

func (m *MemoryStore) UpsertStopZone(ctx context.Context, s StopZoneRow) error {
	globalFareMem.mu.Lock()
	globalFareMem.stopZones[s.StopID] = s.ZoneID
	globalFareMem.mu.Unlock()
	return nil
}

func (m *MemoryStore) ListZones(ctx context.Context) ([]ZoneRow, error) {
	globalFareMem.mu.RLock()
	defer globalFareMem.mu.RUnlock()
	out := make([]ZoneRow, 0, len(globalFareMem.zones))
	for _, z := range globalFareMem.zones {
		out = append(out, z)
	}
	return out, nil
}

func (m *MemoryStore) ListFareAttributes(ctx context.Context) ([]FareAttributeRow, error) {
	globalFareMem.mu.RLock()
	defer globalFareMem.mu.RUnlock()
	out := make([]FareAttributeRow, 0, len(globalFareMem.fares))
	for _, f := range globalFareMem.fares {
		out = append(out, f)
	}
	return out, nil
}

func (m *MemoryStore) ListFareRules(ctx context.Context) ([]FareRuleRow, error) {
	globalFareMem.mu.RLock()
	defer globalFareMem.mu.RUnlock()
	out := make([]FareRuleRow, len(globalFareMem.rules))
	copy(out, globalFareMem.rules)
	return out, nil
}
