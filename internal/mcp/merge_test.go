package mcp

import (
	"testing"
	"time"

	"travelmcp/internal/model"
	"travelmcp/internal/planner"
	"travelmcp/internal/providers"
	"travelmcp/internal/telemetry"
)

type mockProv struct {
	id  string
	net *model.Network
}

func (m *mockProv) ID() string                       { return m.id }
func (m *mockProv) Health() providers.HealthStatus   { return providers.HealthStatus{Up: true} }
func (m *mockProv) Network() (*model.Network, error) { return m.net, nil }

func TestMergeIsolatesProviderConflict(t *testing.T) {
	n1 := model.NewNetwork()
	n1.Stops["dup"] = &model.Stop{ID: "dup", ProviderID: "p1", Name: "A", Lat: 55, Lon: 83, Type: model.StopTypeStation}
	n1.Routes["r1"] = &model.Route{ID: "r1", ProviderID: "p1", ShortName: "r1", Mode: model.ModeBus}
	n1.Trips["t1"] = &model.Trip{ID: "t1", RouteID: "r1", ProviderID: "p1", StopTimes: []model.StopTime{{StopID: "dup", Sequence: 0}}}
	n1.Connections = []model.Connection{{TripID: "t1", ProviderID: "p1", RouteID: "r1", From: "dup", To: "dup", Departure: time.Now(), Arrival: time.Now().Add(time.Hour)}}
	n1.Stops["dup2"] = &model.Stop{ID: "dup2", ProviderID: "p1", Name: "B", Lat: 55, Lon: 84, Type: model.StopTypeStation}
	n2 := model.NewNetwork()
	n2.Stops["dup"] = &model.Stop{ID: "dup", ProviderID: "p2", Name: "A2", Lat: 56, Lon: 83, Type: model.StopTypeStation}
	n2.Routes["r1"] = &model.Route{ID: "r1", ProviderID: "p2", ShortName: "r1", Mode: model.ModeBus}
	n2.Trips["t1"] = &model.Trip{ID: "t1", RouteID: "r1", ProviderID: "p2", StopTimes: []model.StopTime{{StopID: "dup", Sequence: 0}}}
	reg := providers.NewRegistry(nil)
	reg.Register(&mockProv{id: "p1", net: n1})
	reg.Register(&mockProv{id: "p2", net: n2})
	app := New(planner.New(telemetry.New()), reg)
	net, err := app.networkForDay(time.Now())
	if err != nil {
		t.Fatalf("networkForDay failed: %v", err)
	}
	if _, ok := net.Stops["dup"]; !ok {
		t.Fatalf("want dup")
	}
	if _, ok := net.Stops["p2:dup"]; !ok {
		t.Fatalf("want p2:dup suffixed, got %v", keys(net.Stops))
	}
	if _, ok := net.Routes["r1"]; !ok {
		t.Fatalf("want r1")
	}
	if _, ok := net.Routes["p2:r1"]; !ok {
		t.Fatalf("want p2:r1")
	}
	if _, ok := net.Trips["t1"]; !ok {
		t.Fatalf("want t1")
	}
	if _, ok := net.Trips["p2:t1"]; !ok {
		t.Fatalf("want p2:t1")
	}
	// excluded stops
	n3 := model.NewNetwork()
	n3.Stops["bad"] = &model.Stop{ID: "bad", ProviderID: "p3", Name: "Bad", Lat: 0, Lon: 0, Type: model.StopTypeStation}
	n3.Stops["good"] = &model.Stop{ID: "good", ProviderID: "p3", Name: "Good", Lat: 55, Lon: 83, Type: model.StopTypeStation}
	reg2 := providers.NewRegistry(nil)
	reg2.Register(&mockProv{id: "p3", net: n3})
	app2 := New(planner.New(telemetry.New()), reg2)
	net2, _ := app2.networkForDay(time.Now())
	if _, ok := net2.Stops["bad"]; ok {
		t.Fatalf("bad should be excluded")
	}
	if _, ok := net2.Stops["good"]; !ok {
		t.Fatalf("good missing")
	}
}

func keys(m map[string]*model.Stop) []string {
	var o []string
	for k := range m {
		o = append(o, k)
	}
	return o
}
