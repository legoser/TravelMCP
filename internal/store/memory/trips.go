package memory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"travelmcp/internal/model"
	store "travelmcp/internal/store"
)

func stagingKey(source, routeCode, tripCode string) string {
	return source + "\x00" + routeCode + "\x00" + tripCode
}

func (m *MemoryStore) FindRouteID(ctx context.Context, source, routeCode string) (int64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, r := range m.routes {
		if r.ProviderID == source && r.ExternalRouteCode == routeCode {
			return id, true
		}
	}
	return 0, false
}

func (m *MemoryStore) TombstoneRoute(ctx context.Context, source, routeCode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	for id, r := range m.routes {
		if r.ProviderID == source && r.ExternalRouteCode == routeCode {
			r.ValidTo = &now
			m.routes[id] = r
		}
	}
	return nil
}

func (m *MemoryStore) FindTrip(ctx context.Context, routeID int64, tripCode string) (store.TripRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.trips {
		if t.RouteID == routeID && t.ExternalTripCode == tripCode {
			return t, true
		}
	}
	return store.TripRow{}, false
}

func (m *MemoryStore) TombstoneTrip(ctx context.Context, tripID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.trips[tripID]
	if !ok {
		return fmt.Errorf("trip %d not found", tripID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	t.ValidTo = &now
	m.trips[tripID] = t
	return nil
}

func (m *MemoryStore) DeleteStopTimes(ctx context.Context, tripID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.stopTimes[:0]
	for _, st := range m.stopTimes {
		if st.TripID != tripID {
			kept = append(kept, st)
		}
	}
	m.stopTimes = kept
	return nil
}

func (m *MemoryStore) EnsureStopForTerminal(ctx context.Context, terminalID int64, lat, lon float64, name string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.terminals[terminalID]; !ok {
		return 0, fmt.Errorf("terminal %d not found", terminalID)
	}
	for id, s := range m.stops {
		if s.TerminalID == terminalID {
			return id, nil
		}
	}
	id := m.allocID()
	m.stops[id] = store.StopRow{ID: id, TerminalID: terminalID, Lat: lat, Lon: lon, Name: name}
	return id, nil
}

func (m *MemoryStore) UpsertTripSource(ctx context.Context, s store.TripSourceRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tripSources == nil {
		m.tripSources = map[int64]map[string]store.TripSourceRow{}
	}
	if m.tripSources[s.TripID] == nil {
		m.tripSources[s.TripID] = map[string]store.TripSourceRow{}
	}
	m.tripSources[s.TripID][s.Source] = s
	return nil
}

func (m *MemoryStore) UpsertStagingTrip(ctx context.Context, s store.StagingTripRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stagingTrips == nil {
		m.stagingTrips = map[string]store.StagingTripRow{}
	}
	key := stagingKey(s.Source, s.ExternalRouteCode, s.ExternalTripCode)
	if prev, ok := m.stagingTrips[key]; ok {
		s.ID = prev.ID
		s.RetryCount = prev.RetryCount + 1
		m.stagingTrips[key] = s
		return s.ID, nil
	}
	s.ID = m.allocID()
	m.stagingTrips[key] = s
	return s.ID, nil
}

func (m *MemoryStore) ListStagingTrips(ctx context.Context, region string, limit int) ([]store.StagingTripRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []store.StagingTripRow
	for _, s := range m.stagingTrips {
		if region != "" && s.Region != region {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ExternalRouteCode != out[j].ExternalRouteCode {
			return out[i].ExternalRouteCode < out[j].ExternalRouteCode
		}
		return out[i].ExternalTripCode < out[j].ExternalTripCode
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) ListCanonTrips(ctx context.Context, source string) (map[string][]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string][]string{}
	for _, t := range m.trips {
		if t.ProviderID != source || t.ValidTo != nil {
			continue
		}
		r, ok := m.routes[t.RouteID]
		if !ok || r.ProviderID != source || r.ValidTo != nil {
			continue
		}
		out[r.ExternalRouteCode] = append(out[r.ExternalRouteCode], r.ExternalRouteCode+"|"+t.ExternalTripCode)
	}
	return out, nil
}

func (m *MemoryStore) AttachTerminalIdentifier(ctx context.Context, terminalID int64, id model.AdaptedIdentifier) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for tid, ids := range m.terminalIdents {
		for _, ex := range ids {
			if ex.System == id.System && ex.Code == id.Code {
				if tid == terminalID {
					return 0, nil
				}
				return tid, nil
			}
		}
	}
	m.terminalIdents[terminalID] = append(m.terminalIdents[terminalID], id)
	return 0, nil
}

func (m *MemoryStore) ListTerminalCodes(ctx context.Context, terminalID int64, system string) ([]model.AdaptedIdentifier, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []model.AdaptedIdentifier
	for _, id := range m.terminalIdents[terminalID] {
		if system == "" || id.System == system {
			out = append(out, id)
		}
	}
	return out, nil
}

func (m *MemoryStore) DeleteStagingTrip(ctx context.Context, source, routeCode, tripCode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.stagingTrips, stagingKey(source, routeCode, tripCode))
	return nil
}

func (m *MemoryStore) PublishOutbox(ctx context.Context, aggregate, aggregateID, event, payload string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if payload == "" {
		payload = "{}"
	}
	m.outbox = append(m.outbox, store.OutboxEvent{
		ID: m.allocID(), Aggregate: aggregate, AggregateID: aggregateID, Event: event, Payload: payload,
	})
	return nil
}

func (m *MemoryStore) ListOutbox(ctx context.Context, limit int) ([]store.OutboxEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := append([]store.OutboxEvent{}, m.outbox...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) DeleteOutbox(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.outbox[:0]
	for _, e := range m.outbox {
		if e.ID != id {
			kept = append(kept, e)
		}
	}
	m.outbox = kept
	return nil
}
