package namesim

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"  АВ г. Барнаул ":            "ав г барнаул",
		"«ОП «Вокзал «Новосибирск»":   "оп вокзал новосибирск",
		"Новосибирск-Главный":         "новосибирск главный",
		"г. Магнитогорск (Цементный)": "г магнитогорск цементный",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCore(t *testing.T) {
	cases := map[string]string{
		"Барнаул автовокзал":  "барнаул",
		"г. Барнаул":          "барнаул",
		"Кемерово автовокзал": "кемерово",
		"Новосибирск-Главный": "новосибирск главный",
	}
	for in, want := range cases {
		if got := Core(in); got != want {
			t.Errorf("Core(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractSettlement(t *testing.T) {
	cases := map[string]string{
		"АВ г. Барнаул":                     "Барнаул",
		"Автостанция г. Стрежевой":          "Стрежевой",
		"Остановочный пункт п. Новый Быт":   "Новый быт",
		"ОП «Кассовый пункт пгт Яя»":        "Яя",
		"АВ г. Пятигорск":                   "Пятигорск",
		"г. Магнитогорск (Цементный завод)": "Магнитогорск",
		"ОП г. Минеральные Воды (аэропорт)": "Минеральные воды",
		"п. Абзаково":                       "Абзаково",
		"АВ «Северный» г. Екатеринбург":     "Екатеринбург",
		"Кемеровский АВ":                    "Кемеровский",
		"Мариинский автовокзал":             "Мариинский",
		"Новокузнецкий АВ":                  "Новокузнецкий",
		"Промышленновская АС":               "Промышленновская",
		"ОП «ЛПК»":                          "",
		"АВ «Центральный»":                  "",
		"ОП п. Аэропорт":                    "",
		"АС «Автостанция»":                  "",
		"пов. Аэропорт":                     "",
		"":                                  "",
		// формат Минтранса/Яндекса «Населённый пункт, тип ОП» (issue #4)
		"Чажемто, автовокзал":              "Чажемто",
		"Юрга, автовокзал":                 "Юрга",
		"Санкт-Петербург, автовокзал":      "Санкт-Петербург",
		"г. Томск, автовокзал":             "Томск",
		"АВ, Центральный":                  "",
		"Чажемто":                          "",
		"Топки, автостанция":               "Топки",
		"Бийск, автовокзал":                "Бийск",
		"Кемерово, Железнодорожный вокзал": "Кемерово",
	}
	for in, want := range cases {
		if got := ExtractSettlement(in); got != want {
			t.Errorf("ExtractSettlement(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSameSettlement(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Кемерово", "кемеровский", true},
		{"Мариинск", "мариинский", true},
		{"Новокузнецк", "новокузнецкий", true},
		{"Кемерово", "Кемерово", true},
		{"Кемерово", "Томск", false},
		{"", "Кемерово", false},
	}
	for _, c := range cases {
		if got := SameSettlement(c.a, c.b); got != c.want {
			t.Errorf("SameSettlement(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestExpandAbbreviations(t *testing.T) {
	cases := map[string]string{
		"ОП г. Бердск":                  "остановочный пункт г. Бердск",
		"Кемеровский АВ":                "Кемеровский автовокзал",
		"АВ «Северный» г. Екатеринбург": "автовокзал «Северный» г. Екатеринбург",
		"ДКП г. Искитим":                "диспетчерско-кассовый пункт г. Искитим",
		"АС «Автостанция»":              "автостанция «Автостанция»",
		"Барнаул":                       "Барнаул",
		"":                              "",
	}
	for in, want := range cases {
		if got := ExpandAbbreviations(in); got != want {
			t.Errorf("ExpandAbbreviations(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeYo(t *testing.T) {
	if got := Normalize("Линёво"); got != "линево" {
		t.Errorf("Normalize(Линёво) = %q, want линево", got)
	}
	if got := Similarity("Линево", "Линёво, Искитимский район"); got < 0.8 {
		t.Errorf("Similarity(Линево, Линёво...) = %v, want >= 0.8", got)
	}
}

func TestSimilarity(t *testing.T) {
	if got := Similarity("Барнаул автовокзал", "г. Барнаул"); got < 0.99 {
		t.Errorf("exact core similarity = %v, want 1.0", got)
	}
	if got := Similarity("Новосибирск-Главный", "Новосибирск"); got < 0.89 {
		t.Errorf("containment similarity = %v, want >= 0.9", got)
	}
	if got := Similarity("ОП «ЛПК»", "г. Барнаул"); got >= 0.8 {
		t.Errorf("different names similarity = %v, want < 0.8", got)
	}
	if !Match("АВ г. Барнаул", "Барнаул, Алтайский край", 0.8) {
		t.Errorf("Match(барнаул, барнаул...) should pass 0.8")
	}
	if Match("ОП «ЛПК»", "Ленинск-Кузнецкий", 0.8) {
		t.Errorf("Match(лпк, ленинск...) should fail 0.8")
	}
}
