package translate

import "testing"

func TestGenericTerms(t *testing.T) {
	cases := map[string]string{
		"область":           "Oblast",
		"край":              "Krai",
		"республика":        "Republic",
		"район":             "District",
		"городской округ":   "Urban Okrug",
		"федеральный округ": "Federal District",
	}
	for k, v := range cases {
		if GenericTerms[k] != v {
			t.Fatalf("term %q expected %q got %q", k, v, GenericTerms[k])
		}
	}
}

func TestTransliterate(t *testing.T) {
	cases := map[string]string{
		"Кемерово":    "Kemerovo",
		"Москва":      "Moskva",
		"Новосибирск": "Novosibirsk",
		"Юрга":        "Yurga",
		"Томск":       "Tomsk",
	}
	for ru, en := range cases {
		got := TransliterateGOST779(ru)
		if got != en {
			t.Fatalf("translit %q expected %q got %q", ru, en, got)
		}
	}
}

func TestTranslateAdminName(t *testing.T) {
	cases := []struct {
		ru string
		en string
	}{
		{"Кемеровская область", "Kemerovskaya Oblast"},
		{"Сибирский федеральный округ", "Sibirskij Federal District"},
		{"Кемеровский городской округ", "Kemerovskij Urban Okrug"},
		{"Томская область", "Tomskaya Oblast"},
	}
	for _, c := range cases {
		got := TranslateAdminName(c.ru)
		if got != c.en {
			t.Fatalf("translate %q expected %q got %q", c.ru, c.en, got)
		}
	}
}
