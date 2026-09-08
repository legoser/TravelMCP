package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	store "travelmcp/internal/store"
)

func TestLoadNetworkTombstonedTripsExcluded(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	routeID, err := m.UpsertRoute(ctx, store.RouteRow{ProviderID: "mintrans", ExternalRouteCode: "22.42.029", LongName: "Славгород — Белово", Mode: "bus"})
	if err != nil {
		t.Fatal(err)
	}
	dead := "2026-09-08T00:00:00Z"
	live, err := m.UpsertTrip(ctx, store.TripRow{RouteID: routeID, ProviderID: "mintrans", ExternalTripCode: "forward:20:0"})
	if err != nil {
		t.Fatal(err)
	}
	tomb, err := m.UpsertTrip(ctx, store.TripRow{RouteID: routeID, ProviderID: "mintrans", ExternalTripCode: "backward:20:0", ValidTo: &dead})
	if err != nil {
		t.Fatal(err)
	}
	stops := make([]int64, 2)
	for i, name := range []string{"Славгород", "Белово"} {
		id, err := m.UpsertStop(ctx, store.StopRow{ProviderID: "mintrans", ExternalCode: name, Name: name, Lat: 53.5 + float64(i), Lon: 78.0 + float64(i), StopType: "bus_station"})
		if err != nil {
			t.Fatal(err)
		}
		stops[i] = id
	}
	liveTimes := []store.StopTimeRow{
		{TripID: live, StopID: stops[0], Seq: 1, Arrival: 3600, Departure: 3600},
		{TripID: live, StopID: stops[1], Seq: 2, Arrival: 7200, Departure: 7200},
	}
	for _, st := range liveTimes {
		if err := m.UpsertStopTime(ctx, st); err != nil {
			t.Fatal(err)
		}
	}
	tombTimes := []store.StopTimeRow{
		{TripID: tomb, StopID: stops[0], Seq: 1, Arrival: 9000, Departure: 9000},
		{TripID: tomb, StopID: stops[1], Seq: 2, Arrival: 9300, Departure: 9300},
	}
	for _, st := range tombTimes {
		if err := m.UpsertStopTime(ctx, st); err != nil {
			t.Fatal(err)
		}
	}
	net, err := m.LoadNetwork(ctx, []string{"mintrans"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(net.Trips) != 1 {
		t.Fatalf("tombstoned-трип (valid_to) не должен попадать в сеть: ожидался 1 трип, получено %d", len(net.Trips))
	}
	if _, ok := net.Trips["22.42.029|forward:20:0"]; !ok {
		t.Fatalf("живой трип отсутствует в сети: %v", keys(net.Trips))
	}
	if len(net.Connections) != 1 {
		t.Fatalf("связи строются только по живым трипам: ожидалось 1 connection, получено %d", len(net.Connections))
	}
}

func TestLoadNetworkServiceDaysFilter(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	routeID, err := m.UpsertRoute(ctx, store.RouteRow{ProviderID: "mintrans", ExternalRouteCode: "24.42.004/3", LongName: "Кемерово — Новосибирск", Mode: "bus"})
	if err != nil {
		t.Fatal(err)
	}
	tue := "2"
	weekend := "0,6"
	idTue, err := m.UpsertTrip(ctx, store.TripRow{RouteID: routeID, ProviderID: "mintrans", ExternalTripCode: "tue-only", ServiceDays: tue})
	if err != nil {
		t.Fatal(err)
	}
	idWeekend, err := m.UpsertTrip(ctx, store.TripRow{RouteID: routeID, ProviderID: "mintrans", ExternalTripCode: "weekend-only", ServiceDays: weekend})
	if err != nil {
		t.Fatal(err)
	}
	stopID, err := m.UpsertStop(ctx, store.StopRow{ProviderID: "mintrans", ExternalCode: "s1", Name: "Кемерово", Lat: 55.35, Lon: 86.08, StopType: "bus_station"})
	if err != nil {
		t.Fatal(err)
	}
	stopID2, err := m.UpsertStop(ctx, store.StopRow{ProviderID: "mintrans", ExternalCode: "s2", Name: "Новосибирск", Lat: 55.03, Lon: 82.92, StopType: "bus_station"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{idTue, idWeekend} {
		if err := m.UpsertStopTime(ctx, store.StopTimeRow{TripID: id, StopID: stopID, Seq: 1, Arrival: 3600, Departure: 3600}); err != nil {
			t.Fatal(err)
		}
		if err := m.UpsertStopTime(ctx, store.StopTimeRow{TripID: id, StopID: stopID2, Seq: 2, Arrival: 18000, Departure: 18000}); err != nil {
			t.Fatal(err)
		}
	}
	tuesday := time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC)
	net, err := m.LoadNetwork(ctx, []string{"mintrans"}, tuesday)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := net.Trips["24.42.004/3|tue-only"]; !ok {
		t.Fatalf("трип с service_days=%q обязан попасть в сеть во вторник (Weekday=2), got %v", tue, keys(net.Trips))
	}
	if _, ok := net.Trips["24.42.004/3|weekend-only"]; ok {
		t.Fatalf("трип с service_days=%q не должен попадать в сеть во вторник", weekend)
	}
	saturday := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	net, err = m.LoadNetwork(ctx, []string{"mintrans"}, saturday)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := net.Trips["24.42.004/3|weekend-only"]; !ok {
		t.Fatalf("трип weekend-only обязан попасть в сеть в субботу")
	}
	if _, ok := net.Trips["24.42.004/3|tue-only"]; ok {
		t.Fatalf("трип tue-only не должен попадать в сеть в субботу")
	}
}

func keys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestLoadNetworkNoServiceDaysAlwaysIncluded(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	routeID, err := m.UpsertRoute(ctx, store.RouteRow{ProviderID: "mintrans", ExternalRouteCode: "r1", Mode: "bus"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.UpsertTrip(ctx, store.TripRow{RouteID: routeID, ProviderID: "mintrans", ExternalTripCode: "no-cal"})
	if err != nil {
		t.Fatal(err)
	}
	s1, err := m.UpsertStop(ctx, store.StopRow{ProviderID: "mintrans", ExternalCode: "a", Name: "A", Lat: 1, Lon: 1})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := m.UpsertStop(ctx, store.StopRow{ProviderID: "mintrans", ExternalCode: "b", Name: "B", Lat: 1.1, Lon: 1.1})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertStopTime(ctx, store.StopTimeRow{TripID: id, StopID: s1, Seq: 1, Arrival: 60, Departure: 60}); err != nil {
		t.Fatal(err)
	}
	if err := m.UpsertStopTime(ctx, store.StopTimeRow{TripID: id, StopID: s2, Seq: 2, Arrival: 120, Departure: 120}); err != nil {
		t.Fatal(err)
	}
	for _, day := range []time.Time{
		time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC),
	} {
		net, err := m.LoadNetwork(ctx, []string{"mintrans"}, day)
		if err != nil {
			t.Fatal(err)
		}
		if len(net.Trips) != 1 {
			t.Fatalf("трип без календаря должен попадать в сеть в любой день (%s): %d", day.Weekday(), len(net.Trips))
		}
		if len(net.Connections) != 1 {
			t.Fatalf("connection для трипа без календаря: %d", len(net.Connections))
		}
		if !strings.HasPrefix(net.Connections[0].Departure.Format("2006-01-02"), day.Format("2006-01-02")) {
			t.Fatalf("connection должен датироваться запрошенным днём, got %v", net.Connections[0].Departure)
		}
	}
}
