package skeleton

type SeqStop struct {
	Name     string
	Seq      int
	Lat, Lon float64
	Matched  bool
	Duration float64
	Distance float64
}

type Interpolated struct {
	Lat, Lon   float64
	Confidence float64
	Origin     string
	Method     string
}

func InterpolatePosition(stops []SeqStop, idx int) (Interpolated, bool) {
	if idx < 0 || idx >= len(stops) || stops[idx].Matched {
		return Interpolated{}, false
	}
	prev := -1
	for i := idx - 1; i >= 0; i-- {
		if stops[i].Matched {
			prev = i
			break
		}
	}
	next := -1
	for i := idx + 1; i < len(stops); i++ {
		if stops[i].Matched {
			next = i
			break
		}
	}
	if prev < 0 || next < 0 {
		return Interpolated{}, false
	}
	share := seqShare(stops, prev, idx, next)
	lat := stops[prev].Lat + (stops[next].Lat-stops[prev].Lat)*share
	lon := stops[prev].Lon + (stops[next].Lon-stops[prev].Lon)*share
	legKm := haversineM(stops[prev].Lat, stops[prev].Lon, stops[next].Lat, stops[next].Lon) / 1000
	// Доверие интерполяции (origin='seed', ниже verified-порога 0.6):
	// долевой метод точнее пропорционального; длинные перегоны
	// штрафуются — интерполяция на 50+ км почти неинформативна.
	const (
		seqProportionalConf = 0.3
		shareMethodConf     = 0.45
		longLegKm           = 50.0
		longLegPenalty      = 0.6
	)
	conf := seqProportionalConf
	method := "seq_proportional"
	if hasWeights(stops, prev, next) {
		method = "duration_distance_share"
		conf = shareMethodConf
	}
	if legKm > longLegKm {
		conf *= longLegPenalty
	}
	return Interpolated{Lat: lat, Lon: lon, Confidence: conf, Origin: "seed", Method: method}, true
}

func seqShare(stops []SeqStop, prev, idx, next int) float64 {
	if hasWeights(stops, prev, next) {
		total := 0.0
		for i := prev; i < next; i++ {
			total += stopWeight(stops[i])
		}
		if total > 0 {
			acc := 0.0
			for i := prev; i < idx; i++ {
				acc += stopWeight(stops[i])
			}
			acc += stopWeight(stops[idx]) / 2
			return acc / total
		}
	}
	span := float64(next - prev)
	if span <= 0 {
		return 0.5
	}
	return float64(idx-prev) / span
}

func stopWeight(s SeqStop) float64 {
	if s.Duration > 0 {
		return s.Duration
	}
	return s.Distance
}

func hasWeights(stops []SeqStop, prev, next int) bool {
	for i := prev; i <= next; i++ {
		if stops[i].Duration > 0 || stops[i].Distance > 0 {
			return true
		}
	}
	return false
}

type Coverage struct {
	Region  string
	Matched int
	Total   int
}

func (c Coverage) Ratio() float64 {
	if c.Total == 0 {
		return 1
	}
	return float64(c.Matched) / float64(c.Total)
}

func CoverageByRegion(stops []SeqStop, regionOf func(SeqStop) string) []Coverage {
	byRegion := map[string]*Coverage{}
	order := []string{}
	for _, s := range stops {
		r := regionOf(s)
		c, ok := byRegion[r]
		if !ok {
			c = &Coverage{Region: r}
			byRegion[r] = c
			order = append(order, r)
		}
		c.Total++
		if s.Matched {
			c.Matched++
		}
	}
	out := make([]Coverage, 0, len(order))
	for _, r := range order {
		out = append(out, *byRegion[r])
	}
	return out
}

func GatePass(coverages []Coverage, gate float64) (bool, []string) {
	var blocked []string
	for _, c := range coverages {
		if c.Ratio() < gate {
			blocked = append(blocked, c.Region)
		}
	}
	return len(blocked) == 0, blocked
}
