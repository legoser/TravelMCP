package sync

import (
	"fmt"
	"sort"

	"travelmcp/internal/model"
	"travelmcp/internal/verification"
)

type ThresholdPoint struct {
	Threshold    float64 `json:"threshold"`
	Verified     int     `json:"verified"`
	MarginBand   int     `json:"margin_band"`
	Rejected     int     `json:"rejected"`
	Total        int     `json:"total"`
	VerifiedRate float64 `json:"verified_rate"`
}

type CalibrationReport struct {
	Thresholds  []float64        `json:"thresholds"`
	Margin      float64          `json:"margin"`
	Clean       []ThresholdPoint `json:"clean"`
	Noisy       []ThresholdPoint `json:"noisy"`
	NoisyShiftM float64          `json:"noisy_shift_m"`
}

func bestAttachScores(
	stops []RegistryStop,
	terms []AttachTerminal,
	paramsFor func(model.DensityClass) verification.Params,
	classFor func(string) model.DensityClass,
	source string,
) []float64 {
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
	best := make([]float64, 0, len(stops))
	for _, stop := range stops {
		item := PairItemFromStop(stop.Name, stop.Lat, stop.Lon, stop.Settlement, source)
		class := classFor(stop.Region)
		top := 0.0
		for _, cand := range cands {
			if sc := verification.ScorePair(item, cand, class, paramsFor(class)); sc.Value > top {
				top = sc.Value
			}
		}
		best = append(best, top)
	}
	return best
}

func SweepAttachThreshold(
	stops []RegistryStop,
	terms []AttachTerminal,
	thresholds []float64,
	margin float64,
	paramsFor func(model.DensityClass) verification.Params,
	classFor func(string) model.DensityClass,
	source string,
) []ThresholdPoint {
	sorted := append([]float64{}, thresholds...)
	sort.Float64s(sorted)
	best := bestAttachScores(stops, terms, paramsFor, classFor, source)
	out := make([]ThresholdPoint, 0, len(sorted))
	for _, thr := range sorted {
		p := ThresholdPoint{Threshold: thr, Total: len(best)}
		for _, b := range best {
			switch {
			case b >= thr:
				p.Verified++
			case b >= thr-margin:
				p.MarginBand++
			default:
				p.Rejected++
			}
		}
		if p.Total > 0 {
			p.VerifiedRate = float64(p.Verified) / float64(p.Total)
		}
		out = append(out, p)
	}
	return out
}

func NoisyTerms(terms []AttachTerminal, dLat, dLon float64) []AttachTerminal {
	out := make([]AttachTerminal, 0, len(terms))
	for _, t := range terms {
		cp := t
		if cp.Lat != nil {
			v := *cp.Lat + dLat
			cp.Lat = &v
		}
		if cp.Lon != nil {
			v := *cp.Lon + dLon
			cp.Lon = &v
		}
		out = append(out, cp)
	}
	return out
}

func CalibrateAttach(
	stops []RegistryStop,
	terms []AttachTerminal,
	thresholds []float64,
	margin float64,
	noisyShiftDeg float64,
	paramsFor func(model.DensityClass) verification.Params,
	classFor func(string) model.DensityClass,
	source string,
) CalibrationReport {
	return CalibrationReport{
		Thresholds:  append([]float64{}, thresholds...),
		Margin:      margin,
		Clean:       SweepAttachThreshold(stops, terms, thresholds, margin, paramsFor, classFor, source),
		Noisy:       SweepAttachThreshold(stops, NoisyTerms(terms, noisyShiftDeg, noisyShiftDeg), thresholds, margin, paramsFor, classFor, source),
		NoisyShiftM: noisyShiftDeg * 111000,
	}
}

func RecommendThreshold(rep CalibrationReport, targetNoisyRate float64) (float64, string) {
	best := -1.0
	for _, p := range rep.Noisy {
		if p.VerifiedRate >= targetNoisyRate && p.Threshold > best {
			best = p.Threshold
		}
	}
	if best < 0 {
		last := rep.Noisy[len(rep.Noisy)-1]
		return rep.Thresholds[0], fmt.Sprintf("ни один порог не держит noisy-rate %.2f (максимум %.2f на пороге %.2f): взят минимальный порог %.2f, требуется ручная калибровка",
			targetNoisyRate, last.VerifiedRate, last.Threshold, rep.Thresholds[0])
	}
	return best, fmt.Sprintf("максимальный порог с noisy-rate (сдвиг %.0fм) >= %.2f", rep.NoisyShiftM, targetNoisyRate)
}
