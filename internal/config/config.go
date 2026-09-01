package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type HTTP struct {
	Addr string `yaml:"addr"`
}

type Database struct {
	DSN string `yaml:"dsn"`
}

type Intercity struct {
	ReestrPath string `yaml:"reestr_path"`
}

type Providers struct {
	Enabled   []string  `yaml:"enabled"`
	Intercity Intercity `yaml:"intercity"`
}

type Auth struct {
	AdminToken string `yaml:"admin_token"`
}

type Yandex struct {
	RaspKey    string `yaml:"rasp_key"`
	GeocodeKey string `yaml:"geocode_key"`
}

type Planner struct {
	Engine string `yaml:"engine"`
}

type Log struct {
	Level     string `yaml:"level"`
	Format    string `yaml:"format"`
	AddSource bool   `yaml:"add_source"`
}

type Config struct {
	HTTP      HTTP      `yaml:"http"`
	Database  Database  `yaml:"database"`
	Providers Providers `yaml:"providers"`
	Auth      Auth      `yaml:"auth"`
	Yandex    Yandex    `yaml:"yandex"`
	Planner   Planner   `yaml:"planner"`
	Log       Log       `yaml:"log"`
}

func Defaults() *Config {
	return &Config{
		HTTP:     HTTP{Addr: ":8080"},
		Database: Database{},
		Providers: Providers{
			Enabled: []string{},
			Intercity: Intercity{
				ReestrPath: "data/reestr/regions.json",
			},
		},
		Planner: Planner{Engine: "csa"},
		Log:     Log{Level: "info", Format: "json"},
	}
}

func Load(path string) (*Config, error) {
	cfg := Defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
		if err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("config: parse %s: %w", path, err)
			}
		}
	}

	if err := LoadDotenv(); err != nil {
		return nil, fmt.Errorf("config: dotenv: %w", err)
	}
	applyEnv(cfg)
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("HTTP_ADDR"); v != "" {
		cfg.HTTP.Addr = v
	}
	if v := os.Getenv("DATABASE_DSN"); v != "" {
		cfg.Database.DSN = v
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
