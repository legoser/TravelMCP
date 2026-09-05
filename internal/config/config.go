package config

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type HTTP struct {
	Addr               string               `yaml:"addr"`
	ReadHeaderTimeout  string               `yaml:"read_header_timeout"`
	ShutdownTimeout    string               `yaml:"shutdown_timeout"`
	RateLimit          RateLimit            `yaml:"rate_limit"`
	RateLimitOverrides map[string]RateLimit `yaml:"rate_limit_overrides"`
}

type RateLimit struct {
	RPS   int `yaml:"rps"`
	Burst int `yaml:"burst"`
}

type Store struct {
	Kind         string `yaml:"kind"`
	DSN          string `yaml:"dsn"`
	MaxOpenConns int    `yaml:"max_open_conns"`
}

type Cache struct {
	Kind string `yaml:"kind"`
	Addr string `yaml:"addr"`
	TTL  string `yaml:"ttl"`
}

type Queue struct {
	Kind string `yaml:"kind"`
	URL  string `yaml:"url"`
}

type Database struct {
	DSN string `yaml:"dsn"`
}

type Intercity struct {
	ReestrPath string `yaml:"reestr_path"`
	Bounds     string `yaml:"bounds"`
}

type GTFS struct {
	Path string `yaml:"path"`
}

type Cities struct {
	Path string `yaml:"path"`
}

type Motis struct {
	URL string `yaml:"url"`
}

type Providers struct {
	Enabled   []string  `yaml:"enabled"`
	Intercity Intercity `yaml:"intercity"`
	GTFS      GTFS      `yaml:"gtfs"`
}

type Auth struct {
	AdminToken string `yaml:"admin_token"`
}

type Yandex struct {
	RaspKey     string `yaml:"rasp_key"`
	RaspURL     string `yaml:"rasp_url"`
	GeocodeKey  string `yaml:"geocode_key"`
	GeocodeURL  string `yaml:"geocode_url"`
	GeocodeKind string `yaml:"geocode_kind"`
}

type Nominatim struct {
	URL string `yaml:"url"`
}

type Geocoder struct {
	Kind     string `yaml:"kind"`
	URL      string `yaml:"url"`
	Key      string `yaml:"key"`
	ApiKey   string `yaml:"api_key"`
	Attempts int    `yaml:"attempts"`
}

func (g *Geocoder) UnmarshalYAML(node *yaml.Node) error {
	type raw Geocoder
	var tmp struct {
		Kind     string `yaml:"kind"`
		URL      string `yaml:"url"`
		BaseURL  string `yaml:"base_url"`
		Key      string `yaml:"key"`
		ApiKey   string `yaml:"api_key"`
		Attempts *int   `yaml:"attempts"`
	}
	if err := node.Decode(&tmp); err != nil {
		return err
	}
	_ = raw{}
	g.Kind = tmp.Kind
	if tmp.URL != "" {
		g.URL = tmp.URL
	} else if tmp.BaseURL != "" {
		g.URL = tmp.BaseURL
	}
	if tmp.Key != "" {
		g.Key = tmp.Key
	} else if tmp.ApiKey != "" {
		g.Key = tmp.ApiKey
	}
	g.ApiKey = g.Key
	if tmp.Attempts != nil {
		g.Attempts = *tmp.Attempts
	}
	return nil
}

type Planner struct {
	Engine          string `yaml:"engine"`
	SemaphoreSize   int    `yaml:"semaphore_size"`
	SemaphoreEnable bool   `yaml:"semaphore_enable"`
}

type Deduplication struct {
	DistanceM int `yaml:"distance_m"`
}

type Verification struct {
	ConfidenceThreshold float64                     `yaml:"confidence_threshold"`
	DistanceM           int                         `yaml:"distance_m"`
	StrongDistanceM     int                         `yaml:"strong_distance_m"`
	LevThreshold        float64                     `yaml:"lev_threshold"`
	DensityThresholds   map[string]DensityThreshold `yaml:"density_thresholds"`
}

type DensityThreshold struct {
	DistanceM           *int     `yaml:"distance_m"`
	StrongDistanceM     *int     `yaml:"strong_distance_m"`
	LevThreshold        *float64 `yaml:"lev_threshold"`
	ConfidenceThreshold *float64 `yaml:"confidence_threshold"`
}

type Log struct {
	Level     string            `yaml:"level"`
	Format    string            `yaml:"format"`
	AddSource bool              `yaml:"add_source"`
	Levels    map[string]string `yaml:"levels"`
}

type Telemetry struct {
	PrometheusAddr string `yaml:"prometheus_addr"`
}

type Pricing struct {
	DefaultCurrency string `yaml:"default_currency"`
}

type Config struct {
	HTTP          HTTP          `yaml:"http"`
	Store         Store         `yaml:"store"`
	Cache         Cache         `yaml:"cache"`
	Queue         Queue         `yaml:"queue"`
	Database      Database      `yaml:"database"`
	Providers     Providers     `yaml:"providers"`
	Cities        Cities        `yaml:"cities"`
	Auth          Auth          `yaml:"auth"`
	Geocoder      Geocoder      `yaml:"geocoder"`
	Yandex        Yandex        `yaml:"yandex"`
	Nominatim     Nominatim     `yaml:"nominatim"`
	Motis         Motis         `yaml:"motis"`
	Planner       Planner       `yaml:"planner"`
	Log           Log           `yaml:"log"`
	Telemetry     Telemetry     `yaml:"telemetry"`
	Verification  Verification  `yaml:"verification"`
	Deduplication Deduplication `yaml:"deduplication"`
	Pricing       Pricing       `yaml:"pricing"`
}

func Defaults() *Config {
	return &Config{
		HTTP: HTTP{
			Addr:              ":8080",
			ReadHeaderTimeout: "10s",
			ShutdownTimeout:   "10s",
			RateLimit:         RateLimit{RPS: 100, Burst: 200},
		},
		Store:    Store{Kind: "memory"},
		Cache:    Cache{Kind: "memory", TTL: "5m"},
		Queue:    Queue{Kind: "memory"},
		Database: Database{},
		Providers: Providers{
			Enabled: []string{},
			Intercity: Intercity{
				ReestrPath: "data/reestr/regions.json",
			},
		},
		Cities:    Cities{Path: "configs/cities.yaml"},
		Geocoder:  Geocoder{Kind: "", URL: "", Key: "", Attempts: 3},
		Yandex:    Yandex{GeocodeURL: "https://geocode-maps.yandex.ru/1.x", GeocodeKind: ""},
		Nominatim: Nominatim{URL: "https://nominatim.openstreetmap.org"},
		Motis:     Motis{URL: "http://192.168.57.14:8077"},
		Planner:   Planner{Engine: "csa", SemaphoreSize: runtime.NumCPU() * 2, SemaphoreEnable: true},
		Log:       Log{Level: "info", Format: "json", Levels: map[string]string{}},
		Verification: Verification{
			ConfidenceThreshold: 0.6,
			DistanceM:           200,
			StrongDistanceM:     100,
			LevThreshold:        0.15,
			DensityThresholds:   map[string]DensityThreshold{},
		},
		Deduplication: Deduplication{DistanceM: 200},
		Pricing:       Pricing{DefaultCurrency: "RUB"},
	}
}

func Load(path string) (*Config, error) {
	cfg := Defaults()

	if err := LoadDotenv(); err != nil {
		return nil, fmt.Errorf("config: dotenv: %w", err)
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
		if err == nil {
			expanded := os.ExpandEnv(string(data))
			if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
				return nil, fmt.Errorf("config: parse %s: %w", path, err)
			}
		}
	}

	applyEnv(cfg)
	applyPrefixedEnv(cfg)
	syncLegacy(cfg)
	return cfg, nil
}

func syncLegacy(cfg *Config) {
	if cfg.Store.DSN == "" && cfg.Database.DSN != "" {
		cfg.Store.DSN = cfg.Database.DSN
	}
	if cfg.Database.DSN == "" && cfg.Store.DSN != "" {
		cfg.Database.DSN = cfg.Store.DSN
	}
	if cfg.Nominatim.URL == "" {
		cfg.Nominatim.URL = "https://nominatim.openstreetmap.org"
	}
	if cfg.Yandex.GeocodeURL == "" {
		cfg.Yandex.GeocodeURL = "https://geocode-maps.yandex.ru/1.x"
	}
	if cfg.Geocoder.URL != "" && cfg.Yandex.GeocodeURL == "https://geocode-maps.yandex.ru/1.x" {
		cfg.Yandex.GeocodeURL = cfg.Geocoder.URL
	}
	if cfg.Geocoder.Key != "" && cfg.Yandex.GeocodeKey == "" {
		cfg.Yandex.GeocodeKey = cfg.Geocoder.Key
	}
	if cfg.Geocoder.ApiKey != "" && cfg.Yandex.GeocodeKey == "" {
		cfg.Yandex.GeocodeKey = cfg.Geocoder.ApiKey
	}
	if cfg.Geocoder.ApiKey == "" && cfg.Geocoder.Key != "" {
		cfg.Geocoder.ApiKey = cfg.Geocoder.Key
	}
	if cfg.Geocoder.Key == "" && cfg.Geocoder.ApiKey != "" {
		cfg.Geocoder.Key = cfg.Geocoder.ApiKey
	}
	if cfg.Geocoder.Attempts <= 0 {
		cfg.Geocoder.Attempts = 3
	}
	if cfg.Cities.Path == "" {
		cfg.Cities.Path = "configs/cities.yaml"
	}
	if cfg.Verification.ConfidenceThreshold == 0 {
		cfg.Verification.ConfidenceThreshold = 0.6
	}
	if cfg.Verification.DistanceM == 0 {
		cfg.Verification.DistanceM = 500
	}
	if cfg.Verification.StrongDistanceM == 0 {
		cfg.Verification.StrongDistanceM = 200
	}
	if cfg.Verification.LevThreshold == 0 {
		cfg.Verification.LevThreshold = 0.15
	}
	if cfg.Deduplication.DistanceM == 0 {
		cfg.Deduplication.DistanceM = 200
	}
	if cfg.Verification.DistanceM == 0 {
		cfg.Verification.DistanceM = cfg.Deduplication.DistanceM
	}
	if cfg.Verification.StrongDistanceM == 0 {
		cfg.Verification.StrongDistanceM = cfg.Deduplication.DistanceM / 2
	}
	if cfg.Verification.DensityThresholds == nil {
		cfg.Verification.DensityThresholds = map[string]DensityThreshold{}
	}
	if cfg.Pricing.DefaultCurrency == "" {
		cfg.Pricing.DefaultCurrency = "RUB"
	}
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("HTTP_ADDR"); v != "" {
		cfg.HTTP.Addr = v
	}
	if v := os.Getenv("DATABASE_DSN"); v != "" {
		cfg.Database.DSN = v
		cfg.Store.DSN = v
	}
	if v := os.Getenv("ADMIN_TOKEN"); v != "" {
		cfg.Auth.AdminToken = v
	}
	if v := os.Getenv("PROVIDERS_ENABLED"); v != "" {
		cfg.Providers.Enabled = splitCsv(v)
	}
	if v := os.Getenv("INTERCITY_REESTR_PATH"); v != "" {
		cfg.Providers.Intercity.ReestrPath = v
	}
	if v := os.Getenv("YANDEX_RASP_KEY"); v != "" {
		cfg.Yandex.RaspKey = v
	}
	if v := os.Getenv("YANDEX_GEOCODE_KEY"); v != "" {
		cfg.Yandex.GeocodeKey = v
	}
	if v := os.Getenv("YANDEX_GEOCODE_URL"); v != "" {
		cfg.Yandex.GeocodeURL = v
	}
	if v := os.Getenv("YANDEX_GEOCODE_KIND"); v != "" {
		cfg.Yandex.GeocodeKind = v
	}
	if v := os.Getenv("NOMINATIM_URL"); v != "" {
		cfg.Nominatim.URL = v
	}
	if v := os.Getenv("NOMINATIM_BASE_URL"); v != "" {
		cfg.Nominatim.URL = v
	}
	if v := os.Getenv("GEOCODER_KIND"); v != "" {
		cfg.Geocoder.Kind = v
	}
	if v := os.Getenv("GEOCODER_URL"); v != "" {
		cfg.Geocoder.URL = v
	}
	if v := os.Getenv("GEOCODER_BASE_URL"); v != "" {
		cfg.Geocoder.URL = v
	}
	if v := os.Getenv("GEOCODER_KEY"); v != "" {
		cfg.Geocoder.Key = v
		cfg.Geocoder.ApiKey = v
	}
	if v := os.Getenv("GEOCODER_API_KEY"); v != "" {
		cfg.Geocoder.Key = v
		cfg.Geocoder.ApiKey = v
	}
	if v := os.Getenv("GEOCODER_API-KEY"); v != "" {
		cfg.Geocoder.Key = v
		cfg.Geocoder.ApiKey = v
	}
	if v := os.Getenv("GEOCODER_ATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Geocoder.Attempts = n
		}
	}
	if v := os.Getenv("PLANNER_ENGINE"); v != "" {
		cfg.Planner.Engine = v
	}
	if v := os.Getenv("PLANNER_SEMAPHORE_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Planner.SemaphoreSize = n
		}
	}
	if v := os.Getenv("PLANNER_SEMAPHORE_ENABLE"); v != "" {
		cfg.Planner.SemaphoreEnable = v == "1" || v == "true"
	}
	if v := os.Getenv("HTTP_RATE_LIMIT_RPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.HTTP.RateLimit.RPS = n
		}
	}
	if v := os.Getenv("HTTP_RATE_LIMIT_BURST"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.HTTP.RateLimit.Burst = n
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}
	if v := os.Getenv("LOG_ADD_SOURCE"); v != "" {
		cfg.Log.AddSource = v == "1" || v == "true"
	}
	if v := os.Getenv("CITIES_PATH"); v != "" {
		cfg.Cities.Path = v
	}
	if v := os.Getenv("CITIES_DATA_PATH"); v != "" {
		cfg.Cities.Path = v
	}
	if v := os.Getenv("MOTIS_URL"); v != "" {
		cfg.Motis.URL = v
	}
	if v := os.Getenv("MOTIS_BASE"); v != "" {
		cfg.Motis.URL = v
	}
	if v := os.Getenv("VERIFICATION_CONFIDENCE_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Verification.ConfidenceThreshold = f
		}
	}
	if v := os.Getenv("VERIFICATION_DISTANCE_M"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Verification.DistanceM = n
		}
	}
	if v := os.Getenv("VERIFICATION_STRONG_DISTANCE_M"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Verification.StrongDistanceM = n
		}
	}
	if v := os.Getenv("VERIFICATION_LEV_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Verification.LevThreshold = f
		}
	}
	if v := os.Getenv("PRICING_DEFAULT_CURRENCY"); v != "" {
		cfg.Pricing.DefaultCurrency = v
	}
	if v := os.Getenv("DEFAULT_CURRENCY"); v != "" {
		cfg.Pricing.DefaultCurrency = v
	}
}

func applyPrefixedEnv(cfg *Config) {
	for _, e := range os.Environ() {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k, v := kv[0], kv[1]
		if !strings.HasPrefix(k, "TRAVELMCP__") {
			continue
		}
		path := strings.ToLower(strings.TrimPrefix(k, "TRAVELMCP__"))
		parts := strings.Split(path, "__")
		setByPath(cfg, parts, v)
	}
}

func setByPath(cfg *Config, parts []string, v string) {
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "http":
		if len(parts) == 2 && parts[1] == "addr" {
			cfg.HTTP.Addr = v
		}
		if len(parts) == 3 && parts[1] == "rate_limit" && parts[2] == "rps" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.HTTP.RateLimit.RPS = n
			}
		}
		if len(parts) == 3 && parts[1] == "rate_limit" && parts[2] == "burst" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.HTTP.RateLimit.Burst = n
			}
		}
	case "store":
		if len(parts) == 2 && parts[1] == "dsn" {
			cfg.Store.DSN = v
			cfg.Database.DSN = v
		}
		if len(parts) == 2 && parts[1] == "kind" {
			cfg.Store.Kind = v
		}
	case "cache":
		if len(parts) == 2 && parts[1] == "kind" {
			cfg.Cache.Kind = v
		}
		if len(parts) == 2 && parts[1] == "addr" {
			cfg.Cache.Addr = v
		}
	case "log":
		if len(parts) == 2 && parts[1] == "level" {
			cfg.Log.Level = v
		}
		if len(parts) == 2 && parts[1] == "format" {
			cfg.Log.Format = v
		}
		if len(parts) == 3 && parts[1] == "levels" {
			if cfg.Log.Levels == nil {
				cfg.Log.Levels = map[string]string{}
			}
			cfg.Log.Levels[parts[2]] = v
		}
	case "providers":
		if len(parts) == 2 && parts[1] == "enabled" {
			cfg.Providers.Enabled = splitCsv(v)
		}
	case "planner":
		if len(parts) == 2 && parts[1] == "engine" {
			cfg.Planner.Engine = v
		}
		if len(parts) == 2 && parts[1] == "semaphore_size" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.Planner.SemaphoreSize = n
			}
		}
		if len(parts) == 2 && parts[1] == "semaphore_enable" {
			cfg.Planner.SemaphoreEnable = v == "1" || v == "true"
		}
	case "auth":
		if len(parts) == 2 && parts[1] == "admin_token" {
			cfg.Auth.AdminToken = v
		}
	case "geocoder":
		if len(parts) == 2 && parts[1] == "kind" {
			cfg.Geocoder.Kind = v
		}
		if len(parts) == 2 && (parts[1] == "url" || parts[1] == "base_url") {
			cfg.Geocoder.URL = v
		}
		if len(parts) == 2 && (parts[1] == "key" || parts[1] == "api_key") {
			cfg.Geocoder.Key = v
			cfg.Geocoder.ApiKey = v
		}
		if len(parts) == 2 && parts[1] == "attempts" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.Geocoder.Attempts = n
			}
		}
	case "yandex":
		if len(parts) == 2 && parts[1] == "rasp_key" {
			cfg.Yandex.RaspKey = v
		}
		if len(parts) == 2 && (parts[1] == "geocode_key" || parts[1] == "api_key" || parts[1] == "key") {
			cfg.Yandex.GeocodeKey = v
		}
		if len(parts) == 2 && (parts[1] == "geocode_url" || parts[1] == "url" || parts[1] == "base_url") {
			cfg.Yandex.GeocodeURL = v
		}
		if len(parts) == 2 && parts[1] == "geocode_kind" {
			cfg.Yandex.GeocodeKind = v
		}
	case "nominatim":
		if len(parts) == 2 && (parts[1] == "url" || parts[1] == "base_url") {
			cfg.Nominatim.URL = v
		}
	case "cities":
		if len(parts) == 2 && parts[1] == "path" {
			cfg.Cities.Path = v
		}
	case "motis":
		if len(parts) == 2 && (parts[1] == "url" || parts[1] == "base_url") {
			cfg.Motis.URL = v
		}
	case "verification":
		if len(parts) == 2 && parts[1] == "confidence_threshold" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Verification.ConfidenceThreshold = f
			}
		}
		if len(parts) == 2 && parts[1] == "distance_m" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.Verification.DistanceM = n
			}
		}
		if len(parts) == 2 && parts[1] == "strong_distance_m" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.Verification.StrongDistanceM = n
			}
		}
		if len(parts) == 2 && parts[1] == "lev_threshold" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Verification.LevThreshold = f
			}
		}
	case "pricing":
		if len(parts) == 2 && parts[1] == "default_currency" {
			cfg.Pricing.DefaultCurrency = v
		}
	}
}

func splitCsv(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
