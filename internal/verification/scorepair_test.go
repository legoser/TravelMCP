package verification

import (
	"testing"

	"travelmcp/internal/config"
	"travelmcp/internal/model"
)

func ptr(f float64) *float64 { return &f }

func testParams() Params {
	return DefaultStopTerminalParams(config.Verification{}, model.DensityUrban)
}

func TestTrustMatrix(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"mintrans", "mintrans", 0.6},
		{"yandex", "yandex", 0.6},
		{"osm", "motis", 0.6},
		{"osm", "yandex", 1},
		{"osm", "mintrans", 1},
		{"mintrans", "yandex", 1},
		{"seed", "osm", 0.4},
		{"seed", "seed", 0.4},
		{"legacy", "osm", 0.7},
		{"legacy", "legacy", 0.7},
		{"manual", "legacy", 1},
		{"manual", "manual", 1},
		{"", "", 0.6},
		{"osm", "", 1},
	}
	for _, tc := range cases {
		if got := Trust(tc.a, tc.b); got != tc.want {
			t.Errorf("Trust(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestRenormalizeNullGeom(t *testing.T) {
	p := testParams()
	a := PairItem{Name: "Кемерово автовокзал", Settlement: "кемерово", Source: "mintrans"}
	b := PairItem{Name: "Кемерово автовокзал", Settlement: "кемерово", Source: "osm"}
	s := ScorePair(a, b, model.DensityRural, p)
	if s.Value != 1 {
		t.Fatalf("null-geom identical pair must renormalize to 1, got %v", s.Value)
	}
}

func TestUrbanNullGeomNeverVerified(t *testing.T) {
	p := testParams()
	a := PairItem{Name: "Кемерово автовокзал", Settlement: "кемерово", Source: "mintrans"}
	b := PairItem{Name: "Кемерово автовокзал", Lat: ptr(55.34), Lon: ptr(86.06), Settlement: "кемерово", Source: "osm"}
	s := ScorePair(a, b, model.DensityUrban, p)
	if s.GuardOK {
		t.Fatalf("urban geom-null match must not pass guard: %+v", s)
	}
	idx, d, _ := MatchStopToTerminal(a, []PairItem{b}, model.DensityUrban, p)
	if idx != 0 || d != DecisionLowConfidence {
		t.Fatalf("want low_confidence, got %v idx %d", d, idx)
	}
}

func TestRuralNullGeomVerified(t *testing.T) {
	p := testParams()
	a := PairItem{Name: "Кемерово автовокзал", Settlement: "кемерово", Source: "mintrans"}
	b := PairItem{Name: "Кемерово автовокзал", Lat: ptr(55.34), Lon: ptr(86.06), Settlement: "кемерово", Source: "osm"}
	idx, d, _ := MatchStopToTerminal(a, []PairItem{b}, model.DensityRural, p)
	if idx != 0 || d != DecisionVerified {
		t.Fatalf("want verified, got %v idx %d", d, idx)
	}
}

func TestSameVoiceRuralNullGeomNotVerified(t *testing.T) {
	p := testParams()
	a := PairItem{Name: "Кемерово автовокзал", Settlement: "кемерово", Source: "mintrans"}
	b := PairItem{Name: "Кемерово автовокзал", Settlement: "кемерово", Source: "mintrans"}
	_, d, _ := MatchStopToTerminal(a, []PairItem{b}, model.DensityRural, p)
	if d == DecisionVerified {
		t.Fatalf("same-voice rural null-geom must not verify, got %v", d)
	}
}

func TestAmbiguousTwins(t *testing.T) {
	p := testParams()
	a := PairItem{Name: "Кемерово автовокзал", Lat: ptr(55.34), Lon: ptr(86.06), Settlement: "кемерово", Source: "mintrans"}
	b := PairItem{Name: "Кемерово автовокзал", Lat: ptr(55.34), Lon: ptr(86.06), Settlement: "кемерово", Source: "osm"}
	_, d, _ := MatchStopToTerminal(a, []PairItem{b, b}, model.DensityUrban, p)
	if d != DecisionDuplicateAmbiguous {
		t.Fatalf("want duplicate_ambiguous, got %v", d)
	}
}

func TestCodeMatchStrongest(t *testing.T) {
	p := testParams()
	a := PairItem{
		Name:   "ОП ЛПК",
		Source: "mintrans",
		Codes:  []model.AdaptedIdentifier{{System: "yandex", CodeType: "yandex_code", Code: "999"}},
	}
	b := PairItem{
		Name:   "Заречная",
		Lat:    ptr(55.0),
		Lon:    ptr(83.0),
		Source: "yandex",
		Codes:  []model.AdaptedIdentifier{{System: "yandex", CodeType: "yandex_code", Code: "999"}},
	}
	s := ScorePair(a, b, model.DensityUrban, p)
	if !s.CodeMatch || !s.GuardOK {
		t.Fatalf("code match must guard-pass: %+v", s)
	}
	_, d, _ := MatchStopToTerminal(a, []PairItem{b}, model.DensityUrban, p)
	if d != DecisionVerified {
		t.Fatalf("want verified, got %v", d)
	}
}

func TestEmptyCandidates(t *testing.T) {
	p := testParams()
	a := PairItem{Name: "Кемерово автовокзал", Source: "mintrans"}
	idx, d, _ := MatchStopToTerminal(a, nil, model.DensityUrban, p)
	if idx != -1 || d != DecisionRejected {
		t.Fatalf("want rejected/-1, got %v idx %d", d, idx)
	}
}

func TestDecisionReviewReason(t *testing.T) {
	if DecisionVerified.ReviewReason() != "" || DecisionRejected.ReviewReason() != "" {
		t.Fatal("verified/rejected must not map to review")
	}
	if DecisionLowConfidence.ReviewReason() != "low_confidence" {
		t.Fatal("low_confidence mapping broken")
	}
	if DecisionDuplicateAmbiguous.ReviewReason() != "duplicate_ambiguous" {
		t.Fatal("duplicate_ambiguous mapping broken")
	}
}
