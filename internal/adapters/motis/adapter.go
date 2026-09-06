package motis

import (
	"encoding/json"
	"fmt"

	"travelmcp/internal/model"
	"travelmcp/internal/support/translate"
)

func MatchToAdapted(m Match, source string) (model.AdaptedRecord, error) {
	if err := ValidateMatch(m); err != nil {
		return model.AdaptedRecord{}, err
	}
	lat := m.Lat
	lon := m.Lon
	kind := model.AdaptedTerminal
	switch m.Type {
	case "STOP":
		kind = model.AdaptedStop
	case "PLACE":
		kind = model.AdaptedPlace
	}
	ident := model.AdaptedIdentifier{System: "motis", CodeType: "motis_id", Code: m.ID}
	nameRu := m.Name
	nameEn := nameForEnglish(m)
	var adminLevel *int
	var level *int
	if len(m.Areas) > 0 {
		last := m.Areas[len(m.Areas)-1]
		al := int(last.AdminLevel)
		adminLevel = &al
		if lvl, ok := model.LevelForAdminLevel(al); ok {
			li := int(lvl)
			level = &li
		}
	}
	raw, _ := json.Marshal(m)
	rec := model.AdaptedRecord{
		Kind:        kind,
		Identifiers: []model.AdaptedIdentifier{ident},
		NameRu:      nameRu,
		NameEn:      nameEn,
		Lat:         &lat,
		Lon:         &lon,
		Tz:          deref(m.Tz),
		AdminLevel:  adminLevel,
		Level:       level,
		Source:      source,
		Raw:         raw,
	}
	if rec.Source == "" {
		rec.Source = "motis"
	}
	return rec, nil
}

func PlaceToAdapted(p Place, source string) (model.AdaptedRecord, error) {
	if err := ValidatePlace(p); err != nil {
		return model.AdaptedRecord{}, err
	}
	lat := p.Lat
	lon := p.Lon
	ident := model.AdaptedIdentifier{System: "motis", CodeType: "motis_stop_id", Code: deref(p.StopID)}
	if ident.Code == "" {
		ident.Code = p.Name
		ident.CodeType = "motis_name"
	}
	nameRu := p.Name
	nameEn := translate.TransliterateGOST779(nameRu)
	raw, _ := json.Marshal(p)
	rec := model.AdaptedRecord{
		Kind:        model.AdaptedStop,
		Identifiers: []model.AdaptedIdentifier{ident},
		NameRu:      nameRu,
		NameEn:      nameEn,
		Lat:         &lat,
		Lon:         &lon,
		Tz:          deref(p.Tz),
		Source:      source,
		Raw:         raw,
	}
	if rec.Source == "" {
		rec.Source = "motis"
	}
	return rec, nil
}

func nameForEnglish(m Match) string {
	if len(m.Areas) > 0 {
		for _, a := range m.Areas {
			if a.Matched {
				return translate.TranslateAdminName(m.Name)
			}
		}
	}
	return translate.TransliterateGOST779(m.Name)
}

func AreasToAdaptedRecords(m Match, source string) []model.AdaptedRecord {
	if source == "" {
		source = "motis"
	}
	var out []model.AdaptedRecord
	var parentCode string
	for i, a := range m.Areas {
		ident := model.AdaptedIdentifier{System: "motis", CodeType: "area", Code: a.Name}
		admin := int(a.AdminLevel)
		var level *int
		if lvl, ok := model.LevelForAdminLevel(admin); ok {
			li := int(lvl)
			level = &li
		}
		nameRu := a.Name
		nameEn := translate.TranslateAdminName(nameRu)
		raw, _ := json.Marshal(a)
		rec := model.AdaptedRecord{
			Kind:        model.AdaptedPlace,
			Identifiers: []model.AdaptedIdentifier{ident},
			NameRu:      nameRu,
			NameEn:      nameEn,
			AdminLevel:  &admin,
			Level:       level,
			ParentCode:  parentCode,
			Source:      source,
			Raw:         raw,
		}
		if lvl, ok := model.LevelForAdminLevel(admin); ok {
			_ = lvl
		}
		out = append(out, rec)
		parentCode = a.Name
		_ = i
	}
	term, _ := MatchToAdapted(m, source)
	out = append(out, term)
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func MustAdaptedFromMatchJSON(data []byte) model.AdaptedRecord {
	var m Match
	if err := json.Unmarshal(data, &m); err != nil {
		panic(fmt.Sprintf("unmarshal match: %v", err))
	}
	rec, err := MatchToAdapted(m, "motis")
	if err != nil {
		panic(err)
	}
	return rec
}
