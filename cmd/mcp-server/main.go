package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/providers"
	"travelmcp/internal/server"
	"travelmcp/internal/store"
	"travelmcp/internal/telemetry"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "configs/config.example.yaml", "путь к файлу конфигурации (YAML)")
	flag.Parse()

	totalStart := time.Now()
	cfg, err := config.Load(configPath)
	if err != nil {
		slog.New(slog.NewJSONHandler(os.Stdout, nil)).Error("config load failed", "error", err)
		os.Exit(1)
	}
	logger := newLogger(cfg.Log.Level)
	logger.Info("конфиг загружен", "path", configPath, "addr", cfg.HTTP.Addr, "providers", strings.Join(cfg.Providers.Enabled, ","), "dsn", cfg.Database.DSN, "reestr", cfg.Providers.Intercity.ReestrPath, "log_level", cfg.Log.Level)

	metrics := telemetry.New()
	regStart := time.Now()
	registry := providers.NewRegistryWith(cfg.Providers.Enabled, cfg.Providers.Intercity.ReestrPath)
	logger.Info("реестр провайдеров инициализирован", "enabled", cfg.Providers.Enabled, "elapsed", time.Since(regStart).String())

	var st store.Store
	if cfg.Database.DSN != "" {
		s := time.Now()
		logger.Info("инициализация хранилища", "dsn", cfg.Database.DSN)
		var err error
		st, err = store.New(context.Background(), cfg.Database.DSN)
		if err != nil {
			logger.Error("store init failed", "error", err)
			os.Exit(1)
		}
		logger.Info("хранилище подключено", "elapsed", time.Since(s).String())
		if st != nil {
			ms := time.Now()
			logger.Info("миграция схемы БД")
			if err := st.Migrate(context.Background()); err != nil {
				logger.Error("store migrate failed", "error", err)
				os.Exit(1)
			}
			logger.Info("миграция завершена", "elapsed", time.Since(ms).String())
			for _, id := range cfg.Providers.Enabled {
				if id == providers.IntercityID {
					is := time.Now()
					logger.Info("импорт intercity", "path", cfg.Providers.Intercity.ReestrPath)
					if err := store.ImportIntercity(context.Background(), st, cfg.Providers.Intercity.ReestrPath); err != nil {
						logger.Warn("intercity import failed", "error", err, "elapsed", time.Since(is).String())
					} else {
						logger.Info("intercity импортирован", "path", cfg.Providers.Intercity.ReestrPath, "elapsed", time.Since(is).String())
					}
				}
			}
		}
	} else {
		logger.Info("хранилище не настроено (DATABASE_DSN пуст) — работа без БД")
	}

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           server.NewWithStore(cfg, logger, metrics, registry, st),
		ReadHeaderTimeout: 10 * time.Second,
	}
	logger.Info("подготовка HTTP завершена", "total_startup", time.Since(totalStart).String())

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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

func newLogger(level string) *slog.Logger {
	lvl := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl, AddSource: lvl == slog.LevelDebug}))
}
