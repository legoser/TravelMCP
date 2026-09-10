package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"travelmcp/internal/gtfs"
	"travelmcp/internal/model"
	store "travelmcp/internal/store"
	"travelmcp/internal/store/memory"
)

// gtfs-validate — CI-gate производственного GTFS (план §3.3/§6):
// (1) сборка zip из memory-фикстуры через тот же компилятор с hard-gate
// полноты provenance; (2) прогон MobilityData gtfs-validator на собранном
// zip (бинарник передаётся аргументом; без него — только gate).
func main() {
	validatorBin := ""
	if len(os.Args) > 1 {
		validatorBin = os.Args[1]
	}
	m := memory.NewMemoryStore()
	ctx := context.Background()

	// Фикстура: полный путь конвейера — route + trip + stop с provenance.
	routeID, err := m.UpsertRoute(ctx, store.RouteRow{ProviderID: "mintrans", ExternalRouteCode: "42", ShortName: "42", LongName: "Тестовый маршрут", Mode: "bus"})
	if err != nil {
		slog.Error("fixture route failed", "error", err)
		os.Exit(1)
	}
	tripID, err := m.UpsertTrip(ctx, store.TripRow{RouteID: routeID, ProviderID: "mintrans", ExternalTripCode: "fwd-1"})
	if err != nil {
		slog.Error("fixture trip failed", "error", err)
		os.Exit(1)
	}
	for _, p := range []model.Provenance{
		{EntityType: "route", EntityID: routeID, Source: "mintrans", Confidence: 1, ObservedAt: time.Now(), Channel: model.ChannelLocalFile},
		{EntityType: "trip", EntityID: tripID, Source: "mintrans", Confidence: 1, ObservedAt: time.Now(), Channel: model.ChannelLocalFile},
	} {
		if err := m.SaveProvenance(ctx, p); err != nil {
			slog.Error("fixture provenance failed", "error", err)
			os.Exit(1)
		}
	}

	comp := gtfs.NewCompiler("f-ru")
	data, err := comp.BuildPerRegionFromStore(ctx, m, "fixture", time.Now())
	if err != nil {
		slog.Error("provenance gate / compile failed", "error", err)
		os.Exit(1)
	}

	outDir := "bin"
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		slog.Error("mkdir failed", "error", err)
		os.Exit(1)
	}
	zipPath := filepath.Join(outDir, gtfs.ArchiveName("fixture"))
	if err := os.WriteFile(zipPath, data, 0o644); err != nil {
		slog.Error("zip write failed", "error", err)
		os.Exit(1)
	}
	slog.Info("fixture zip built", "path", zipPath, "bytes", len(data))

	if validatorBin == "" {
		slog.Warn("gtfs-validator не задан: прогнан только provenance-gate (передай путь к бинарнику аргументом)")
		return
	}
	if _, err := os.Stat(validatorBin); err != nil {
		slog.Error("gtfs-validator бинарник недоступен", "path", validatorBin, "error", err)
		os.Exit(1)
	}
	cmd := exec.Command(validatorBin, "-x", zipPath, "-o", outDir)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		fmt.Println(out.String())
		slog.Error("gtfs-validator failed", "error", err)
		os.Exit(1)
	}
	fmt.Println(out.String())
	slog.Info("gtfs-validator ok", "zip", zipPath)
}
