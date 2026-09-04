package translate

import (
	"sort"
	"strings"
)

var GenericTerms = map[string]string{
	"область":             "Oblast",
	"край":                "Krai",
	"республика":          "Republic",
	"район":               "District",
	"городской округ":     "Urban Okrug",
	"муниципальный округ": "Municipal Okrug",
	"федеральный округ":   "Federal District",
	"автономный округ":    "Autonomous Okrug",
	"автономная область":  "Autonomous Oblast",
}

var genericOrder []string

func init() {
	genericOrder = make([]string, 0, len(GenericTerms))
	for k := range GenericTerms {
		genericOrder = append(genericOrder, k)
	}
	sort.Slice(genericOrder, func(i, j int) bool {
		return len(genericOrder[i]) > len(genericOrder[j])
	})
}

var gost779 = map[rune]string{
	'А': "A", 'а': "a",
	'Б': "B", 'б': "b",
	'В': "V", 'в': "v",
	'Г': "G", 'г': "g",
	'Д': "D", 'д': "d",
	'Е': "E", 'е': "e",
	'Ё': "Yo", 'ё': "yo",
	'Ж': "Zh", 'ж': "zh",
	'З': "Z", 'з': "z",
	'И': "I", 'и': "i",
	'Й': "J", 'й': "j",
	'К': "K", 'к': "k",
	'Л': "L", 'л': "l",
	'М': "M", 'м': "m",
	'Н': "N", 'н': "n",
	'О': "O", 'о': "o",
	'П': "P", 'п': "p",
	'Р': "R", 'р': "r",
	'С': "S", 'с': "s",
	'Т': "T", 'т': "t",
	'У': "U", 'у': "u",
	'Ф': "F", 'ф': "f",
	'Х': "Kh", 'х': "kh",
	'Ц': "Cz", 'ц': "cz",
	'Ч': "Ch", 'ч': "ch",
	'Ш': "Sh", 'ш': "sh",
	'Щ': "Shh", 'щ': "shh",
	'Ъ': "``", 'ъ': "``",
	'Ы': "Y`", 'ы': "y`",
	'Ь': "`", 'ь': "`",
	'Э': "E`", 'э': "e`",
	'Ю': "Yu", 'ю': "yu",
	'Я': "Ya", 'я': "ya",
}

func TransliterateGOST779(s string) string {
	var b strings.Builder
	for _, r := range s {
		if v, ok := gost779[r]; ok {
			b.WriteString(v)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TranslateAdminName(ru string) string {
	lower := strings.ToLower(strings.TrimSpace(ru))
	for _, k := range genericOrder {
		if strings.Contains(lower, k) {
			enTerm := GenericTerms[k]
			idx := strings.Index(lower, k)
			properPart := strings.TrimSpace(ru[:idx])
			if properPart == "" {
				properPart = strings.TrimSpace(ru[idx+len(k):])
				if properPart != "" {
					return TransliterateGOST779(properPart) + " " + enTerm
				}
				return enTerm
			}
			properPart = strings.Trim(properPart, " -")
			return TransliterateGOST779(properPart) + " " + enTerm
		}
	}
	return TransliterateGOST779(ru)
}

func IsGenericTerm(s string) bool {
	_, ok := GenericTerms[strings.ToLower(strings.TrimSpace(s))]
	return ok
}
