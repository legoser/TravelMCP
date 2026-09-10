package sync

import (
	"testing"

	"travelmcp/internal/support/namesim"
)

func registryStopsFromTrips(t *testing.T) []RegistryStop {
	t.Helper()
	trips := loadFlatTrips(t)
	seen := map[string]bool{}
	var out []RegistryStop
	for _, ft := range trips {
		for _, s := range ft.Stops {
			if seen[s.StopID] {
				continue
			}
			seen[s.StopID] = true
			out = append(out, RegistryStop{
				Name: s.Name, Region: s.Region,
				Settlement: namesim.ExtractSettlement(s.Name),
				Lat:        s.Lat, Lon: s.Lon,
			})
		}
	}
	return out
}

func TestMeasureAttachCoverageFull(t *testing.T) {
	trips := loadFlatTrips(t)
	terms := indexFromFixture(t, trips)
	stops := registryStopsFromTrips(t)
	cov := MeasureAttachCoverage(stops, terms, 0.4, attachParamsFor, nil, testSource)
	if len(cov) == 0 {
		t.Fatal("пустое покрытие по фикстуре")
	}
	for _, c := range cov {
		if c.Ratio() != 1 {
			t.Fatalf("регион %s: ratio=%.3f, want 1.0 на полном индексе", c.Region, c.Ratio())
		}
	}
	if ok, blocked := GatePassRegions(cov, 0.8); !ok || len(blocked) != 0 {
		t.Fatalf("gate = %v %v, want pass", ok, blocked)
	}
}

func TestMeasureAttachCoverageGap(t *testing.T) {
	trips := loadFlatTrips(t)
	terms := indexFromFixture(t, trips)
	stops := registryStopsFromTrips(t)
	full := MeasureAttachCoverage(stops, terms, 0.4, attachParamsFor, nil, testSource)
	empty := MeasureAttachCoverage(stops, nil, 0.4, attachParamsFor, nil, testSource)
	fullMatched, emptyMatched := 0, 0
	for _, c := range full {
		fullMatched += c.Matched
	}
	for _, c := range empty {
		emptyMatched += c.Matched
	}
	if fullMatched != len(stops) {
		t.Fatalf("полный индекс: matched=%d want %d", fullMatched, len(stops))
	}
	if emptyMatched != 0 {
		t.Fatalf("пустой индекс: matched=%d want 0", emptyMatched)
	}
}

func TestGatePassRegions(t *testing.T) {
	cov := []RegionCoverage{{Region: "42", Matched: 8, Total: 10}, {Region: "54", Matched: 9, Total: 10}}
	if ok, _ := GatePassRegions(cov, 0.8); !ok {
		t.Fatal("gate 0.8 обязан пройти")
	}
	ok, blocked := GatePassRegions(cov, 0.85)
	if ok || len(blocked) != 1 || blocked[0] != "42" {
		t.Fatalf("gate = %v %v, want blocked [42]", ok, blocked)
	}
}
