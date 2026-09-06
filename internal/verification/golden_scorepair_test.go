package verification

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"travelmcp/internal/config"
	"travelmcp/internal/model"
)

type goldenPoint struct {
	Name       string                    `json:"name"`
	Lat        *float64                  `json:"lat"`
	Lon        *float64                  `json:"lon"`
	Source     string                    `json:"source"`
	Settlement string                    `json:"settlement"`
	Transport  string                    `json:"transport"`
	Codes      []model.AdaptedIdentifier `json:"codes"`
}

func (p goldenPoint) item() PairItem {
	return PairItem{
		Name:       p.Name,
		Lat:        p.Lat,
		Lon:        p.Lon,
		Source:     p.Source,
		Settlement: p.Settlement,
		Transport:  p.Transport,
		Codes:      p.Codes,
	}
}

type goldenTripCase struct {
	ID         string        `json:"id"`
	Task       string        `json:"task"`
	Matcher    string        `json:"matcher"`
	Class      string        `json:"class"`
	Stop       goldenPoint   `json:"stop"`
	Terminals  []goldenPoint `json:"terminals"`
	Expect     string        `json:"expect"`
	ExpectBest *int          `json:"expect_best"`
}

func TestGoldenTripsScorepair(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden_trips.json"))
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
		if tc.Matcher != "scorepair" {
			skipped++
			continue
		}
		evaluated++
		class := model.DensityClass(tc.Class)
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
		wantVerified := tc.Expect == string(DecisionVerified)
		gotVerified := d == DecisionVerified
		switch {
		case wantVerified && gotVerified:
			tp++
		case !wantVerified && gotVerified:
			fp++
		case wantVerified && !gotVerified:
			fn++
		}
	}
	if evaluated == 0 {
		t.Fatal("no scorepair cases in golden_trips.json")
	}
	precision, recall := 0.0, 0.0
	if tp+fp > 0 {
		precision = float64(tp) / float64(tp+fp)
	}
	if tp+fn > 0 {
		recall = float64(tp) / float64(tp+fn)
	}
	t.Logf("scorepair golden: %d/%d exact, precision %.3f recall %.3f, skipped %d (4.2 validators)",
		correct, evaluated, precision, recall, skipped)
	if correct != evaluated {
		t.Fatalf("accuracy %d/%d, want 1.0", correct, evaluated)
	}
	if precision != 1 || recall != 1 {
		t.Fatalf("precision %.3f recall %.3f, want 1.0/1.0", precision, recall)
	}
}
