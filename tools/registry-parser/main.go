package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

const generatorName = "registry-parser(mintrans-ru)"

func main() {
	var in, out, source, oldPath string
	var trustNK bool
	var churn float64
	flag.StringVar(&in, "input", "", "срез реестра (reestr.json)")
	flag.StringVar(&out, "out", "", "выходной flat_trips.json")
	flag.StringVar(&source, "source", "gov-registry", "значение meta.source (провайдер в providers.code)")
	flag.StringVar(&oldPath, "bootstrap", "", "опционально: старый срез для сравнения NK-стабильности")
	flag.Float64Var(&churn, "churn", 0.2, "порог churn для bootstrap-сравнения")
	flag.BoolVar(&trustNK, "trust-nk", false, "использовать reg-код как route_nk (иначе синтезированный ключ)")
	flag.Parse()

	if in == "" || out == "" {
		fmt.Fprintln(os.Stderr, "registry-parser: обязательны --input и --out")
		os.Exit(1)
	}
	raw, err := os.ReadFile(in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "registry-parser: чтение %s: %v\n", in, err)
		os.Exit(1)
	}
	ds, err := ParseDataset(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "registry-parser: контракт: %v\n", err)
		os.Exit(1)
	}

	if oldPath != "" {
		oldRaw, err := os.ReadFile(oldPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "registry-parser: чтение %s: %v\n", oldPath, err)
			os.Exit(1)
		}
		rep, err := CompareDatasets(oldRaw, raw, churn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "registry-parser: bootstrap: %v\n", err)
			os.Exit(1)
		}
		repJSON, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Fprintf(os.Stderr, "bootstrap: %s\n", repJSON)
		if rep.Alert {
			fmt.Fprintln(os.Stderr, "registry-parser: churn-алерт — не доверять external_route_code (trust_nk=false)")
			trustNK = false
		}
	}

	trips, stats := FlattenTrips(ds, source)
	payload := FlatPayload{
		Meta:  FlatMeta{Source: source, Snapshot: ds.Snapshot, Generator: generatorName},
		Stats: stats,
		Trips: trips,
	}
	for i := range payload.Trips {
		t := &payload.Trips[i]
		if trustNK {
			t.RouteNK = t.RouteReg
		} else {
			t.RouteNK = SyntheticKeyForRoute(t.RouteReg, t.RouteFrom, t.RouteTo, t.CarrierINN)
		}
	}
	blob, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "registry-parser: кодирование: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, blob, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "registry-parser: запись %s: %v\n", out, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "registry-parser: %d трипов → %s (source=%s, snapshot=%s)\n", len(trips), out, source, ds.Snapshot)
}
