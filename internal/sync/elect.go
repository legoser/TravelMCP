package sync

import "time"

type AttributeCandidate struct {
	Field      string
	Value      string
	Source     string
	Confidence float64
	ObservedAt time.Time
	ActorID    *int64
	Origin     string
}

func IsSkeletonSource(source string) bool {
	return source == "osm" || source == "motis"
}

func ElectField(cands []AttributeCandidate) (AttributeCandidate, bool) {
	if len(cands) == 0 {
		return AttributeCandidate{}, false
	}
	for _, c := range cands {
		if c.ActorID != nil {
			return c, true
		}
	}
	live := make([]AttributeCandidate, 0, len(cands))
	for _, c := range cands {
		if c.Origin != "seed" {
			live = append(live, c)
		}
	}
	pool := live
	if len(pool) == 0 {
		pool = cands
	}
	best := pool[0]
	bestScore := scoreOf(best)
	for _, c := range pool[1:] {
		s := scoreOf(c)
		if s > bestScore || (s == bestScore && c.ObservedAt.After(best.ObservedAt)) {
			best, bestScore = c, s
		}
	}
	return best, true
}

func scoreOf(c AttributeCandidate) float64 {
	s := c.Confidence
	if c.Field == "geom" && IsSkeletonSource(c.Source) {
		s += 0.15
		if s > 1 {
			s = 1
		}
	}
	return s
}

type LegacyMatchResult struct {
	Matched      bool
	Score        float64
	ReviewReason string
}

func MatchLegacy(score, threshold float64) LegacyMatchResult {
	if score >= threshold {
		return LegacyMatchResult{Matched: true, Score: score}
	}
	return LegacyMatchResult{Matched: false, Score: score, ReviewReason: "legacy_unmatched"}
}
