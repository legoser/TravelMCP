package logger

import (
	"log/slog"
	"os"
	"strings"

	"travelmcp/internal/config"
)

type Factory struct {
	global    slog.Level
	overrides map[string]slog.Level
	addSource bool
	format    string
}

func NewFactory(cfg config.Log) *Factory {
	f := &Factory{
		global:    parseLevel(cfg.Level),
		overrides: map[string]slog.Level{},
		addSource: cfg.AddSource,
		format:    cfg.Format,
	}
	for k, v := range cfg.Levels {
		key := strings.ToLower(strings.ReplaceAll(k, ".", "_"))
		key = strings.ReplaceAll(key, "-", "_")
		f.overrides[key] = parseLevel(v)
		f.overrides[strings.ToLower(k)] = parseLevel(v)
	}
	return f
}

func (f *Factory) For(module string) *slog.Logger {
	lvl := f.levelFor(module)
	opts := &slog.HandlerOptions{Level: lvl, AddSource: f.addSource || lvl == slog.LevelDebug}
	if strings.ToLower(f.format) == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

func (f *Factory) levelFor(module string) slog.Level {
	key := strings.ToLower(module)
	if lvl, ok := f.overrides[key]; ok {
		return lvl
	}
	norm := strings.ReplaceAll(key, ".", "_")
	if lvl, ok := f.overrides[norm]; ok {
		return lvl
	}
	for k, lvl := range f.overrides {
		if strings.HasPrefix(key, k+".") || strings.HasPrefix(norm, k) {
			return lvl
		}
	}
	return f.global
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
