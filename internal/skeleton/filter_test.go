package skeleton

import (
	"testing"

	"travelmcp/internal/model"
)

func TestFilterYandexRecords(t *testing.T) {
	in := []model.AdaptedRecord{
		{NameRu: "Автовокзал Кемерово", Extra: map[string]string{"region": "Кузбасс", "transport_type": "bus", "station_type": "bus_station"}},
		{NameRu: "Остановка Ленина", Extra: map[string]string{"region": "Кузбасс", "transport_type": "bus", "station_type": "bus_stop"}},
		{NameRu: "Кемерово-Пасс.", Extra: map[string]string{"region": "Кузбасс", "transport_type": "rail", "station_type": "station"}},
		{NameRu: "Аэропорт Стрижино", Extra: map[string]string{"region": "Новосибирская", "transport_type": "flight", "station_type": "airport"}},
		{NameRu: "Без экстра"},
	}
	got := FilterYandexRecords(in, []string{"Кузбасс"}, map[string]bool{"bus": true, "rail": true, "flight": true}, StationClasses)
	if len(got) != 2 {
		t.Fatalf("терминальный класс Кузбасса: want 2, got %d (%v)", len(got), got)
	}
	if got[0].NameRu != "Автовокзал Кемерово" || got[1].NameRu != "Кемерово-Пасс." {
		t.Fatalf("не те записи: %v", got)
	}

	got = FilterYandexRecords(in, nil, nil, nil)
	if len(got) != len(in) {
		t.Fatalf("пустые фильтры = без отбора: want %d got %d", len(in), len(got))
	}

	got = FilterYandexRecords(in, []string{"Кузбасс"}, nil, nil)
	if len(got) != 3 {
		t.Fatalf("регион без типов: want 3 got %d", len(got))
	}
}
