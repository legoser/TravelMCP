package geo

import (
	"context"
	"fmt"
	"sync"

	"travelmcp/internal/geocoder"
	"travelmcp/internal/support/namesim"
)

// RegionNames — коды регионов реестра Минтранса (собственная нумерация) → названия.
var RegionNames = map[string]string{
	"01": "Республика Адыгея", "02": "Республика Башкортостан", "03": "Республика Бурятия",
	"04": "Республика Altai", "05": "Республика Дагестан", "06": "Республика Ингушетия",
	"07": "Кабардино-Балкарская Республика", "08": "Республика Калмыкия",
	"09": "Карачаево-Черкесская Республика", "10": "Республика Карелия",
	"11": "Республика Коми", "12": "Республика Марий Эл", "13": "Республика Мордовия",
	"15": "Республика Северная Осетия — Алания", "16": "Республика Татарстан",
	"17": "Республика Тыва", "18": "Удмуртская Республика", "19": "Республика Хакасия",
	"20": "Чеченская Республика", "21": "Чувашская Республика",
	"22": "Алтайский край", "23": "Краснодарский край", "24": "Красноярский край",
	"25": "Приморский край", "26": "Ставропольский край", "27": "Хабаровский край",
	"28": "Амурская область", "29": "Архангельская область", "30": "Астраханская область",
	"31": "Белгородская область", "32": "Брянская область", "33": "Владимирская область",
	"34": "Волгоградская область", "35": "Вологодская область", "36": "Воронежская область",
	"37": "Ивановская область", "38": "Иркутская область", "40": "Калужская область",
	"42": "Кемеровская область", "43": "Кировская область", "44": "Костромская область",
	"45": "Курганская область", "46": "Курская область", "47": "Ленинградская область",
	"48": "Липецкая область", "50": "Московская область", "52": "Нижегородская область",
	"53": "Новгородская область", "54": "Новосибирская область", "55": "Омская область",
	"56": "Оренбургская область", "57": "Орловская область", "58": "Пензенская область",
	"59": "Пермский край", "60": "Псковская область", "61": "Ростовская область",
	"62": "Рязанская область", "63": "Самарская область", "64": "Саратовская область",
	"66": "Свердловская область", "67": "Смоленская область", "68": "Тамбовская область",
	"69": "Тверская область", "70": "Томская область", "71": "Тульская область",
	"72": "Тюменская область", "73": "Ульяновская область", "74": "Челябинская область",
	"75": "Забайкальский край", "76": "Ярославская область",
	"78": "Санкт-Петербург", "79": "Еврейская автономная область", "77": "Москва",
	"86": "Ханты-Мансийский АО — Югра", "90": "Запорожская область",
	"91": "Республика Крым", "92": "Севастополь",
	"93": "Донецкая Народная Республика", "94": "Луганская Народная Республика",
	"95": "Херсонская область",
}

func RegionName(code string) string { return RegionNames[code] }

type GeoResolver struct {
	multi     geocoder.Geocoder
	limit     int
	threshold float64
	maxCalls  int
	mu        sync.Mutex
	apiCalls  int
	cache     map[string]resolveResult
}

type resolveResult struct {
	lat, lon float64
	source   string
	conf     float64
	ok       bool
}

func NewGeoResolver(g geocoder.Geocoder, limit int, threshold float64, maxCalls int) *GeoResolver {
	if limit <= 0 {
		limit = 5
	}
	if threshold <= 0 {
		threshold = 0.8
	}
	return &GeoResolver{multi: g, limit: limit, threshold: threshold, maxCalls: maxCalls, cache: map[string]resolveResult{}}
}

// Resolve находит координаты для стопа по имени и коду региона. Топоним извлекается из
// имени («г. X», «пгт X», «п. X» …). Запрос к API строится из полного имени с раскрытыми
// сокращениями («ОП г. Бердск» → «Россия, <регион>, остановочный пункт г. Бердск»);
// при промахе — фолбэк на «Россия, <регион>, <топоним>». Результат проходит name-gate
// (сходство с топонимом). Координаты даёт только API; places.json как источник не
// используется. Без топонима API не вызывается (имя объекта «ОП «ЛПК»» вернёт мусор).
func (r *GeoResolver) Resolve(ctx context.Context, name, region string) (float64, float64, string, float64, bool) {
	if r == nil || name == "" {
		return 0, 0, "", 0, false
	}
	settlement := namesim.ExtractSettlement(name)
	if settlement == "" {
		return 0, 0, "", 0, false
	}
	key := region + "|" + namesim.Normalize(settlement)
	if cr, ok := r.cached(key); ok {
		return cr.lat, cr.lon, cr.source, cr.conf, cr.ok
	}
	if !r.canCall() {
		return 0, 0, "", 0, false
	}
	queries := []string{
		buildGeocodeQuery(region, namesim.ExpandAbbreviations(name)),
		buildGeocodeQuery(region, settlement),
	}
	var res resolveResult
	for _, query := range queries {
		if res = r.apiGeocode(ctx, query, settlement); res.ok {
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	r.store(key, res)
	return res.lat, res.lon, res.source, res.conf, res.ok
}

func (r *GeoResolver) cached(key string) (resolveResult, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cr, ok := r.cache[key]
	return cr, ok
}

func (r *GeoResolver) store(key string, cr resolveResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[key] = cr
}

func (r *GeoResolver) canCall() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxCalls > 0 && r.apiCalls >= r.maxCalls {
		return false
	}
	r.apiCalls++
	return true
}

func (r *GeoResolver) apiGeocode(ctx context.Context, query, expect string) resolveResult {
	var candidates []geocoder.Candidate
	if r.multi != nil {
		if m, ok := r.multi.(geocoder.MultiGeocoder); ok {
			cands, err := m.GeocodeCandidates(ctx, query, r.limit)
			if err == nil {
				candidates = cands
			}
		} else if res, err := r.multi.Geocode(ctx, query); err == nil && res != nil {
			candidates = []geocoder.Candidate{{Lat: res.Lat, Lon: res.Lon, Name: res.Name}}
		}
	}
	bestIdx := -1
	bestSim := 0.0
	for i, c := range candidates {
		sim := namesim.Similarity(expect, c.Name)
		if sim > bestSim {
			bestSim = sim
			bestIdx = i
		}
	}
	if bestIdx >= 0 && bestSim >= r.threshold {
		best := candidates[bestIdx]
		src := best.Provider
		if src == "" {
			src = "geocode"
		}
		return resolveResult{lat: best.Lat, lon: best.Lon, source: src, conf: bestSim, ok: true}
	}
	return resolveResult{ok: false}
}

func buildGeocodeQuery(region, settlement string) string {
	regionName := RegionName(region)
	if regionName != "" {
		return fmt.Sprintf("Россия, %s, %s", regionName, settlement)
	}
	return fmt.Sprintf("Россия, %s", settlement)
}
