package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/geocoder"
	"travelmcp/internal/httpx"
	"travelmcp/internal/logger"
	"travelmcp/internal/providers"
	"travelmcp/internal/server"
	"travelmcp/internal/store"
	"travelmcp/internal/telemetry"

	_ "travelmcp/internal/adapters/nominatim"
	_ "travelmcp/internal/adapters/yandex"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "configs/config.example.yaml", "путь к файлу конфигурации (YAML)")
	flag.Parse()

	totalStart := time.Now()
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})).Error("config load failed", "error", err)
		os.Exit(1)
	}
	factory := logger.NewFactory(cfg.Log)
	logger := factory.For("main")
	slog.SetDefault(logger)
	logger.Info("config loaded", "path", configPath, "addr", cfg.HTTP.Addr, "providers", strings.Join(cfg.Providers.Enabled, ","), "dsn", maskDSN(cfg.Database.DSN), "reestr", cfg.Providers.Intercity.ReestrPath, "log_level", cfg.Log.Level, "log_format", cfg.Log.Format, "log_levels", cfg.Log.Levels)

	httpLogger := factory.For("http")
	httpxLogger := factory.For("httpx")
	httpxClient := httpx.New(httpxLogger, "geocoder")
	if g, err := geocoder.New(*cfg, httpxClient); err != nil {
		logger.Warn("geocoder init", "error", err)
	} else {
		_ = g
		logger.Info("geocoder ready", "attempts", cfg.Geocoder.Attempts, "preferred", cfg.Geocoder.Kind, "registered", geocoder.RegisteredKinds())
	}

	metrics := telemetry.New()
	regStart := time.Now()
	intercityLogger := factory.For("providers.intercity")
	registry := providers.NewRegistryWithLogger(cfg.Providers.Enabled, cfg.Providers.Intercity.ReestrPath, intercityLogger)
	logger.Info("provider registry initialized", "enabled", cfg.Providers.Enabled, "elapsed", time.Since(regStart).String())

	var st store.Store
	if cfg.Database.DSN != "" {
		s := time.Now()
		logger.Info("storage init", "dsn", maskDSN(cfg.Database.DSN))
		var err error
		st, err = store.New(context.Background(), cfg.Database.DSN)
		if err != nil {
			logger.Error("storage init failed", "error", err)
			os.Exit(1)
		}
		logger.Info("storage connected", "elapsed_ms", time.Since(s).Milliseconds())
		if st != nil {
			ms := time.Now()
			logger.Info("db migration started")
			if err := st.Migrate(context.Background()); err != nil {
				logger.Error("db migration failed", "error", err)
				os.Exit(1)
			}
			logger.Info("db migration completed", "elapsed_ms", time.Since(ms).Milliseconds())
			for _, id := range cfg.Providers.Enabled {
				if id == providers.IntercityID {
					is := time.Now()
					logger.Info("import started", "provider", id, "path", cfg.Providers.Intercity.ReestrPath)
					if err := store.ImportIntercity(context.Background(), st, cfg.Providers.Intercity.ReestrPath, logger); err != nil {
						logger.Warn("import failed", "provider", id, "error", err, "elapsed_ms", time.Since(is).Milliseconds())
					} else {
						logger.Info("import completed", "provider", id, "path", cfg.Providers.Intercity.ReestrPath, "elapsed_ms", time.Since(is).Milliseconds())
					}
				}
			}
		}
	} else {
		logger.Info("storage disabled", "reason", "DATABASE_DSN empty")
	}

	readTimeout := parseDuration(cfg.HTTP.ReadHeaderTimeout, 10*time.Second)
	shutdownTimeout := parseDuration(cfg.HTTP.ShutdownTimeout, 10*time.Second)
	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           server.NewWithStore(cfg, httpLogger, metrics, registry, st),
		ReadHeaderTimeout: readTimeout,
	}
	_ = shutdownTimeout
	_ = readTimeout
	logger.Info("http setup completed", "total_startup_ms", time.Since(totalStart).Milliseconds())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("mcp-server started", "addr", cfg.HTTP.Addr, "providers", cfg.Providers.Enabled)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}
}

func maskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	if strings.HasPrefix(dsn, "postgres") {
		u, err := url.Parse(dsn)
		if err == nil && u.User != nil {
			u.User = url.UserPassword(u.User.Username(), "***")
			return u.String()
		}
	}
	if strings.Contains(dsn, "@") {
		return "***"
	}
	return dsn
}

func parseDuration(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return def
}

// deprecated: use logger.NewFactory
func newLogger(level, format string, addSource bool) *slog.Logger {
	return logger.NewFactory(config.Log{Level: level, Format: format, AddSource: addSource}).For("main")
}
