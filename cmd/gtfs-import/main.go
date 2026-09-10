package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/logger"
	"travelmcp/internal/store"
	_ "travelmcp/internal/store/memory"
	_ "travelmcp/internal/store/postgres"
	"travelmcp/internal/sync"
)

// appVersion → sync.LogicVersionID() (plan_id от семантической версии, план §4.3).

func main() {
	var configPath, feedPath, tag string
	flag.StringVar(&configPath, "config", "configs/config.dev.yaml", "путь к YAML-конфигу")
	flag.StringVar(&feedPath, "feed", "", "путь к GTFS zip-фиду")
	flag.StringVar(&tag, "tag", "gtfs-import", "тег прогона")
	flag.Parse()
	if feedPath == "" {
		slog.Error("feed пуст: -feed path/to/gtfs.zip")
		os.Exit(1)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load failed: %v\n", err)
		os.Exit(1)
	}
	factory := logger.NewFactory(cfg.Log)
	slog.SetDefault(factory.For("main"))
	ctx := context.Background()
	st, err := store.New(ctx, cfg.Store.DSN)
	if err != nil {
		slog.Error("store open failed", "error", err)
		os.Exit(1)
	}
	if st == nil {
		slog.Error("store dsn empty")
		os.Exit(1)
	}
	defer func() { _ = st.Close() }()
	gst, ok := st.(sync.GtfsImportStore)
	if !ok {
		slog.Error("store lacks gtfs-import methods")
		os.Exit(1)
	}

	f, err := os.Open(feedPath)
	if err != nil {
		slog.Error("feed open failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = f.Close() }()
	stt, err := f.Stat()
	if err != nil {
		slog.Error("feed stat failed", "error", err)
		os.Exit(1)
	}
	zr, err := zip.NewReader(f, stt.Size())
	if err != nil {
		slog.Error("feed zip failed", "error", err)
		os.Exit(1)
	}
	sum := sha256.Sum256([]byte(feedPath))
	inputSHA := hex.EncodeToString(sum[:])
	planID := sync.ComputePlanID(sync.LogicVersionID(), "", []string{inputSHA})
	runID, err := gst.CreateSyncRun(ctx, store.SyncRunRow{PlanID: planID, Kind: "gtfs_import", InputSHA: inputSHA, Tag: tag})
	if err != nil {
		slog.Error("begin run failed", "error", err)
		os.Exit(1)
	}
	stats, err := sync.ImportGtfsFeed(ctx, gst, zr)
	if err != nil {
		_ = gst.FinishSyncRun(ctx, runID, "dead", `{"error":`+mustJSON(err.Error())+`}`)
		slog.Error("gtfs import failed", "run_id", runID, "error", err)
		os.Exit(1)
	}
	raw, _ := json.Marshal(stats)
	if err := gst.FinishSyncRun(ctx, runID, "done", string(raw)); err != nil {
		slog.Error("finish run failed", "error", err)
		os.Exit(1)
	}
	slog.Info("gtfs import done", "run_id", runID, "stats", string(raw),
		"elapsed", time.Duration(stats.ElapsedMs)*time.Millisecond)
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
