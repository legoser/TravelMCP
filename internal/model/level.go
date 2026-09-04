package model

type Level int

const (
	LevelCountry         Level = 0
	LevelFederalDistrict Level = 1
	LevelRegion          Level = 2
	LevelDistrict        Level = 3
	LevelCity            Level = 4
	LevelCityDistrict    Level = 5
)

var AdminLevelToLevel = map[int]Level{
	2:  LevelCountry,
	3:  LevelFederalDistrict,
	4:  LevelRegion,
	6:  LevelDistrict,
	8:  LevelCity,
	9:  LevelCityDistrict,
	10: LevelCityDistrict,
}

func LevelForAdminLevel(adminLevel int) (Level, bool) {
	l, ok := AdminLevelToLevel[adminLevel]
	return l, ok
}

func (l Level) AdminLevel() int {
	switch l {
	case LevelCountry:
		return 2
	case LevelFederalDistrict:
		return 3
	case LevelRegion:
		return 4
	case LevelDistrict:
		return 6
	case LevelCity:
		return 8
	case LevelCityDistrict:
		return 9
	default:
		return 0
	}
}

func (l Level) String() string {
	switch l {
	case LevelCountry:
		return "country"
	case LevelFederalDistrict:
		return "federal_district"
	case LevelRegion:
		return "region"
	case LevelDistrict:
		return "district"
	case LevelCity:
		return "city"
	case LevelCityDistrict:
		return "city_district"
	default:
		return "unknown"
	}
}

var LevelNamesRu = map[Level]string{
	LevelCountry:         "страна",
	LevelFederalDistrict: "федеральный округ",
	LevelRegion:          "регион",
	LevelDistrict:        "район / городской округ",
	LevelCity:            "город / населённый пункт",
	LevelCityDistrict:    "внутригородской район",
}

var LevelExamples = map[Level]string{
	LevelCountry:         "РФ",
	LevelFederalDistrict: "Сибирский ФО",
	LevelRegion:          "Кемеровская область",
	LevelDistrict:        "Кемеровский ГО",
	LevelCity:            "Кемерово",
	LevelCityDistrict:    "Центральный район",
}
