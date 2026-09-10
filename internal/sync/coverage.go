package sync

import (
	"sort"

	"travelmcp/internal/model"
	"travelmcp/internal/skeleton"
	store "travelmcp/internal/store"
)

type RegistryStop struct {
	Name       string
	Region     string
	Settlement string
	Codes      []model.AdaptedIdentifier
	Lat, Lon   *float64
}

type RegionCoverage struct {
	Region  string
	Matched int
	Total   int
}

func (c RegionCoverage) Ratio() float64 {
	if c.Total == 0 {
		return 1
	}
	return float64(c.Matched) / float64(c.Total)
}

func MeasureCoverage(stops []RegistryStop, skeletons []store.SkeletonTerminalRow, softThreshold float64, cfg skeleton.JoinConfig) []RegionCoverage {
	skelAdapted := make([]model.AdaptedRecord, 0, len(skeletons))
	for _, s := range skeletons {
		skelAdapted = append(skelAdapted, skeletonTerminalAdapted(s))
	}
	byRegion := map[string]*RegionCoverage{}
	order := []string{}
	for _, stop := range stops {
		c, ok := byRegion[stop.Region]
		if !ok {
			c = &RegionCoverage{Region: stop.Region}
			byRegion[stop.Region] = c
			order = append(order, stop.Region)
		}
		c.Total++
		if coverageMatched(stopAdapted(stop), skelAdapted, softThreshold, cfg) {
			c.Matched++
		}
	}
	out := make([]RegionCoverage, 0, len(order))
	for _, r := range order {
		out = append(out, *byRegion[r])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Region < out[j].Region })
	return out
}

func stopAdapted(s RegistryStop) model.AdaptedRecord {
	r := model.AdaptedRecord{Kind: model.AdaptedTerminal, NameRu: s.Name}
	if s.Lat != nil && s.Lon != nil {
		r.Lat, r.Lon = s.Lat, s.Lon
	}
	if s.Settlement != "" {
		r.Extra = map[string]string{"settlement": s.Settlement}
	}
	if len(s.Codes) > 0 {
		r.Identifiers = append([]model.AdaptedIdentifier{}, s.Codes...)
	}
	return r
}

func coverageMatched(stop model.AdaptedRecord, skeletons []model.AdaptedRecord, softThreshold float64, cfg skeleton.JoinConfig) bool {
	best := 0.0
	for _, s := range skeletons {
		if sc := skeleton.PairScore(stop, s, cfg); sc > best {
			best = sc
		}
	}
	return best >= softThreshold
}

func ToSkeletonCoverages(in []RegionCoverage) []skeleton.Coverage {
	out := make([]skeleton.Coverage, 0, len(in))
	for _, c := range in {
		out = append(out, skeleton.Coverage{Region: c.Region, Matched: c.Matched, Total: c.Total})
	}
	return out
}
