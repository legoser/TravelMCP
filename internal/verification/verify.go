package verification

import (
	"math"

	"travelmcp/internal/config"
	"travelmcp/internal/model"
	"travelmcp/internal/support/namesim"
)

type Candidate struct {
	Lat  float64
	Lon  float64
	Name string
}

type Result struct {
	Confidence float64
	Verified   bool
	Reason     string
}

func VerifyTerminal(a Candidate, b *Candidate, c *Candidate, d *Candidate, cfg config.Verification, density string) Result {
	threshold := cfg.ConfidenceThreshold
	distM := cfg.DistanceM
	strongM := cfg.StrongDistanceM
	levTh := cfg.LevThreshold
	if density != "" {
		if dt, ok := cfg.DensityThresholds[density]; ok {
			if dt.DistanceM != nil {
				distM = *dt.DistanceM
			}
			if dt.StrongDistanceM != nil {
				strongM = *dt.StrongDistanceM
			}
			if dt.LevThreshold != nil {
				levTh = *dt.LevThreshold
			}
			if dt.ConfidenceThreshold != nil {
				threshold = *dt.ConfidenceThreshold
			}
		}
	}
	conf := 0.0
	check := func(other *Candidate, weight float64) {
		if other == nil {
			return
		}
		d := haversineMeters(a.Lat, a.Lon, other.Lat, other.Lon)
		lev := normalizedLevenshtein(a.Name, other.Name)
		if d < float64(distM) && lev < levTh {
			strong := d < float64(strongM) && lev == 0
			add := weight
			if strong {
				add += 0.2
			}
			conf += add
		}
	}
	check(b, 0.4)
	check(c, 0.4)
	if d != nil {
		dist := haversineMeters(a.Lat, a.Lon, d.Lat, d.Lon)
		if dist < float64(distM) {
			conf += 0.2
		}
	}
	if conf > 1 {
		conf = 1
	}
	verified := conf >= threshold
	reason := "low confidence"
	if verified {
		reason = "verified"
	}
	return Result{Confidence: conf, Verified: verified, Reason: reason}
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * R * math.Asin(math.Sqrt(a))
}

func normalizedLevenshtein(a, b string) float64 { return namesim.NormalizedLevenshtein(a, b) }

func levenshtein(a, b string) int { return namesim.Levenshtein(a, b) }

func AdaptedToCandidates(rec model.AdaptedRecord) Candidate {
	lat, lon := 0.0, 0.0
	if rec.Lat != nil {
		lat = *rec.Lat
	}
	if rec.Lon != nil {
		lon = *rec.Lon
	}
	return Candidate{Lat: lat, Lon: lon, Name: rec.NameRu}
}
