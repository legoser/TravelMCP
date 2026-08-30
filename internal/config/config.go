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

type Providers struct {
	Enabled []string `yaml:"enabled"`
}

type Auth struct {
	AdminToken string `yaml:"admin_token"`
}

type Config struct {
	HTTP      HTTP      `yaml:"http"`
	Database  Database  `yaml:"database"`
	Providers Providers `yaml:"providers"`
	Auth      Auth      `yaml:"auth"`
}

func Defaults() *Config {
	return &Config{
		HTTP:     HTTP{Addr: ":8080"},
		Database: Database{},
		Providers: Providers{
			Enabled: []string{"synth"},
		},
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
