package sync

import (
	"testing"
)

func TestCalibrateAttachFixture(t *testing.T) {
	trips := loadFlatTrips(t)
	terms := indexFromFixture(t, trips)
	stops := registryStopsFromTrips(t)
	thresholds := []float64{0.3, 0.4, 0.5, 0.6, 0.7, 0.8}
	rep := CalibrateAttach(stops, terms, thresholds, 0.1, 0.005, attachParamsFor, nil, testSource)
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

// S-4: фиксация метода gate. Замер на фикстуре показывает: verified-rate — строгая
// нижняя граница soft-rate на всех уровнях деградации индекса (verified ⊂ soft:
// каждый verified-стоп проходит и soft-порог). Решение: gate меряет soft-rate,
// attach-движок продолжает требовать verified — двухуровневая дамба. Если verified
// == soft на регионе (разрыв 0), обе оси эквивалентны; расхождение — запас
// стопов "кандидат есть, но не верифицируется" — это зона operator-флоу §5.4,
// а не причина запускать attach.
func TestGateMethodSoftVsVerified(t *testing.T) {
	trips := loadFlatTrips(t)
	terms := indexFromFixture(t, trips)
	stops := registryStopsFromTrips(t)

	scenarios := []struct {
		name  string
		terms []AttachTerminal
	}{
		{"full", terms},
		{"noisy_555m", NoisyTerms(terms, 0.005, 0.005)},
		{"empty", nil},
	}
	for _, sc := range scenarios {
		rep := CompareGateMethods(stops, sc.terms, 0.4, attachParamsFor, nil, testSource)
		for _, r := range rep {
			t.Logf("%s region=%s total=%d soft=%.3f verified=%.3f", sc.name, r.Region, r.Total, r.SoftRate, r.VerifiedRate)
			if sc.name == "full" && (r.SoftRate != 1 || r.VerifiedRate != 1) {
				t.Fatalf("%s region=%s: полный индекс обязан давать 1.0/1.0, soft=%.3f verified=%.3f", sc.name, r.Region, r.SoftRate, r.VerifiedRate)
			}
			if sc.name == "empty" && (r.SoftRate != 0 || r.VerifiedRate != 0) {
				t.Fatalf("%s region=%s: пустой индекс обязан давать 0.0/0.0", sc.name, r.Region)
			}
			if r.VerifiedRate > r.SoftRate+1e-9 {
				t.Fatalf("%s region=%s: verified-rate (%.3f) не может превышать soft-rate (%.3f) — verified ⊂ soft нарушен", sc.name, r.Region, r.VerifiedRate, r.SoftRate)
			}
		}
	}
}
