package skeleton

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"travelmcp/internal/model"
)

type flexFloat float64

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	var num float64
	if err := json.Unmarshal(data, &num); err == nil {
		*f = flexFloat(num)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("flexFloat: %s", string(data))
	}
	s = strings.TrimSpace(s)
	if s == "" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("flexFloat %q: %w", s, err)
	}
	*f = flexFloat(v)
	return nil
}

type yandexCodes struct {
	YandexCode string `json:"yandex_code"`
	EsrCode    string `json:"esr_code"`
}

type yandexStation struct {
	Title         string      `json:"title"`
	Longitude     flexFloat   `json:"longitude"`
	Latitude      flexFloat   `json:"latitude"`
	TransportType string      `json:"transport_type"`
	StationType   string      `json:"station_type"`
	Codes         yandexCodes `json:"codes"`
}

type yandexSettlement struct {
	Title    string          `json:"title"`
	Stations []yandexStation `json:"stations"`
}

type yandexRegion struct {
	Title       string             `json:"title"`
	Settlements []yandexSettlement `json:"settlements"`
}

type yandexCountry struct {
	Title   string         `json:"title"`
	Regions []yandexRegion `json:"regions"`
}

type yandexDump struct {
	Countries []yandexCountry `json:"countries"`
}

type YandexDumpSource struct {
	Path string
}

func (s YandexDumpSource) Name() string { return "yandex" }

func (s YandexDumpSource) Load() ([]model.AdaptedRecord, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	var dump yandexDump
	if err := json.Unmarshal(data, &dump); err != nil {
		return nil, fmt.Errorf("skeleton yandex %s: %w", s.Path, err)
	}
	var out []model.AdaptedRecord
	for _, c := range dump.Countries {
		for _, r := range c.Regions {
			for _, st := range r.Settlements {
				for _, s := range st.Stations {
					if strings.TrimSpace(s.Title) == "" {
						continue
					}
					lat, lon := float64(s.Latitude), float64(s.Longitude)
					var latP, lonP *float64
					if lat != 0 || lon != 0 {
						la, lo := lat, lon
						latP, lonP = &la, &lo
					}
					ids := []model.AdaptedIdentifier{}
					if s.Codes.YandexCode != "" {
						ids = append(ids, model.AdaptedIdentifier{System: "yandex", CodeType: "yandex_code", Code: s.Codes.YandexCode})
					}
					if s.Codes.EsrCode != "" {
						ids = append(ids, model.AdaptedIdentifier{System: "yandex", CodeType: "esr_code", Code: s.Codes.EsrCode})
					}
					out = append(out, model.AdaptedRecord{
						Kind:        model.AdaptedTerminal,
						NameRu:      s.Title,
						Lat:         latP,
						Lon:         lonP,
						Source:      "yandex",
						Identifiers: ids,
						Extra: map[string]string{
							"settlement":     st.Title,
							"region":         r.Title,
							"transport_type": yandexTransportType(s.TransportType),
							"station_type":   s.StationType,
						},
					})
				}
			}
		}
	}
	return out, nil
}

func yandexTransportType(t string) string {
	switch t {
	case "train", "suburban":
		return "rail"
	case "plane":
		return "flight"
	case "bus", "tram", "trolleybus", "metro", "water":
		return t
	default:
		return t
	}
}
