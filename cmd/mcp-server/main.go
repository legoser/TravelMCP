package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("config load failed", "error", err)
		os.Exit(1)
	}

	metrics := telemetry.New()
	registry := providers.NewRegistryWith(cfg.Providers.Enabled, cfg.Providers.Intercity.ReestrPath)

	var st store.Store
	if cfg.Database.DSN != "" {
		var err error
		st, err = store.New(context.Background(), cfg.Database.DSN)
		if err != nil {
			logger.Error("store init failed", "error", err)
			os.Exit(1)
		}
		if st != nil {
			if err := st.Migrate(context.Background()); err != nil {
				logger.Error("store migrate failed", "error", err)
				os.Exit(1)
			}
			for _, id := range cfg.Providers.Enabled {
				if id == providers.IntercityID {
					if err := store.ImportIntercity(context.Background(), st, cfg.Providers.Intercity.ReestrPath); err != nil {
						logger.Warn("intercity import failed", "error", err)
					} else {
						logger.Info("intercity imported", "path", cfg.Providers.Intercity.ReestrPath)
					}
				}
			}
		}
	}

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           server.NewWithStore(cfg, logger, metrics, registry, st),
		ReadHeaderTimeout: 10 * time.Second,
	}

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
