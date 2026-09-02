package store

import (
	"context"
	"fmt"
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
	users        map[int64]UserRow
	usersByEmail map[string]int64
	apiKeys      map[int64]ApiKeyRow
	apiKeysByKey map[string]int64
	nextID       int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		stations:     make(map[int64]StationRow),
		stops:        make(map[int64]StopRow),
		carriers:     make(map[int64]CarrierRow),
		routes:       make(map[int64]RouteRow),
		trips:        make(map[int64]TripRow),
		imports:      make(map[string]time.Time),
		users:        make(map[int64]UserRow),
		usersByEmail: make(map[string]int64),
		apiKeys:      make(map[int64]ApiKeyRow),
		apiKeysByKey: make(map[string]int64),
		nextID:       1,
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
func (m *MemoryStore) CreateUser(ctx context.Context, email, passHash, role string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.usersByEmail[email]; ok {
		return 0, fmt.Errorf("user exists")
	}
	id := m.allocID()
	status := "pending"
	if role == "admin" {
		status = "active"
	}
	m.users[id] = UserRow{ID: id, Email: email, PassHash: passHash, Status: status, Role: role, CreatedAt: time.Now().Unix()}
	m.usersByEmail[email] = id
	return id, nil
}
func (m *MemoryStore) GetUserByEmail(ctx context.Context, email string) (UserRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.usersByEmail[email]
	if !ok {
		return UserRow{}, false
	}
	u, ok := m.users[id]
	return u, ok
}
func (m *MemoryStore) GetUserByID(ctx context.Context, id int64) (UserRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[id]
	return u, ok
}
func (m *MemoryStore) ListUsers(ctx context.Context) ([]UserRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []UserRow
	for _, u := range m.users {
		out = append(out, u)
	}
	return out, nil
}
func (m *MemoryStore) UpdateUserStatus(ctx context.Context, id int64, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	u.Status = status
	m.users[id] = u
	return nil
}
func (m *MemoryStore) UpdateUserConfig(ctx context.Context, id int64, config string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	u.Config = config
	m.users[id] = u
	return nil
}
func (m *MemoryStore) CreateApiKey(ctx context.Context, userID int64, scopes string) (ApiKeyRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if scopes == "" {
		scopes = "mcp:read"
	}
	id := m.allocID()
	key := fmt.Sprintf("tm_%d_%d", userID, time.Now().UnixNano())
	row := ApiKeyRow{ID: id, UserID: userID, Key: key, Scopes: scopes, CreatedAt: time.Now().Unix()}
	m.apiKeys[id] = row
	m.apiKeysByKey[key] = id
	return row, nil
}
func (m *MemoryStore) GetApiKey(ctx context.Context, key string) (ApiKeyRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.apiKeysByKey[key]
	if !ok {
		return ApiKeyRow{}, false
	}
	r, ok := m.apiKeys[id]
	return r, ok
}
func (m *MemoryStore) ListApiKeys(ctx context.Context, userID int64) ([]ApiKeyRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ApiKeyRow
	for _, k := range m.apiKeys {
		if k.UserID == userID {
			out = append(out, k)
		}
	}
	return out, nil
}
func (m *MemoryStore) DeleteApiKey(ctx context.Context, id int64, userID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.apiKeys[id]
	if !ok || k.UserID != userID {
		return fmt.Errorf("not found")
	}
	delete(m.apiKeys, id)
	delete(m.apiKeysByKey, k.Key)
	return nil
}
func (m *MemoryStore) TouchApiKey(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.apiKeysByKey[key]
	if !ok {
		return nil
	}
	k := m.apiKeys[id]
	k.LastUsed = time.Now().Unix()
	m.apiKeys[id] = k
	return nil
}
func (m *MemoryStore) MarkImported(ctx context.Context, providerID string, at time.Time, records int) error {
	m.mu.Lock()
	m.imports[providerID] = at
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) MarkImportedVersion(ctx context.Context, providerID, snapshot, checksum string, at time.Time, records, issues int) error {
	m.mu.Lock()
	m.imports[providerID] = at
	m.mu.Unlock()
	return nil
}
func (m *MemoryStore) GetImport(ctx context.Context, providerID string) (ImportRow, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	at, ok := m.imports[providerID]
	if !ok {
		return ImportRow{}, false
	}
	return ImportRow{ProviderID: providerID, At: at.Unix()}, true
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
