package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
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
	"travelmcp/internal/jobs"
	"travelmcp/internal/logger"
	"travelmcp/internal/pricing"
	"travelmcp/internal/providers"
	"travelmcp/internal/server"
	"travelmcp/internal/store"
	_ "travelmcp/internal/store/memory"
	_ "travelmcp/internal/store/postgres"
	syncpkg "travelmcp/internal/sync"
	"travelmcp/internal/telemetry"

	_ "travelmcp/internal/adapters/nominatim"
	_ "travelmcp/internal/adapters/overpass"
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
	pricing.Configure(cfg.Pricing.DefaultCurrency)
	logger.Info("config loaded", "path", configPath, "addr", cfg.HTTP.Addr, "providers", strings.Join(cfg.Providers.Enabled, ","), "dsn", maskDSN(cfg.Database.DSN), "reestr", cfg.Providers.Intercity.ReestrPath, "log_level", cfg.Log.Level, "log_format", cfg.Log.Format, "log_levels", cfg.Log.Levels, "pricing_currency", cfg.Pricing.DefaultCurrency)

	httpLogger := factory.For("http")

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
			for _, p := range []string{"yandex", "nominatim", "motis", "mintrans", "gtfs"} {
				_ = st.SetQuotaLimit(context.Background(), p, 1000)
			}
			if qs, err := st.ListQuotas(context.Background()); err == nil {
				logger.Info("quotas loaded", "count", len(qs))
			}
			for _, id := range cfg.Providers.Enabled {
				if id == providers.IntercityID {
					logger.Warn("legacy mintrans import disabled: канон пишут skeleton-sync + trips-sync, сервер только читает", "provider", id, "reestr", cfg.Providers.Intercity.ReestrPath)
				}
			}
		}
	} else {
		logger.Info("storage disabled", "reason", "DATABASE_DSN empty")
	}
	if st != nil {
		go func() {
			workerLogger := factory.For("jobs")
			w := newJobsWorker(st, cfg, workerLogger)
			workerLogger.Info("jobs worker started")
			w.Run(context.Background(), 5*time.Second)
		}()
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

func newJobsWorker(st store.Store, cfg *config.Config, l *slog.Logger) *jobs.Worker {
	w := jobs.NewWorker(st, l)
	w.Register("import_gtfs", func(ctx context.Context, job store.JobRow) error {
		l.Info("handling import_gtfs", "id", job.ID, "payload", job.Payload)
		var p map[string]any
		_ = json.Unmarshal([]byte(job.Payload), &p)
		path, _ := p["path"].(string)
		tmpDir, _ := p["tmp_dir"].(string)
		if path == "" {
			l.Warn("gtfs import: no path, nothing to do (upload file first)")
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			l.Error("gtfs open failed", "path", path, "err", err)
			return err
		}
		defer f.Close()
		fi, _ := f.Stat()
		l.Debug("gtfs file", "path", path, "size", fi.Size())
		zr, err := zip.NewReader(f, fi.Size())
		if err != nil {
			l.Error("gtfs zip invalid", "err", err)
			return err
		}
		l.Info("gtfs zip validated", "files", len(zr.File), "path", path)
		for _, zf := range zr.File {
			l.Debug("gtfs entry", "name", zf.Name, "size", zf.UncompressedSize64)
		}
		if tmpDir != "" {
			unpacked := tmpDir + "/unpacked"
			_ = os.MkdirAll(unpacked, 0755)
			for _, zf := range zr.File {
				if zf.FileInfo().IsDir() {
					continue
				}
				clean := zf.Name
				if clean == "" || strings.Contains(clean, "..") {
					continue
				}
				dest := unpacked + "/" + clean
				if !strings.HasPrefix(dest, unpacked) {
					continue
				}
				_ = os.MkdirAll(dest[:strings.LastIndex(dest, "/")+1], 0755)
				rc, err := zf.Open()
				if err != nil {
					continue
				}
				out, err := os.Create(dest)
				if err != nil {
					rc.Close()
					continue
				}
				_, _ = out.ReadFrom(rc)
				_ = out.Close()
				_ = rc.Close()
			}
			l.Info("gtfs unpacked", "tmp_dir", tmpDir, "unpacked", unpacked)
		}
		l.Info("gtfs import done (MVP: validated only, canonical import via AdaptedRecord в фазе 6)")
		if tmpDir != "" {
			if err := os.RemoveAll(tmpDir); err != nil {
				l.Warn("gtfs tmp cleanup failed", "tmp_dir", tmpDir, "err", err)
			} else {
				l.Info("gtfs tmp cleaned", "tmp_dir", tmpDir)
			}
		}
		return nil
	})
	w.Register("sync_mintrans", func(_ context.Context, job store.JobRow) error {
		l.Warn("sync_mintrans disabled: legacy-импорт вырезан, канон пишут skeleton-sync + trips-sync", "id", job.ID)
		return errors.New("sync_mintrans отключён: legacy-импорт вырезан, канон пишут skeleton-sync + trips-sync")
	})
	w.Register("sync_rail", func(ctx context.Context, job store.JobRow) error {
		l.Info("handling sync_rail", "id", job.ID)
		return nil
	})
	w.Register("cleanup", func(ctx context.Context, job store.JobRow) error {
		l.Info("handling cleanup (staging expiry §5.3 + hygiene sweep Фаза 5)", "id", job.ID)
		days := 14
		retentionDays := 90
		var p map[string]any
		if json.Unmarshal([]byte(job.Payload), &p) == nil {
			if d, ok := p["staging_expiry_days"].(float64); ok && d > 0 {
				days = int(d)
			}
			if d, ok := p["attribute_retention_days"].(float64); ok && d > 0 {
				retentionDays = int(d)
			}
		}
		if cfg != nil && cfg.Sync.StagingExpiryDays > 0 {
			days = cfg.Sync.StagingExpiryDays
		}
		ex, ok := st.(syncpkg.StagingExpirer)
		if !ok {
			return errors.New("cleanup: стор не поддерживает staging expiry")
		}
		if err := syncpkg.HandleCleanupJob(ctx, ex, days, l); err != nil {
			return err
		}
		// freshness-sweep Фазы 5 (§2): finalize/GC attribute_state +
		// possible_merge-аудит + recompute transport_types; сбой sweep
		// не роняет staging-expiry (шаги независимы, частичность в логах).
		if sw, ok := st.(syncpkg.AttributeHygieneSweeper); ok {
			if rq, ok2 := st.(syncpkg.ReviewQueueWriter); ok2 {
				if _, err := syncpkg.RunHygieneSweep(ctx, sw, rq, time.Duration(retentionDays)*24*time.Hour, l); err != nil {
					l.Warn("hygiene sweep partial failure (staging expiry уже применён)", "err", err)
					return err
				}
			}
		} else {
			l.Info("cleanup: стор без hygiene-sweep (memory в тестах) — пропущено")
		}
		return nil
	})
	return w
}
