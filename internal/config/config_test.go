package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExample(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "config.example.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HTTP.Addr != ":8080" {
		t.Fatalf("addr = %q, want :8080", cfg.HTTP.Addr)
	}
	if len(cfg.Providers.Enabled) == 0 || cfg.Providers.Enabled[0] != "synth" {
		t.Fatalf("enabled = %v, want [synth]", cfg.Providers.Enabled)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":9999")
	t.Setenv("PROVIDERS_ENABLED", "synth, gtfs")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HTTP.Addr != ":9999" {
		t.Fatalf("addr = %q, want :9999", cfg.HTTP.Addr)
	}
	if len(cfg.Providers.Enabled) != 2 || cfg.Providers.Enabled[0] != "synth" || cfg.Providers.Enabled[1] != "gtfs" {
		t.Fatalf("enabled = %v, want [synth gtfs]", cfg.Providers.Enabled)
	}
}

func TestMissingNoError(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing file must not error (defaults + env), got %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg is nil")
	}
}

func TestUnsetEnv(t *testing.T) {
	if v := os.Getenv("HTTP_ADDR"); v != "" {
		t.Skip("HTTP_ADDR set in environment")
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HTTP.Addr != ":8080" {
		t.Fatalf("addr = %q, want default :8080", cfg.HTTP.Addr)
	}
}
