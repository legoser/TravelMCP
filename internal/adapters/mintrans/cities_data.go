package mintrans

import (
	_ "embed"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed cities.yaml
var embeddedCitiesYAML []byte

type cityOverride struct {
	lat, lon float64
	source   string
}

type cityEntry struct {
	Name   string  `yaml:"name"`
	Lat    float64 `yaml:"lat"`
	Lon    float64 `yaml:"lon"`
	Source string  `yaml:"source"`
}

type citiesFile struct {
	Cities []cityEntry `yaml:"cities"`
}

func getCityOverrides() map[string]cityOverride {
	m, err := loadCityOverrides()
	if err != nil || m == nil {
		return map[string]cityOverride{}
	}
	return m
}

func loadCityOverrides() (map[string]cityOverride, error) {
	path := os.Getenv("CITIES_PATH")
	if path == "" {
		path = os.Getenv("CITIES_DATA_PATH")
	}
	if path == "" {
		path = os.Getenv("TRAVELMCP__CITIES__PATH")
	}
	var data []byte
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		data = b
	} else {
		data = embeddedCitiesYAML
	}
	var f citiesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	m := make(map[string]cityOverride, len(f.Cities))
	for _, c := range f.Cities {
		key := strings.ToLower(strings.TrimSpace(c.Name))
		if key == "" {
			continue
		}
		m[key] = cityOverride{lat: c.Lat, lon: c.Lon, source: c.Source}
	}
	return m, nil
}
