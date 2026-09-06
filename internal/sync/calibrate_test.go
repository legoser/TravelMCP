package sync

import (
	"testing"
)

func TestCalibrateAttachFixture(t *testing.T) {
	trips := loadFlatTrips(t)
	terms := indexFromFixture(t, trips)
	stops := registryStopsFromTrips(t)
	thresholds := []float64{0.3, 0.4, 0.5, 0.6, 0.7, 0.8}
	rep := CalibrateAttach(stops, terms, thresholds, 0.1, 0.005, attachParamsFor, nil, "mintrans")
	for _, p := range rep.Clean {
		t.Logf("clean thr=%.1f verified=%d/%d margin=%d rejected=%d", p.Threshold, p.Verified, p.Total, p.MarginBand, p.Rejected)
	}
	for _, p := range rep.Noisy {
		t.Logf("noisy thr=%.1f verified=%d/%d margin=%d rejected=%d", p.Threshold, p.Verified, p.Total, p.MarginBand, p.Rejected)
	}
	for i := 1; i < len(rep.Clean); i++ {
		if rep.Clean[i].Verified > rep.Clean[i-1].Verified {
			t.Fatal("verified обязан монотонно не расти с порогом")
		}
	}
	rec, why := RecommendThreshold(rep, 0.8)
	t.Logf("recommended=%.2f (%s)", rec, why)
	if rec != 0.7 {
		t.Fatalf("калибровка на фикстуре обязана рекомендовать 0.7 (плато робастности к шуму 555м, обрыв на 0.8): recommended=%.2f (%s)", rec, why)
	}
	for _, p := range rep.Clean {
		if p.Threshold == 0.6 && p.Verified != p.Total {
			t.Fatalf("дефолтный порог 0.6 обязан держать 100%% verified на чистых данных: %+v", p)
		}
	}
}
