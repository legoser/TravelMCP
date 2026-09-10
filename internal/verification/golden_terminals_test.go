package verification

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"travelmcp/internal/config"
	"travelmcp/internal/model"
)

// TestGoldenTerminalsScorepair — золотой набор identity-кейсов терминалов
// (Фаза 5 §8): «должен/не должен быть терминалом» + merge-кейсы. Требование
// плана: precision/recall 1.0 по каждой задаче; калибровочная выборка
// Кузбасса с этим набором не пересекается.
func TestGoldenTerminalsScorepair(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden_terminals.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var cases []goldenTripCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	cfg := config.Verification{}
	evaluated, correct, skipped := 0, 0, 0
	tp, fp, fn := 0, 0, 0
	for _, tc := range cases {
		if tc.Matcher != "" && tc.Matcher != "scorepair" {
			skipped++
			continue
		}
		if tc.Task != "skeleton_osm" && tc.Task != "merge" {
			skipped++
			continue
		}
		evaluated++
		class := model.DensityClass(tc.Class)
		if class == "" {
			class = model.DensityUrban
		}
		p := DefaultStopTerminalParams(cfg, class)
		terms := make([]PairItem, 0, len(tc.Terminals))
		for _, g := range tc.Terminals {
			terms = append(terms, g.item())
		}
		best, d, s := MatchStopToTerminal(tc.Stop.item(), terms, class, p)
		ok := string(d) == tc.Expect && (tc.ExpectBest == nil || best == *tc.ExpectBest)
		if !ok {
			t.Errorf("%s: got %v best %d score %.3f, want %s", tc.ID, d, best, s.Value, tc.Expect)
			continue
		}
		correct++
		// precision/recall по смыслу «должен сматчиться (verified)»
		wantMatch := tc.Expect == "verified"
		gotMatch := d == DecisionVerified
		switch {
		case wantMatch && gotMatch:
			tp++
		case !wantMatch && gotMatch:
			fp++
		case wantMatch && !gotMatch:
			fn++
		}
	}
	if evaluated == 0 {
		t.Fatalf("нет исполняемых кейсов (все skipped=%d?)", skipped)
	}
	if correct != evaluated {
		t.Fatalf("correct=%d evaluated=%d — ожидания не сходятся", correct, evaluated)
	}
	precision := 1.0
	if tp+fp > 0 {
		precision = float64(tp) / float64(tp+fp)
	}
	recall := 1.0
	if tp+fn > 0 {
		recall = float64(tp) / float64(tp+fn)
	}
	if precision != 1.0 || recall != 1.0 {
		t.Fatalf("precision=%.2f recall=%.2f (tp=%d fp=%d fn=%d), требование плана — 1.0/1.0", precision, recall, tp, fp, fn)
	}
}
