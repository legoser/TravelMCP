package namesim

import (
	"strings"
	"unicode"
)

// noiseTokens — слова-мусор для сопоставления имён: типы объектов, адм. префиксы,
// транспортные суффиксы и регионные слова. Удаляются из Core целиком (в любой позиции).
var noiseTokens = map[string]bool{
	"оп": true, "ав": true, "ас": true, "дкп": true, "кдп": true, "кп": true,
	"пов": true, "остановочный": true, "пункт": true, "кассовый": true,
	"автостанция": true, "автовокзал": true, "автобусная": true, "станция": true,
	"вокзал": true, "жд": true, "аэропорт": true, "аэродром": true,
	"остановка": true, "автопавильон": true, "диспетчерско": true, "диспетчерский": true,
	"г": true, "город": true, "пос": true, "посёлок": true, "поселок": true,
	"пгт": true, "п": true, "село": true, "с": true, "ст": true, "д": true, "деревня": true,
	"край": true, "область": true, "республика": true, "округ": true, "ао": true, "россия": true,
}

var settlementPrefixes = map[string]bool{
	"г": true, "город": true, "пгт": true, "пос": true, "посёлок": true, "поселок": true,
	"п": true, "село": true, "с": true, "ст": true, "д": true, "деревня": true,
}

var facilityStopWords = map[string]bool{
	"аэропорт": true, "аэродром": true, "вокзал": true, "автостанция": true, "остановка": true,
	"остановочный": true, "пункт": true, "кассовый": true, "диспетчерско-кассовый": true,
	"завод": true, "цементный": true, "школа": true, "центральный": true, "международный": true,
}

// ExpandAbbreviations раскрывает сокращения типов объектов в запросе к геокодеру:
// «ОП г. Бердск» → «остановочный пункт г. Бердск». Сравнение по целым токенам,
// регистронезависимо; остальное не трогается.
var abbreviationExpansions = map[string]string{
	"оп":  "остановочный пункт",
	"ав":  "автовокзал",
	"ас":  "автостанция",
	"дкп": "диспетчерско-кассовый пункт",
	"кдп": "кассово-диспетчерский пункт",
	"кп":  "кассовый пункт",
	"дп":  "диспетчерский пункт",
	"ждв": "железнодорожный вокзал",
	"жд":  "железнодорожный",
}

func ExpandAbbreviations(name string) string {
	toks := strings.Fields(name)
	for i, t := range toks {
		key := strings.ToLower(strings.Trim(t, ".\"«»()"))
		if exp, ok := abbreviationExpansions[key]; ok {
			toks[i] = exp
		}
	}
	return strings.Join(toks, " ")
}

func ExtractSettlement(name string) string {
	n := Normalize(name)
	if n == "" {
		return ""
	}
	toks := strings.Fields(n)
	// Формат Минтранса/Яндекса «Населённый пункт, тип ОП» («Чажемто,
	// автовокзал»): топоним — первая часть до запятой. Шумные первые
	// части («г.», «АВ») шаблон сам отклоняет — их подберут шаблоны ниже.
	if first := firstCommaPart(name); first != "" {
		return first
	}
	for i, t := range toks {
		if !settlementPrefixes[t] {
			continue
		}
		var out []string
		for _, w := range toks[i+1:] {
			if facilityStopWords[w] || noiseTokens[w] {
				break
			}
			out = append(out, w)
			if len(out) >= 3 {
				break
			}
		}
		if len(out) == 0 {
			continue
		}
		return titleCase(strings.Join(out, " "))
	}
	// Паттерн реестра Минтранса: название — прилагательное города + тип
	// объекта («Кемеровский АВ», «Мариинский автовокзал»). Префикса нет, но
	// первый токен — городская форма прилагательного: возвращаем её,
	// SameSettlement сведёт с именным вариантом («Кемерово»).
	if len(toks) > 1 {
		if first := toks[0]; len([]rune(first)) >= 6 {
			for _, suf := range adjectiveSuffixes {
				if strings.HasSuffix(first, suf) && !facilityStopWords[first] {
					return titleCase(first)
				}
			}
		}
	}
	return ""
}

// firstCommaPart — топоним из формата «Населённый пункт, тип ОП»: берём
// часть до первой запятой в исходном регистре; если она — шум-токен
// (тип ОП, адм. префикс) или короткая аббревиатура, шаблон не сработал.
func firstCommaPart(name string) string {
	idx := strings.IndexByte(name, ',')
	if idx <= 0 {
		return ""
	}
	part := strings.TrimSpace(name[:idx])
	if part == "" {
		return ""
	}
	for _, t := range strings.Fields(Normalize(part)) {
		if noiseTokens[t] || facilityStopWords[t] || len([]rune(t)) < 3 {
			return ""
		}
	}
	return part
}

// titleCase — заглавная первая буква: тег settlement идёт в газетир,
// где isPlausibleSettlement требует топоним с заглавной буквы
// (строчные прилагательные «кемеровский» отсекаются как мусор).
func titleCase(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// adjectiveSuffixes — суффиксы городских прилагательных («Кемеровский» от
// «Кемерово»). Словарное сравнение имён не ловит связку
// «Кемеровский АВ» ↔ «Кемерово, автовокзал», settlement-фича молчит и
// без-координатный стоп уходит в skeleton_gap.
var adjectiveSuffixes = []string{"ский", "цкий", "ая", "ое", "ый"}

// SameSettlement — равенство названий поселений с учётом прилагательной
// формы города: «Кемерово» ≡ «кемеровский» (основа прилагательного — префикс
// имени города, хвост имени ≤2 символов — выпадающая гласная/«-ск»:
// кемеров+о, мариин+ск, новокузнец+к). «Мариинск» ≡ «мариинский».
func SameSettlement(a, b string) bool {
	ca, cb := Core(a), Core(b)
	if ca == "" || cb == "" {
		return false
	}
	if ca == cb {
		return true
	}
	return adjectiveStemMatches(ca, cb) || adjectiveStemMatches(cb, ca)
}

// adjectiveStemMatches: adj — прилагательное (заканчивается на суффикс),
// city — имя города; основа прилагательного (≥4 символов) должна быть
// префиксом города, остаток — ≤2 символов (кемеров|о, мариин|ск).
func adjectiveStemMatches(adj, city string) bool {
	ar, cr := []rune(adj), []rune(city)
	for _, suf := range adjectiveSuffixes {
		sr := []rune(suf)
		if !hasSuffixRunes(ar, sr) {
			continue
		}
		stem := ar[:len(ar)-len(sr)]
		if len(stem) < 4 || len(stem) > len(cr) {
			continue
		}
		if string(stem) != string(cr[:len(stem)]) {
			continue
		}
		if len(cr)-len(stem) <= 2 {
			return true
		}
	}
	return false
}

func hasSuffixRunes(s, suf []rune) bool {
	if len(s) < len(suf) {
		return false
	}
	return string(s[len(s)-len(suf):]) == string(suf)
}

func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSpace = false
			continue
		}
		if r == ' ' || r == '-' || r == '.' || r == ',' {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		}
	}
	res := strings.TrimSpace(b.String())
	res = strings.Join(strings.Fields(res), " ")
	return res
}

func Core(s string) string {
	toks := strings.Fields(Normalize(s))
	var out []string
	for _, t := range toks {
		if noiseTokens[t] {
			continue
		}
		out = append(out, t)
	}
	return strings.Join(out, " ")
}

func tokenSubset(short, long string) bool {
	ss := strings.Fields(short)
	if len(ss) == 0 {
		return false
	}
	have := map[string]bool{}
	for _, t := range strings.Fields(long) {
		have[t] = true
	}
	for _, t := range ss {
		if !have[t] {
			return false
		}
	}
	return true
}

func Levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	da := make([][]int, len(ra)+1)
	for i := range da {
		da[i] = make([]int, len(rb)+1)
		da[i][0] = i
	}
	for j := range da[0] {
		da[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			da[i][j] = min(da[i-1][j]+1, da[i][j-1]+1, da[i-1][j-1]+cost)
		}
	}
	return da[len(ra)][len(rb)]
}

func NormalizedLevenshtein(a, b string) float64 {
	a = Normalize(a)
	b = Normalize(b)
	if a == b {
		return 0
	}
	la, lb := len([]rune(a)), len([]rune(b))
	if la == 0 || lb == 0 {
		return 1
	}
	dist := Levenshtein(a, b)
	maxLen := la
	if lb > maxLen {
		maxLen = lb
	}
	return float64(dist) / float64(maxLen)
}

func Similarity(a, b string) float64 {
	ca := Core(a)
	cb := Core(b)
	if ca == "" || cb == "" {
		return NormalizedSimilarity(a, b)
	}
	if ca == cb {
		return 1.0
	}
	if tokenSubset(ca, cb) || tokenSubset(cb, ca) {
		return 0.95
	}
	if len([]rune(ca)) >= 3 && len([]rune(cb)) >= 3 {
		if strings.Contains(ca, cb) || strings.Contains(cb, ca) {
			return 0.9
		}
	}
	lev := NormalizedLevenshtein(ca, cb)
	sim := 1.0 - lev
	if sim < 0 {
		sim = 0
	}
	return sim
}

func NormalizedSimilarity(a, b string) float64 {
	na := Normalize(a)
	nb := Normalize(b)
	if na == nb {
		return 1.0
	}
	if len([]rune(na)) >= 3 && len([]rune(nb)) >= 3 {
		if strings.Contains(na, nb) || strings.Contains(nb, na) {
			return 0.9
		}
	}
	lev := NormalizedLevenshtein(na, nb)
	sim := 1.0 - lev
	if sim < 0 {
		sim = 0
	}
	return sim
}

func Match(a, b string, threshold float64) bool {
	return Similarity(a, b) >= threshold
}
