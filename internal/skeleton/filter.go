package skeleton

import "travelmcp/internal/model"

// StationClasses — терминальные классы Яндекс-станций для междугородного
// скелета (городские bus_stop/stop/platform не входят в пилот сбора).
var StationClasses = map[string]bool{
	"bus_station":   true,
	"station":       true,
	"train_station": true,
	"airport":       true,
}

// FilterYandexRecords — фильтр AdaptedRecord-набора (экстра-ключи дампа
// Яндекса): регионы (пусто = все), транспорт (canon: bus/train/flight/
// rail/...), терминальные классы station_type. nil-фильтр = без отбора.
func FilterYandexRecords(in []model.AdaptedRecord, regions []string, transports map[string]bool, stationTypes map[string]bool) []model.AdaptedRecord {
	regionSet := map[string]bool{}
	for _, r := range regions {
		regionSet[r] = true
	}
	useRegions := len(regionSet) > 0
	useTransports := len(transports) > 0
	useStationTypes := len(stationTypes) > 0
	out := make([]model.AdaptedRecord, 0, len(in))
	for _, r := range in {
		if useRegions && (r.Extra == nil || !regionSet[r.Extra["region"]]) {
			continue
		}
		if useTransports && (r.Extra == nil || !transports[r.Extra["transport_type"]]) {
			continue
		}
		if useStationTypes && (r.Extra == nil || !stationTypes[r.Extra["station_type"]]) {
			continue
		}
		out = append(out, r)
	}
	return out
}
