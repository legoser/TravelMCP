package classifier

import (
	"testing"

	"travelmcp/internal/model"
)

func TestClassifyEdge(t *testing.T) {
	cases := []struct {
		name string
		stop string
		tags map[string]string
		want model.StopType
	}{
		{"aerodrome tag", "Поле", map[string]string{"aeroway": "aerodrome"}, model.StopTypeAirport},
		{"bus station tag", "Остановка", map[string]string{"amenity": "bus_station"}, model.StopTypeHub},
		{"station tag", "Платформа", map[string]string{"public_transport": "station"}, model.StopTypeStation},
		{"no tags default", "Остановка", nil, model.StopTypeStation},
		{"empty tags default", "Остановка", map[string]string{}, model.StopTypeStation},
		{"unrelated tags default", "Остановка", map[string]string{"shop": "mall"}, model.StopTypeStation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Default.Classify(tc.stop, tc.tags); got != tc.want {
				t.Fatalf("Default.Classify=%q want %q", got, tc.want)
			}
		})
	}
	ruCases := []struct {
		name string
		stop string
		want model.StopType
	}{
		{"airport name", "Аэропорт Толмачёво", model.StopTypeAirport},
		{"bus station name", "Кемерово, автовокзал", model.StopTypeHub},
		{"rail station name", "Жд вокзал", model.StopTypeStation},
		{"plain name", "Остановка Центральная", model.StopTypeStation},
		{"empty name", "", model.StopTypeStation},
	}
	for _, tc := range ruCases {
		t.Run("ru/"+tc.name, func(t *testing.T) {
			if got := Ru.Classify(tc.stop, nil); got != tc.want {
				t.Fatalf("Ru.Classify(%q)=%q want %q", tc.stop, got, tc.want)
			}
		})
	}
}
