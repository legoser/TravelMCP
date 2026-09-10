package verification

import (
	"math"
	"strings"

	"travelmcp/internal/config"
	"travelmcp/internal/geo"
	"travelmcp/internal/model"
	"travelmcp/internal/support/namesim"
)

type Task string

const (
	TaskSkeletonOSM  Task = "skeleton_osm"
	TaskStopTerminal Task = "stop_terminal"
	TaskMerge        Task = "merge"
	TaskDedup        Task = "dedup"
	TaskLegacyMatch  Task = "legacy_match"
)

type Decision string

const (
	DecisionVerified           Decision = "verified"
	DecisionLowConfidence      Decision = "low_confidence"
	DecisionDuplicateAmbiguous Decision = "duplicate_ambiguous"
	DecisionRejected           Decision = "rejected"
)

func (d Decision) ReviewReason() string {
	switch d {
	case DecisionLowConfidence:
		return "low_confidence"
	case DecisionDuplicateAmbiguous:
		return "duplicate_ambiguous"
	default:
		return ""
	}
}

type PairItem struct {
	Name       string
	Lat        *float64
	Lon        *float64
	Transport  string
	Settlement string
	Codes      []model.AdaptedIdentifier
	Source     string
}

type Params struct {
	WeightName       float64
	WeightGeom       float64
	WeightSettlement float64
	WeightTransport  float64
	Threshold        float64
	Margin           float64
	Ambiguity        float64
	GeoThresholdM    float64
}

// Веса фичей и дефолты порогов ScorePair. Пороговые дефолты повторяют
// verification.* конфига (confidence_threshold, score_margin,
// score_ambiguity, distance_m); явные значения Params имеют приоритет.
// Веса (name 0.45 / geom 0.3 / settlement 0.15 / transport 0.1) —
// калибровочные, меняются только с прогоном калибровки (calibrate.go).
const (
	defaultWeightName       = 0.45
	defaultWeightGeom       = 0.3
	defaultWeightSettlement = 0.15
	defaultWeightTransport  = 0.1
	defaultThreshold        = 0.6
	defaultMargin           = 0.1
	defaultAmbiguity        = 0.05
	defaultGeoThresholdM    = 500.0
)

// Доверие источникам (множитель Trust): seed/shadow-источники голосуют
// слабо, ручные правки и консенсус — в полную силу.
const (
	trustSeed       = 0.4
	trustManual     = 1.0
	trustLegacy     = 0.7
	trustSameSource = 0.6
	trustDefault    = 1.0
)

// nonUrbanNameGate — порог nameSim для негородского guard без геометрии
// (= verification.name_similarity).
const nonUrbanNameGate = 0.8

// Полосы geom-фичи: ближняя (≤threshold/nearBand → 1), средняя
// (≤threshold → midScore), дальняя (≤threshold·farBand → farScore).
const (
	geomNearBand = 5.0
	geomFarBand  = 4.0
	geomMidScore = 0.6
	geomFarScore = 0.25
)

func DefaultStopTerminalParams(cfg config.Verification, class model.DensityClass) Params {
	p := Params{
		WeightName:       defaultWeightName,
		WeightGeom:       defaultWeightGeom,
		WeightSettlement: defaultWeightSettlement,
		WeightTransport:  defaultWeightTransport,
		Threshold:        cfg.ConfidenceThreshold,
		Margin:           cfg.ScoreMargin,
		Ambiguity:        cfg.ScoreAmbiguity,
		GeoThresholdM:    float64(cfg.DistanceM),
	}
	if p.Threshold == 0 {
		p.Threshold = defaultThreshold
	}
	if p.Margin == 0 {
		p.Margin = defaultMargin
	}
	if p.Ambiguity == 0 {
		p.Ambiguity = defaultAmbiguity
	}
	if p.GeoThresholdM == 0 {
		p.GeoThresholdM = defaultGeoThresholdM
	}
	if dt, ok := cfg.DensityThresholds[string(class)]; ok {
		if dt.DistanceM != nil {
			p.GeoThresholdM = float64(*dt.DistanceM)
		}
		if dt.ConfidenceThreshold != nil {
			p.Threshold = *dt.ConfidenceThreshold
		}
	}
	return p
}

func voiceOf(s string) string {
	s = normSource(s)
	if s == "motis" {
		return "osm"
	}
	return s
}

func normSource(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "unknown"
	}
	return s
}

func Trust(a, b string) float64 {
	na, nb := voiceOf(a), voiceOf(b)
	if na == "seed" || nb == "seed" {
		return trustSeed
	}
	if na == "manual" || nb == "manual" {
		return trustManual
	}
	if na == "legacy" || nb == "legacy" {
		return trustLegacy
	}
	if na == nb {
		return trustSameSource
	}
	return trustDefault
}

type PairScore struct {
	Value     float64
	Trust     float64
	CodeMatch bool
	HasGeom   bool
	DistanceM float64
	NameSim   float64
	GuardOK   bool
}

func codesEqual(a, b []model.AdaptedIdentifier) bool {
	for _, x := range a {
		if x.Code == "" {
			continue
		}
		for _, y := range b {
			if x.Code == y.Code && (x.CodeType == y.CodeType || x.System == y.System) {
				return true
			}
		}
	}
	return false
}

func geomFeature(a, b PairItem, geoThresholdM float64) (float64, bool, float64) {
	if a.Lat == nil || a.Lon == nil || b.Lat == nil || b.Lon == nil {
		return 0, false, 0
	}
	d := geo.HaversineM(*a.Lat, *a.Lon, *b.Lat, *b.Lon)
	switch {
	case d <= geoThresholdM/geomNearBand:
		return 1, true, d
	case d <= geoThresholdM:
		return geomMidScore, true, d
	case d <= geoThresholdM*geomFarBand:
		return geomFarScore, true, d
	default:
		return 0, true, d
	}
}

func settlementFeature(a, b PairItem) (float64, bool) {
	if a.Settlement == "" || b.Settlement == "" {
		return 0, false
	}
	if namesim.SameSettlement(a.Settlement, b.Settlement) {
		return 1, true
	}
	return 0, true
}

func transportFeature(a, b PairItem) (float64, bool) {
	if a.Transport == "" || b.Transport == "" {
		return 0, false
	}
	for _, x := range strings.Split(a.Transport, "+") {
		for _, y := range strings.Split(b.Transport, "+") {
			if x != "" && x == y {
				return 1, true
			}
		}
	}
	return 0, true
}

func isNonUrban(class model.DensityClass) bool {
	return class == model.DensityRural || class == model.DensitySuburban
}

func ScorePair(a, b PairItem, class model.DensityClass, p Params) PairScore {
	t := Trust(a.Source, b.Source)
	if codesEqual(a.Codes, b.Codes) {
		return PairScore{Value: t, Trust: t, CodeMatch: true, GuardOK: true, NameSim: namesim.Similarity(a.Name, b.Name)}
	}
	nameSim := namesim.Similarity(a.Name, b.Name)
	geom, hasGeom, dist := geomFeature(a, b, p.GeoThresholdM)
	settle, hasSettle := settlementFeature(a, b)
	trans, hasTrans := transportFeature(a, b)
	type fw struct {
		w       float64
		v       float64
		present bool
	}
	feats := []fw{
		{p.WeightName, nameSim, true},
		{p.WeightGeom, geom, hasGeom},
		{p.WeightSettlement, settle, hasSettle},
		{p.WeightTransport, trans, hasTrans},
	}
	sumW := 0.0
	for _, f := range feats {
		if f.present {
			sumW += f.w
		}
	}
	weighted := 0.0
	if sumW > 0 {
		for _, f := range feats {
			if f.present {
				weighted += f.w / sumW * f.v
			}
		}
	}
	v := t * weighted
	guard := false
	switch {
	case hasGeom && dist <= p.GeoThresholdM:
		guard = true
	case isNonUrban(class) && nameSim >= nonUrbanNameGate && hasSettle && settle == 1 && t >= 1:
		guard = true
	}
	return PairScore{Value: v, Trust: t, HasGeom: hasGeom, DistanceM: dist, NameSim: nameSim, GuardOK: guard}
}

func decideBest(s PairScore, p Params, marginOK bool) Decision {
	if s.Value >= p.Threshold && s.GuardOK && marginOK {
		return DecisionVerified
	}
	if s.Value >= p.Threshold-p.Margin {
		return DecisionLowConfidence
	}
	return DecisionRejected
}

func MatchStopToTerminal(stop PairItem, terms []PairItem, class model.DensityClass, p Params) (int, Decision, PairScore) {
	if len(terms) == 0 {
		return -1, DecisionRejected, PairScore{}
	}
	type ranked struct {
		idx int
		s   PairScore
	}
	all := make([]ranked, 0, len(terms))
	for i, t := range terms {
		all = append(all, ranked{i, ScorePair(stop, t, class, p)})
	}
	top := 0
	for i := range all {
		if all[i].s.Value > all[top].s.Value {
			top = i
		}
	}
	best := all[top]
	second := math.Inf(1)
	for i := range all {
		if i == top {
			continue
		}
		if all[i].s.Value < second {
			second = all[i].s.Value
		}
	}
	gap := math.Inf(1)
	if !math.IsInf(second, 1) {
		gap = best.s.Value - second
	}
	if best.s.Value >= p.Threshold-p.Margin && len(all) > 1 && gap < p.Ambiguity {
		return best.idx, DecisionDuplicateAmbiguous, best.s
	}
	marginOK := math.IsInf(gap, 1) || gap >= p.Ambiguity
	if !best.s.CodeMatch && !best.s.HasGeom && isNonUrban(class) {
		return best.idx, decideBest(best.s, p, marginOK), best.s
	}
	return best.idx, decideBest(best.s, p, true), best.s
}
