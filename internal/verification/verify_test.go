package verification

import (
	"testing"

	"travelmcp/internal/config"
)

func defaultCfg() config.Verification {
	return config.Verification{
		ConfidenceThreshold: 0.6,
		DistanceM:           500,
		StrongDistanceM:     200,
		LevThreshold:        0.15,
		DensityThresholds:   map[string]config.DensityThreshold{},
	}
}

func TestStrongSingleSource(t *testing.T) {
	cfg := defaultCfg()
	a := Candidate{Lat: 55.355, Lon: 86.088, Name: "Кемерово автовокзал"}
	b := Candidate{Lat: 55.3551, Lon: 86.0881, Name: "Кемерово автовокзал"}
	res := VerifyTerminal(a, &b, nil, nil, cfg, "")
	if !res.Verified || res.Confidence < 0.6 {
		t.Fatalf("strong single should verify: %+v", res)
	}
}

func TestWeakSingleNotVerified(t *testing.T) {
	cfg := defaultCfg()
	a := Candidate{Lat: 55.355, Lon: 86.088, Name: "Кемерово автовокзал"}
	b := Candidate{Lat: 55.3552, Lon: 86.0882, Name: "Кемерово автовокзал "}
	res := VerifyTerminal(a, &b, nil, nil, cfg, "")
	if !res.Verified {
		t.Fatalf("weak single with identical name + near distance should still be strong 0.6: %+v", res)
	}
	b2 := Candidate{Lat: 55.3552, Lon: 86.0882, Name: "Совсем другое"}
	res2 := VerifyTerminal(a, &b2, nil, nil, cfg, "")
	if res2.Verified {
		t.Fatalf("different name should not verify despite proximity: %+v", res2)
	}
}

func TestTwoSourcesVerifies(t *testing.T) {
	cfg := defaultCfg()
	a := Candidate{Lat: 55.355, Lon: 86.088, Name: "Кемерово автовокзал"}
	b := Candidate{Lat: 55.3555, Lon: 86.0885, Name: "Кемерово автовокзал"}
	c := Candidate{Lat: 55.3552, Lon: 86.0882, Name: "Кемерово автовокзал"}
	res := VerifyTerminal(a, &b, &c, nil, cfg, "")
	if !res.Verified || res.Confidence < 0.8 {
		t.Fatalf("two sources should verify: %+v", res)
	}
}

func TestDensityOverride(t *testing.T) {
	cfg := defaultCfg()
	thin := 50
	cfg.DensityThresholds["metro"] = config.DensityThreshold{DistanceM: &thin}
	a := Candidate{Lat: 55.755, Lon: 37.617, Name: "Тверская"}
	b := Candidate{Lat: 55.7555, Lon: 37.6175, Name: "Тверская"}
	resRural := VerifyTerminal(a, &b, nil, nil, cfg, "rural")
	resMetro := VerifyTerminal(a, &b, nil, nil, cfg, "metro")
	if !resRural.Verified {
		t.Fatalf("rural should verify close distance: %+v", resRural)
	}
	if resMetro.Verified {
		t.Fatalf("metro with 50m threshold should not verify 70m distance: %+v", resMetro)
	}
}
