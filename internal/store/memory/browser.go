package memory

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	store "travelmcp/internal/store"
)

func (m *MemoryStore) terminalStopIDs(terminalID int64) []int64 {
	stopIDs := []int64{}
	for _, s := range m.stops {
		if s.TerminalID == terminalID {
			stopIDs = append(stopIDs, s.ID)
		}
	}
	return stopIDs
}

func (m *MemoryStore) terminalNameByID(id int64) string {
	if m.terminalNames[id] == nil {
		return ""
	}
	if n := m.terminalNames[id]["ru"]; n != "" {
		return n
	}
	for _, v := range m.terminalNames[id] {
		return v
	}
	return ""
}

func (m *MemoryStore) ListRoutesAdmin(ctx context.Context, limit, offset int, q string) ([]map[string]any, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	q = strings.TrimSpace(strings.ToLower(q))
	filtered := []map[string]any{}
	for _, r := range m.routes {
		if q != "" && !strings.Contains(strings.ToLower(r.ShortName), q) && !strings.Contains(strings.ToLower(r.LongName), q) && !strings.Contains(strings.ToLower(r.ExternalRouteCode), q) {
			continue
		}
		liveTrips := 0
		for _, t := range m.trips {
			if t.RouteID == r.ID && t.ValidTo == nil {
				liveTrips++
			}
		}
		carrier := ""
		if c, ok := m.carriers[r.CarrierID]; ok {
			carrier = c.Name
		}
		filtered = append(filtered, map[string]any{"id": r.ID, "provider": r.ProviderID, "external_route_code": r.ExternalRouteCode, "short_name": r.ShortName, "long_name": r.LongName, "mode": r.Mode, "carrier": carrier, "live_trips": liveTrips})
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i]["id"].(int64) < filtered[j]["id"].(int64) })
	total := len(filtered)
	if offset >= total {
		return []map[string]any{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return filtered[offset:end], total, nil
}

func (m *MemoryStore) ListTripsAdmin(ctx context.Context, routeID int64, limit, offset int) ([]map[string]any, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	filtered := []map[string]any{}
	for _, t := range m.trips {
		if t.RouteID != routeID {
			continue
		}
		stopCount, provCount := 0, 0
		for _, st := range m.stopTimes {
			if st.TripID == t.ID {
				stopCount++
				if st.IsProvisional {
					provCount++
				}
			}
		}
		item := map[string]any{"id": t.ID, "external_trip_code": t.ExternalTripCode, "direction": t.Direction, "service_days": t.ServiceDays, "stop_times_count": stopCount, "provisional_count": provCount, "is_live": t.ValidTo == nil}
		if t.DirectionID != nil {
			item["direction_id"] = *t.DirectionID
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i]["id"].(int64) < filtered[j]["id"].(int64) })
	total := len(filtered)
	if offset >= total {
		return []map[string]any{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return filtered[offset:end], total, nil
}

func (m *MemoryStore) GetTripStopTimes(ctx context.Context, tripID int64) ([]map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	times := []map[string]any{}
	for _, st := range m.stopTimes {
		if st.TripID != tripID {
			continue
		}
		name := ""
		if s, ok := m.stops[st.StopID]; ok {
			name = s.Name
		}
		times = append(times, map[string]any{"seq": st.Seq, "arrival": st.Arrival, "departure": st.Departure, "pickup_type": st.PickupType, "drop_off_type": st.DropOffType, "is_provisional": st.IsProvisional, "match_score": st.MatchScore, "match_method": st.MatchMethod, "name": name})
	}
	sort.Slice(times, func(i, j int) bool { return times[i]["seq"].(int) < times[j]["seq"].(int) })
	return times, nil
}

func (m *MemoryStore) GetTerminalStats(ctx context.Context, terminalID int64) (map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stopIDs := map[int64]bool{}
	for _, s := range m.stops {
		if s.TerminalID == terminalID {
			stopIDs[s.ID] = true
		}
	}
	stopTimes, provisional, liveTrips := 0, 0, map[int64]bool{}
	for _, st := range m.stopTimes {
		if !stopIDs[st.StopID] {
			continue
		}
		stopTimes++
		if st.IsProvisional {
			provisional++
		}
		if t, ok := m.trips[st.TripID]; ok && t.ValidTo == nil {
			liveTrips[t.ID] = true
		}
	}
	return map[string]any{"stop_times": stopTimes, "provisional_stop_times": provisional, "live_trips": len(liveTrips), "dead": stopTimes == 0}, nil
}

func (m *MemoryStore) GetTerminalSchedule(ctx context.Context, terminalID int64, date time.Time, limit int) ([]map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	weekday := int(date.Weekday())
	stopIDs := map[int64]bool{}
	for _, s := range m.stops {
		if s.TerminalID == terminalID {
			stopIDs[s.ID] = true
		}
	}
	out := []map[string]any{}
	for _, st := range m.stopTimes {
		if !stopIDs[st.StopID] {
			continue
		}
		t, ok := m.trips[st.TripID]
		if !ok || t.ValidTo != nil {
			continue
		}
		if !serviceDaysMatchWeekday(t.ServiceDays, weekday) {
			continue
		}
		r, _ := m.routes[t.RouteID]
		dest := ""
		if r.LongName != "" {
			dest = r.LongName
		} else if r.ShortName != "" {
			dest = r.ShortName
		}
		out = append(out, map[string]any{"departure": st.Departure, "terminal_name": m.terminalNameByID(terminalID), "destination": dest, "mode": r.Mode, "trip_id": t.ID, "external_trip_code": t.ExternalTripCode, "service_days": t.ServiceDays})
		if len(out) >= limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["departure"].(int) < out[j]["departure"].(int) })
	return out, nil
}

func serviceDaysMatchWeekday(serviceDays string, weekday int) bool {
	if serviceDays == "" {
		return true
	}
	for _, part := range strings.Split(serviceDays, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && n == weekday {
			return true
		}
	}
	return false
}

func (m *MemoryStore) ListTerminalAliases(ctx context.Context, terminalID int64) ([]store.TerminalAliasRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []store.TerminalAliasRow{}
	for _, a := range m.aliases[terminalID] {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out, nil
}

func (m *MemoryStore) ListTerminalReviewEntries(ctx context.Context, terminalID int64) ([]store.ReviewQueueRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []store.ReviewQueueRow{}
	for _, rq := range m.reviewQueue {
		if rq.EntityType == "terminal" && rq.EntityID == terminalID {
			out = append(out, rq)
		}
	}
	return out, nil
}

func (m *MemoryStore) ApproveTerminal(ctx context.Context, terminalID int64, tr store.TerminalRow, names map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.terminals[terminalID]
	if !ok {
		return fmt.Errorf("terminal %d not found", terminalID)
	}
	if tr.Lat != 0 || tr.Lon != 0 {
		cur.Lat = tr.Lat
		cur.Lon = tr.Lon
	}
	if tr.LastVerifiedAt != nil {
		cur.LastVerifiedAt = tr.LastVerifiedAt
	}
	cur.IsLocked = true
	m.terminals[terminalID] = cur
	if len(names) > 0 {
		if m.terminalNames[terminalID] == nil {
			m.terminalNames[terminalID] = map[string]string{}
		}
		for k, v := range names {
			if v != "" {
				m.terminalNames[terminalID][k] = v
			}
		}
	}
	return nil
}

func (m *MemoryStore) ListTerminalsByLiveness(ctx context.Context, limit, offset int, dead string) ([]map[string]any, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	filtered := []map[string]any{}
	for id, tr := range m.terminals {
		stopIDs := map[int64]bool{}
		for _, s := range m.stops {
			if s.TerminalID == id {
				stopIDs[s.ID] = true
			}
		}
		tripsServed := 0
		seen := map[int64]bool{}
		for _, st := range m.stopTimes {
			if !stopIDs[st.StopID] {
				continue
			}
			if t, ok := m.trips[st.TripID]; ok && t.ValidTo == nil && !seen[st.TripID] {
				seen[st.TripID] = true
				tripsServed++
			}
		}
		isDead := tripsServed == 0
		if dead == "yes" && !isDead {
			continue
		}
		if dead == "no" && isDead {
			continue
		}
		filtered = append(filtered, map[string]any{"id": id, "name": m.terminalNameByID(id), "lat": tr.Lat, "lon": tr.Lon, "is_locked": tr.IsLocked, "place_id": tr.PlaceID, "trips_served": tripsServed, "dead": isDead})
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i]["id"].(int64) < filtered[j]["id"].(int64) })
	total := len(filtered)
	if offset >= total {
		return []map[string]any{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return filtered[offset:end], total, nil
}
