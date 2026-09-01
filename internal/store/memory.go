package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"travelmcp/internal/model"
)

type MemoryStore struct {
	mu           sync.RWMutex
	stations     map[int64]StationRow
	stops        map[int64]StopRow
	stationCodes []StationCodeRow
	carriers     map[int64]CarrierRow
	routes       map[int64]RouteRow
	trips        map[int64]TripRow
	frequencies  []FrequencyRow
	stopTimes    []StopTimeRow
	transfers    []TransferRow
	quality      []QualityRow
	imports      map[string]time.Time
	nextID       int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		stations: make(map[int64]StationRow),
		stops:    make(map[int64]StopRow),
		carriers: make(map[int64]CarrierRow),
		routes:   make(map[int64]RouteRow),
		trips:    make(map[int64]TripRow),
		imports:  make(map[string]time.Time),
		nextID:   1,
	}
}

func (m *MemoryStore) allocID() int64 { id := m.nextID; m.nextID++; return id }

func (m *MemoryStore) Migrate(ctx context.Context) error { return nil }
func (m *MemoryStore) Close() error                      { return nil }
func (m *MemoryStore) WithTx(ctx context.Context, fn func(Store) error) error { return fn(m) }
func (m *MemoryStore) FindStation(ctx context.Context, name, region string) (StationRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.stations {
		if s.Name == name && s.RegionCode == region && s.Lat != 0 {
			return s, true
		}
	}
	return StationRow{}, false
}
func (m *MemoryStore) MarkImported(ctx context.Context, providerID string, at time.Time, records int) error {
	m.mu.Lock()
	m.imports[providerID] = at
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) SaveQualityIssue(ctx context.Context, q QualityRow) error {
	m.mu.Lock()
	m.quality = append(m.quality, q)
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) UpsertFare(ctx context.Context, f FareRow) error { return nil }

func (m *MemoryStore) UpsertStation(ctx context.Context, s StationRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.ID == 0 {
		s.ID = m.allocID()
	}
	m.stations[s.ID] = s
	return s.ID, nil
}
func (m *MemoryStore) UpsertStop(ctx context.Context, s StopRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.ID == 0 {
		s.ID = m.allocID()
	}
	m.stops[s.ID] = s
	return s.ID, nil
}
func (m *MemoryStore) UpsertStationCode(ctx context.Context, c StationCodeRow) error {
	m.mu.Lock()
	m.stationCodes = append(m.stationCodes, c)
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) UpsertCarrier(ctx context.Context, c CarrierRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.ID == 0 {
		c.ID = m.allocID()
	}
	m.carriers[c.ID] = c
	return c.ID, nil
}
func (m *MemoryStore) UpsertRoute(ctx context.Context, r RouteRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.ID == 0 {
		r.ID = m.allocID()
	}
	m.routes[r.ID] = r
	return r.ID, nil
}
func (m *MemoryStore) UpsertTrip(ctx context.Context, t TripRow) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.ID == 0 {
		t.ID = m.allocID()
	}
	m.trips[t.ID] = t
	return t.ID, nil
}
func (m *MemoryStore) UpsertFrequency(ctx context.Context, f FrequencyRow) error {
	m.mu.Lock()
	m.frequencies = append(m.frequencies, f)
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) UpsertStopTime(ctx context.Context, st StopTimeRow) error {
	m.mu.Lock()
	m.stopTimes = append(m.stopTimes, st)
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) UpsertTransfer(ctx context.Context, tr TransferRow) error {
	m.mu.Lock()
	m.transfers = append(m.transfers, tr)
	m.mu.Unlock()
	return nil
}

func (m *MemoryStore) LoadNetwork(ctx context.Context, providers []string, day time.Time) (*model.Network, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	allow := map[string]bool{}
	if len(providers) > 0 {
		for _, p := range providers {
			allow[p] = true
		}
	}
	net := model.NewNetwork()
	stopIDMap := map[int64]string{}
	for _, s := range m.stations {
		if len(allow) > 0 && s.PrimaryProvider != "" && !allow[s.PrimaryProvider] {
			continue
		}
		id := s.ID
		_ = id
	}
	for id, st := range m.stops {
		if len(allow) > 0 && !allow[st.ProviderID] {
			continue
		}
		sid := string(rune(id))
		strID := st.ExternalCode
		if strID == "" {
			strID = st.Name
		}
		station, ok := m.stations[st.StationID]
		lat, lon := 0.0, 0.0
		if ok {
			lat, lon = station.Lat, station.Lon
		}
		ms := &model.Stop{ID: strID, ProviderID: st.ProviderID, Name: st.Name, Lat: lat, Lon: lon, Type: model.StopType(st.StopType)}
		if ms.Type == "" {
			ms.Type = model.InferStopType(st.Name)
		}
		net.Stops[strID] = ms
		stopIDMap[id] = strID
		_ = sid
	}
	for _, r := range m.routes {
		if len(allow) > 0 && !allow[r.ProviderID] {
			continue
		}
		mr := &model.Route{ID: r.ExternalCode, ProviderID: r.ProviderID, ShortName: r.ShortName, LongName: r.LongName, Mode: model.Mode(r.Mode)}
		net.Routes[r.ExternalCode] = mr
	}
	for _, t := range m.trips {
		if len(allow) > 0 && !allow[t.ProviderID] {
			continue
		}
		route, ok := m.routes[t.RouteID]
		routeID := ""
		if ok {
			routeID = route.ExternalCode
		}
		times := []model.StopTime{}
		for _, st := range m.stopTimes {
			if st.TripID != t.ID {
				continue
			}
			sid, ok := stopIDMap[st.StopID]
			if !ok {
				continue
			}
			times = append(times, model.StopTime{StopID: sid, Sequence: st.Seq, ArrivalSec: st.Arrival, DepartureSec: st.Departure})
		}
		sort.Slice(times, func(i, j int) bool { return times[i].Sequence < times[j].Sequence })
		if len(times) == 0 {
			continue
		}
		mt := &model.Trip{ID: routeID + ":" + t.Direction, RouteID: routeID, ProviderID: t.ProviderID, Mode: model.Mode(route.Mode), ServiceID: t.ServiceID, StopTimes: times}
		net.Trips[mt.ID] = mt
		for i := 0; i < len(times)-1; i++ {
			_ = i
		}
	}
	// transfers
	for _, tr := range m.transfers {
		from, ok1 := stopIDMap[tr.FromStopID]
		to, ok2 := stopIDMap[tr.ToStopID]
		if !ok1 || !ok2 {
			continue
		}
		net.Transfers = append(net.Transfers, model.Transfer{FromStopID: from, ToStopID: to, Minutes: tr.Minutes})
	}
	dayBase := time.Now().UTC().Truncate(24 * time.Hour)
	if !day.IsZero() {
		dayBase = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	}
	// Build connections from stopTimes via trips
	for _, trip := range net.Trips {
		for i := 0; i < len(trip.StopTimes)-1; i++ {
			from := trip.StopTimes[i]
			to := trip.StopTimes[i+1]
			base := dayBase
			dep := base.Add(time.Duration(from.DepartureSec) * time.Second)
			arr := base.Add(time.Duration(to.ArrivalSec) * time.Second)
			if arr.Before(dep) {
				arr = arr.Add(24 * time.Hour)
			}
			net.Connections = append(net.Connections, model.Connection{
				TripID: trip.ID, ProviderID: trip.ProviderID, RouteID: trip.RouteID, Mode: trip.Mode,
				From: from.StopID, To: to.StopID, Departure: dep, Arrival: arr,
			})
		}
	}
	sort.Slice(net.Connections, func(i, j int) bool { return net.Connections[i].Departure.Before(net.Connections[j].Departure) })
	net.BuildIndexes()
	return net, nil
}
