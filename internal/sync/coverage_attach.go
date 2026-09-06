package sync

import (
	"sort"

	"travelmcp/internal/model"
	"travelmcp/internal/verification"
)

func MeasureAttachCoverage(
	stops []RegistryStop,
	terms []AttachTerminal,
	softThreshold float64,
	paramsFor func(model.DensityClass) verification.Params,
	classFor func(string) model.DensityClass,
	source string,
) []RegionCoverage {
	if classFor == nil {
		classFor = func(string) model.DensityClass { return model.DensityUrban }
	}
	if source == "" {
		source = "mintrans"
	}
	cands := make([]verification.PairItem, 0, len(terms))
	for _, t := range terms {
		cands = append(cands, PairItemFromTerminal(t))
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
		item := PairItemFromStop(stop.Name, stop.Lat, stop.Lon, stop.Settlement, source)
		class := classFor(stop.Region)
		best := 0.0
		for _, cand := range cands {
			if sc := verification.ScorePair(item, cand, class, paramsFor(class)); sc.Value > best {
				best = sc.Value
			}
		}
		if best >= softThreshold {
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

func GatePassRegions(cov []RegionCoverage, gate float64) (bool, []string) {
	var blocked []string
	for _, c := range cov {
		if c.Ratio() < gate {
			blocked = append(blocked, c.Region)
		}
	}
	sort.Strings(blocked)
	return len(blocked) == 0, blocked
}
