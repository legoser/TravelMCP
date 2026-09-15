package memory

import (
	"context"
	"testing"

	"travelmcp/test/common"
)

type memDemandSeeder struct {
	ms *MemoryStore
}

func (s *memDemandSeeder) SeedDemandTrip(t *testing.T, tag string) (int64, string, string, string) {
	t.Helper()
	ctx := context.Background()
	prov, routeCode, extCode := "demand_"+tag, "r_"+tag, "t_"+tag
	routeID, err := s.ms.UpsertRoute(ctx, RouteRow{ProviderID: prov, ExternalRouteCode: routeCode, Mode: "bus"})
	if err != nil {
		t.Fatal(err)
	}
	tripID, err := s.ms.UpsertTrip(ctx, TripRow{RouteID: routeID, ProviderID: prov, ExternalTripCode: extCode})
	if err != nil {
		t.Fatal(err)
	}
	return tripID, prov, routeCode, extCode
}

func TestDemandContractMemory(t *testing.T) {
	ms := NewMemoryStore()
	common.AssertDemandContract(t, ms, &memDemandSeeder{ms: ms})
}
