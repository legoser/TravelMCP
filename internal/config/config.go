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

type Providers struct {
	Enabled   []string  `yaml:"enabled"`
	Intercity Intercity `yaml:"intercity"`
	GTFS      GTFS      `yaml:"gtfs"`
}

type Auth struct {
	AdminToken string `yaml:"admin_token"`
}

type Yandex struct {
	RaspKey    string `yaml:"rasp_key"`
	GeocodeKey string `yaml:"geocode_key"`
}

type Planner struct {
	Engine          string `yaml:"engine"`
	SemaphoreSize   int    `yaml:"semaphore_size"`
	SemaphoreEnable bool   `yaml:"semaphore_enable"`
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

type Config struct {
	HTTP      HTTP      `yaml:"http"`
	Store     Store     `yaml:"store"`
	Cache     Cache     `yaml:"cache"`
	Queue     Queue     `yaml:"queue"`
	Database  Database  `yaml:"database"`
	Providers Providers `yaml:"providers"`
	Auth      Auth      `yaml:"auth"`
	Yandex    Yandex    `yaml:"yandex"`
	Planner   Planner   `yaml:"planner"`
	Log       Log       `yaml:"log"`
	Telemetry Telemetry `yaml:"telemetry"`
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
		Planner: Planner{Engine: "csa", SemaphoreSize: runtime.NumCPU() * 2, SemaphoreEnable: true},
		Log:     Log{Level: "info", Format: "json", Levels: map[string]string{}},
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
