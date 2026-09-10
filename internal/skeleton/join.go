package skeleton

import (
	"sort"
	"strings"

	"travelmcp/internal/model"
	"travelmcp/internal/support/namesim"
)

type JoinConfig struct {
	GeoThresholdM float64
	Threshold     float64
	Margin        float64
	Ambiguity     float64
}

// Дефолты join-порогов (= verification.* в конфиге: distance_m,
// confidence_threshold, score_margin, score_ambiguity). Явные значения
// JoinConfig имеют приоритет; applyDefaults гарантирует те же числа
// в config.Verification.
const (
	defaultGeoThresholdM = 500.0
	defaultThreshold     = 0.6
	defaultMargin        = 0.1
	defaultAmbiguity     = 0.05
)

func DefaultJoinConfig() JoinConfig {
	return JoinConfig{
		GeoThresholdM: defaultGeoThresholdM,
		Threshold:     defaultThreshold,
		Margin:        defaultMargin,
		Ambiguity:     defaultAmbiguity,
	}
}

func PairScore(a, b model.AdaptedRecord, cfg JoinConfig) float64 {
	return scorePairBody(a, b, cfg, namesim.NormalizedSimilarity)
}

// scorePairBody — единая формула скоринга пары OSM×Yandex (D-5): одна
// реализация для PairScore (прямой вызов) и pairScoreCached (мемоизация
// nameSim в JoinPager). nameSimFn — инжектируемое сравнение имён.
func scorePairBody(a, b model.AdaptedRecord, cfg JoinConfig, nameSimFn func(string, string) float64) float64 {
	if codesMatch(a, b) {
		return 1
	}
	nameSim := nameSimFn(a.NameRu, b.NameRu)
	geom := 0.0
	geomPresent := a.HasCoords() && b.HasCoords()
	if geomPresent {
		d := haversineM(*a.Lat, *a.Lon, *b.Lat, *b.Lon)
		switch {
		case d <= cfg.GeoThresholdM/5:
			geom = 1
		case d <= cfg.GeoThresholdM:
			geom = 0.6
		case d <= cfg.GeoThresholdM*4:
			geom = 0.25
		}
	}
	as, bs := extra(a, "settlement"), extra(b, "settlement")
	settlement := 0.0
	settlementPresent := as != "" && bs != ""
	if settlementPresent && namesim.SameSettlement(as, bs) {
		settlement = 1
	}
	at, bt := extra(a, "transport_type"), extra(b, "transport_type")
	transport := 0.0
	transportPresent := at != "" && bt != ""
	if transportPresent && transportCompatible(at, bt) {
		transport = 1
	}
	type fw struct {
		w       float64
		v       float64
		present bool
	}
	feats := []fw{
		{0.45, nameSim, true},
		{0.3, geom, geomPresent},
		{0.15, settlement, settlementPresent},
		{0.1, transport, transportPresent},
	}
	sumW := 0.0
	for _, f := range feats {
		if f.present {
			sumW += f.w
		}
	}
	if sumW == 0 {
		return 0
	}
	score := 0.0
	for _, f := range feats {
		if f.present {
			score += f.w / sumW * f.v
		}
	}
	return score
}

func codesMatch(a, b model.AdaptedRecord) bool {
	for _, x := range a.Identifiers {
		if x.Code == "" {
			continue
		}
		for _, y := range b.Identifiers {
			if x.Code == y.Code && (x.CodeType == y.CodeType || x.System == y.System) {
				return true
			}
		}
	}
	return false
}

func transportCompatible(a, b string) bool {
	for _, x := range splitPlus(a) {
		for _, y := range splitPlus(b) {
			if x == y {
				return true
			}
		}
	}
	return false
}

func splitPlus(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '+' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}

func normalizeTransport(combined string) string {
	seen := map[string]bool{}
	var parts []string
	for _, p := range splitPlus(combined) {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		parts = append(parts, p)
	}
	sort.Strings(parts)
	return strings.Join(parts, "+")
}

func Join(osm, yandex []model.AdaptedRecord, cfg JoinConfig) JoinOutcome {
	used := make([]bool, len(yandex))
	var out JoinOutcome
	for _, o := range osm {
		best := -1
		bestScore := 0.0
		second := 0.0
		for j, y := range yandex {
			if used[j] {
				continue
			}
			s := PairScore(o, y, cfg)
			if s > bestScore {
				second = bestScore
				bestScore = s
				best = j
			} else if s > second {
				second = s
			}
		}
		marginOK := bestScore-second >= cfg.Margin || second == 0
		verified := best >= 0 && bestScore >= cfg.Threshold && marginOK &&
			hardGuard(o, yandex[best], cfg)
		if verified {
			used[best] = true
			out.Canon = append(out.Canon, JoinedRecord{
				Record:     enrichFrom(o, yandex[best]),
				Score:      bestScore,
				Enrichment: Enriched,
			})
			continue
		}
		ambiguous := best >= 0 && len(yandex) > 1 &&
			bestScore >= cfg.Threshold-cfg.Margin &&
			bestScore-second < cfg.Ambiguity
		if ambiguous {
			used[best] = true
			out.DuplicateAmbiguous = append(out.DuplicateAmbiguous, JoinedRecord{
				Record:     enrichFrom(o, yandex[best]),
				Score:      bestScore,
				Enrichment: IdentityOnly,
			})
			continue
		}
		out.Canon = append(out.Canon, JoinedRecord{Record: o, Score: bestScore, Enrichment: IdentityOnly})
	}
	for j, y := range yandex {
		if !used[j] {
			out.Unverified = append(out.Unverified, y)
		}
	}
	return out
}

func enrichFrom(o, y model.AdaptedRecord) model.AdaptedRecord {
	merged := o
	if y.NameRu != "" && y.NameRu != o.NameRu {
		merged = withExtra(merged, "yandex_title", y.NameRu)
	}
	if extra(merged, "settlement") == "" {
		merged = withExtra(merged, "settlement", extra(y, "settlement"))
	}
	if extra(merged, "region") == "" {
		merged = withExtra(merged, "region", extra(y, "region"))
	}
	if extra(merged, "transport_type") == "" {
		merged = withExtra(merged, "transport_type", extra(y, "transport_type"))
	}
	seen := map[string]bool{}
	for _, id := range merged.Identifiers {
		seen[id.System+"|"+id.CodeType+"|"+id.Code] = true
	}
	for _, id := range y.Identifiers {
		k := id.System + "|" + id.CodeType + "|" + id.Code
		if !seen[k] {
			merged.Identifiers = append(merged.Identifiers, id)
		}
	}
	return merged
}

func hardGuard(a, b model.AdaptedRecord, cfg JoinConfig) bool {
	if codesMatch(a, b) {
		return true
	}
	if a.HasCoords() && b.HasCoords() {
		return haversineM(*a.Lat, *a.Lon, *b.Lat, *b.Lon) <= cfg.GeoThresholdM
	}
	return false
}
