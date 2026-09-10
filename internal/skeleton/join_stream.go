package skeleton

import (
	"math"

	"travelmcp/internal/geo"
	"travelmcp/internal/model"
	"travelmcp/internal/support/namesim"
)

// defaultPageSize — OSM-записей на страницу JoinPager (потолок совпадает
// с sync.skeleton_chunk_size: страница не больше чанка промоушена).
const defaultPageSize = 100

// JoinPager — пагинированный join OSM×Yandex: избегает O(N×M) на больших
// датасетах, разбивая обе стороны на гео-ячейки. Страница = до pageSize
// OSM-записей (левая сторона Join), проматченных против Yandex своей и
// соседних ячеек. Семантика повторяет Join: страница итерирует OSM-записи,
// каждая ищет лучшего свободного Yandex-кандидата.
type JoinPager struct {
	cfg      JoinConfig
	pageSize int
	yanCells map[[2]int][]int // ячейка → индексы yandex
	yandex   []model.AdaptedRecord
	yUsed    []bool
	osm      []model.AdaptedRecord
	cursor   int
	nameSim  map[[2]string]float64 // мемоизация nameSim (Левенштейн — узкое место)
}

// NewJoinPager строит индекс Yandex по гео-ячейкам (индексы, не записи:
// yUsed-флаги глобальны по индексу).
func NewJoinPager(osm, yandex []model.AdaptedRecord, cfg JoinConfig, pageSize int) *JoinPager {
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if cfg.Ambiguity == 0 {
		cfg.Ambiguity = defaultAmbiguity
	}
	p := &JoinPager{
		cfg:      cfg,
		pageSize: pageSize,
		yanCells: map[[2]int][]int{},
		yandex:   yandex,
		yUsed:    make([]bool, len(yandex)),
		osm:      osm,
		nameSim:  map[[2]string]float64{},
	}
	for i, y := range yandex {
		if y.HasCoords() {
			c := cellOf(*y.Lat, *y.Lon)
			p.yanCells[c] = append(p.yanCells[c], i)
		} else {
			p.yanCells[[2]int{math.MinInt32, math.MinInt32}] = append(p.yanCells[[2]int{math.MinInt32, math.MinInt32}], i)
		}
	}
	return p
}

func cellOf(lat, lon float64) [2]int {
	return [2]int{int(math.Floor(lat / geo.JoinCellSizeDeg)), int(math.Floor(lon / geo.JoinCellSizeDeg))}
}

// nearbyYandex возвращает индексы Yandex-кандидатов ячейки записи и соседних
// (±1) ячеек + все бескординатные. Порядок — как в исходном слайсе yandex
// (эквивалентно итерации Join), stable-выбор при равных score.
func (p *JoinPager) nearbyYandex(lat, lon float64) []int {
	base := cellOf(lat, lon)
	seen := map[int]bool{}
	var out []int
	push := func(idxs []int) {
		for _, i := range idxs {
			if !seen[i] {
				seen[i] = true
				out = append(out, i)
			}
		}
	}
	for di := -1; di <= 1; di++ {
		for dj := -1; dj <= 1; dj++ {
			push(p.yanCells[[2]int{base[0] + di, base[1] + dj}])
		}
	}
	push(p.yanCells[[2]int{math.MinInt32, math.MinInt32}])
	return out
}

// NextPage возвращает следующую страницу join-результата. Страница итерирует
// до pageSize OSM-записей: verified-матч потребляет Yandex-кандидата (yUsed),
// OSM без пары — IdentityOnly-канон (как в Join). ok=false — страницы
// закончились. Unverified-накопитель ведётся отдельно (FlushUnverified).
func (p *JoinPager) NextPage() (JoinOutcome, bool) {
	if p.cursor >= len(p.osm) {
		return JoinOutcome{}, false
	}
	end := p.cursor + p.pageSize
	if end > len(p.osm) {
		end = len(p.osm)
	}
	page := p.osm[p.cursor:end]
	p.cursor = end

	out := JoinOutcome{}
	for _, o := range page {
		var candIdx []int
		if o.HasCoords() {
			candIdx = p.nearbyYandex(*o.Lat, *o.Lon)
		} else {
			candIdx = p.allFreeYandex()
		}
		best := -1
		bestScore := 0.0
		second := 0.0
		// pre-filter: кандидат дальше GeoThresholdM*4 без код-матча не может
		// дать verified (hardGuard) и неинформативен для ambiguity —
		// отсекается до дорогого PairScore (9µs/вызов)
		candIdx = p.prefilter(o, candIdx)
		for _, j := range candIdx {
			if p.yUsed[j] {
				continue
			}
			s := p.pairScoreCached(o, p.yandex[j])
			if s > bestScore {
				second = bestScore
				bestScore = s
				best = j
			} else if s > second {
				second = s
			}
		}
		marginOK := bestScore-second >= p.cfg.Margin || second == 0
		verified := best >= 0 && bestScore >= p.cfg.Threshold && marginOK &&
			hardGuard(o, p.yandex[best], p.cfg)
		if verified {
			p.yUsed[best] = true
			out.Canon = append(out.Canon, JoinedRecord{
				Record:     enrichFrom(o, p.yandex[best]),
				Score:      bestScore,
				Enrichment: Enriched,
			})
			continue
		}
		ambiguous := best >= 0 && len(candIdx) > 1 &&
			bestScore >= p.cfg.Threshold-p.cfg.Margin &&
			bestScore-second < p.cfg.Ambiguity
		if ambiguous {
			p.yUsed[best] = true
			out.DuplicateAmbiguous = append(out.DuplicateAmbiguous, JoinedRecord{
				Record:     enrichFrom(o, p.yandex[best]),
				Score:      bestScore,
				Enrichment: IdentityOnly,
			})
			continue
		}
		out.Canon = append(out.Canon, JoinedRecord{Record: o, Score: bestScore, Enrichment: IdentityOnly})
	}
	return out, true
}

// pairScoreCached — PairScore с мемоизацией nameSim по паре имён: в плотных
// городах одни и те же пары имён оцениваются многократно, Левенштейн —
// доминирующая стоимость. Формула — общая scorePairBody (D-5), здесь только
// кэширующая обёртка над nameSim.
func (p *JoinPager) pairScoreCached(a, b model.AdaptedRecord) float64 {
	return scorePairBody(a, b, p.cfg, func(x, y string) float64 {
		key := [2]string{x, y}
		if v, ok := p.nameSim[key]; ok {
			return v
		}
		v := namesim.NormalizedSimilarity(x, y)
		p.nameSim[key] = v
		return v
	})
}

// prefilter отсекает кандидатов дальше JoinGeoWindowM без код-матча: быстрая
// планарная оценка (degrees → метры по широте), haversine остаётся внутри
// PairScore. Код-матч проходит окно всегда (тёзка за 79км — легитимный
// identity-сигнал). Бескординатные кандидаты проходят всегда.
func (p *JoinPager) prefilter(o model.AdaptedRecord, candIdx []int) []int {
	if !o.HasCoords() {
		return candIdx
	}
	oLat, oLon := *o.Lat, *o.Lon
	out := candIdx[:0]
	for _, j := range candIdx {
		y := p.yandex[j]
		if !y.HasCoords() {
			out = append(out, j)
			continue
		}
		dLat := (*y.Lat - oLat) * geo.MetersPerDegree
		dLon := (*y.Lon - oLon) * geo.MetersPerDegree * geo.CosLatApprox(oLat)
		if dLat*dLat+dLon*dLon <= geo.JoinGeoWindowM*geo.JoinGeoWindowM {
			out = append(out, j)
			continue
		}
		if codesMatch(o, y) {
			out = append(out, j)
		}
	}
	return out
}

func (p *JoinPager) allFreeYandex() []int {
	out := make([]int, 0, len(p.yandex))
	for i := range p.yandex {
		if !p.yUsed[i] {
			out = append(out, i)
		}
	}
	return out
}

// FlushUnverified возвращает Yandex-записи, не потреблённые ни одной
// страницей (глобальные Unverified по семантике Join). Вызывать после
// исчерпания NextPage; детерминированный порядок.
func (p *JoinPager) FlushUnverified() []model.AdaptedRecord {
	out := make([]model.AdaptedRecord, 0)
	for i, y := range p.yandex {
		if !p.yUsed[i] {
			out = append(out, y)
		}
	}
	return out
}
