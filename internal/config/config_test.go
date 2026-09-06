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
	if len(cfg.Providers.Enabled) != 0 {
		t.Fatalf("enabled = %v, want [] (нет продакшн-источников по умолчанию)", cfg.Providers.Enabled)
	}
	if cfg.Sync.LogDir == "" {
		t.Fatal("sync.log_dir пуст после загрузки примера (ожидался data/logs через default)")
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":9999")
	t.Setenv("PROVIDERS_ENABLED", "synth, gtfs")
	t.Setenv("SYNC_LOG_DIR", "/tmp/sync-logs")
	t.Setenv("SYNC_COVERAGE_GATE", "0.7")
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
	if cfg.Sync.LogDir != "/tmp/sync-logs" {
		t.Fatalf("sync.log_dir = %q, want /tmp/sync-logs", cfg.Sync.LogDir)
	}
	if cfg.Sync.CoverageGate != 0.7 {
		t.Fatalf("sync.coverage_gate = %v, want 0.7", cfg.Sync.CoverageGate)
	}
}

func TestScorepairDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Verification.ScoreMargin != 0.1 {
		t.Fatalf("score_margin = %v, want 0.1", cfg.Verification.ScoreMargin)
	}
	if cfg.Verification.ScoreAmbiguity != 0.05 {
		t.Fatalf("score_ambiguity = %v, want 0.05", cfg.Verification.ScoreAmbiguity)
	}
	if cfg.Sync.TripsMaxSpeedKmh != 200 {
		t.Fatalf("trips_max_speed_kmh = %v, want 200", cfg.Sync.TripsMaxSpeedKmh)
	}
	t.Setenv("VERIFICATION_SCORE_MARGIN", "0.2")
	t.Setenv("VERIFICATION_SCORE_AMBIGUITY", "0.07")
	t.Setenv("SYNC_TRIPS_MAX_SPEED_KMH", "250")
	cfg, err = Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Verification.ScoreMargin != 0.2 {
		t.Fatalf("score_margin = %v, want 0.2", cfg.Verification.ScoreMargin)
	}
	if cfg.Verification.ScoreAmbiguity != 0.07 {
		t.Fatalf("score_ambiguity = %v, want 0.07", cfg.Verification.ScoreAmbiguity)
	}
	if cfg.Sync.TripsMaxSpeedKmh != 250 {
		t.Fatalf("trips_max_speed_kmh = %v, want 250", cfg.Sync.TripsMaxSpeedKmh)
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

func TestParseDotenv(t *testing.T) {
	data := []byte("# комментарий\nA=1\nexport B=\"two words\"\nC='x y'\nD=\nE = 1\nG без равно\n\n")
	got, err := parseDotenv(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got["A"] != "1" || got["B"] != "two words" || got["C"] != "x y" || got["D"] != "" || got["E"] != "1" {
		t.Fatalf("parsed = %#v", got)
	}
	if _, ok := got["G"]; ok {
		t.Fatalf("G без '=' должна быть пропущена, got %#v", got)
	}
}

func TestDotenvFillsGapButEnvWins(t *testing.T) {
	t.Setenv("YANDEX_RASP_KEY", "from-env")
	if err := LoadDotenv(); err != nil {
		t.Fatalf("dotenv: %v", err)
	}
	if v := os.Getenv("YANDEX_RASP_KEY"); v != "from-env" {
		t.Fatalf("env must win over .env, got %q", v)
	}
	vals, err := parseDotenv([]byte("YANDEX_GEOCODE_KEY=\"k2\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if vals["YANDEX_GEOCODE_KEY"] != "k2" {
		t.Fatalf("parse: %#v", vals)
	}
}
