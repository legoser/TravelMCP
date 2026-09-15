package config

import (
	"runtime"
	"strings"
)

func Defaults() *Config {
	return &Config{
		HTTP: HTTP{
			Addr:              ":8080",
			ReadHeaderTimeout: "10s",
			ShutdownTimeout:   "10s",
			RateLimit:         RateLimit{RPS: 100, Burst: 200},
		},
		Store: Store{Kind: "memory"},
		Providers: Providers{
			Enabled: []string{},
		},
		Geocoder:  Geocoder{Kind: "", Attempts: 3, Limit: 5, MaxCalls: 0, TTLVerified: "2160h", TTLDisputed: "168h"},
		Yandex:    Yandex{RaspURL: "https://api.rasp.yandex.net/v3.0", GeocodeURL: "https://geocode-maps.yandex.ru/1.x", RaspCacheDir: "data/yandex/cache"},
		Nominatim: Nominatim{URL: "https://nominatim.openstreetmap.org"},
		Overpass: Overpass{
			URL:       "https://overpass-api.de/api/interpreter",
			MirrorURL: "https://overpass.openstreetmap.fr/api/interpreter",
		},
		Motis:   Motis{URL: "http://192.168.57.14:8077"},
		Planner: Planner{Engine: "csa", SemaphoreSize: runtime.NumCPU() * 2, SemaphoreEnable: true, MaxWalkMinutes: 30, MinTransferMinutes: 15, FlightCheckInMinutes: 120, AltWindowMinutes: 360, AltStepMinutes: 60, ArrivalWindowHours: 24, ArrivalStepMinutes: 30},
		Log:     Log{Level: "info", Format: "json", Levels: map[string]string{}, Loki: LokiLog{BatchSize: 100, BatchWait: "1s"}},
		Verification: Verification{
			ConfidenceThreshold: 0.6,
			DistanceM:           200,
			StrongDistanceM:     100,
			LevThreshold:        0.15,
			NameSimilarity:      0.8,
			ScoreMargin:         0.1,
			ScoreAmbiguity:      0.05,
			DensityThresholds:   map[string]DensityThreshold{},
		},
		Deduplication: Deduplication{DistanceM: 200},
		Pricing:       Pricing{DefaultCurrency: "RUB"},
		GTFS:          GTFS{TmpDir: "data/tmp/gtfs"},
		Sync:          Sync{LogDir: "data/logs", CoverageGate: 0, SkeletonChunkSize: 100, OsmPath: "data/osm/stations.json", YandexDumpPath: "data/yandex/cache/global_stations_list.json", SkeletonRegion: "Кемеровская область - Кузбасс", Bbox: "53.5,84.0,57.0,88.5", RegionBBoxes: defaultRegionBBoxes(), LegacyThreshold: 0.6, CoverageSoftScore: 0.4, TripsChurnThreshold: 0.2, TripsMaxSpeedKmh: 200, OverpassMax: 200, StagingExpiryDays: 14, JobsPollInterval: "5s", CleanupInterval: "24h", HygieneRetentionDays: 90, QuotaHistoryKeepDays: 7},
	}
}

// defaultRegionBBoxes — pilot-region bboxes (SFD): overpass collection must
// receive stations of the selected region, not a silent global bbox
// (issue #12: a "Republic of Altai" run with the static Kuzbass bbox).
func defaultRegionBBoxes() map[string]string {
	return map[string]string{
		"Кемеровская область - Кузбасс": "53.5,82.5,57.5,89.5",
		"Алтайский край":                "51.0,78.5,54.5,86.5",
		"Республика Алтай":              "49.0,83.5,52.7,89.5",
		"Новосибирская область":         "53.0,75.0,57.0,85.0",
		"Томская область":               "56.0,75.5,60.5,88.5",
		"Омская область":                "53.0,70.0,58.5,78.0",
		"Красноярский край":             "51.0,82.0,61.0,89.5",
		"Иркутская область":             "51.5,98.0,62.0,110.0",
		"Республика Хакасия":            "51.5,86.5,55.5,92.0",
		"Республика Тыва":               "49.5,87.5,53.5,100.0",
	}
}

// applyDefaults fills defaults for empty values after YAML and env.
// Empty strings/zeros mean "unset"; an explicit 0 where it is meaningful
// cannot be set via YAML — use env for such knobs.
func applyDefaults(cfg *Config) {
	if cfg.Nominatim.URL == "" {
		cfg.Nominatim.URL = "https://nominatim.openstreetmap.org"
	}
	if cfg.Overpass.URL == "" {
		cfg.Overpass.URL = "https://overpass-api.de/api/interpreter"
	}
	if cfg.Overpass.MirrorURL == "" {
		cfg.Overpass.MirrorURL = "https://overpass.openstreetmap.fr/api/interpreter"
	}
	if cfg.Yandex.GeocodeURL == "" {
		cfg.Yandex.GeocodeURL = "https://geocode-maps.yandex.ru/1.x"
	}
	if cfg.Yandex.RaspURL == "" {
		cfg.Yandex.RaspURL = "https://api.rasp.yandex.net/v3.0"
	}
	if cfg.Geocoder.Attempts <= 0 {
		cfg.Geocoder.Attempts = 3
	}
	if cfg.Deduplication.DistanceM == 0 {
		cfg.Deduplication.DistanceM = 200
	}
	if cfg.Verification.ConfidenceThreshold == 0 {
		cfg.Verification.ConfidenceThreshold = 0.6
	}
	if cfg.Verification.DistanceM == 0 {
		cfg.Verification.DistanceM = cfg.Deduplication.DistanceM
	}
	if cfg.Verification.StrongDistanceM == 0 {
		cfg.Verification.StrongDistanceM = cfg.Deduplication.DistanceM / 2
	}
	if cfg.Verification.LevThreshold == 0 {
		cfg.Verification.LevThreshold = 0.15
	}
	if cfg.Verification.NameSimilarity == 0 {
		cfg.Verification.NameSimilarity = 0.8
	}
	if cfg.Verification.ScoreMargin == 0 {
		cfg.Verification.ScoreMargin = 0.1
	}
	if cfg.Verification.ScoreAmbiguity == 0 {
		cfg.Verification.ScoreAmbiguity = 0.05
	}
	if cfg.Verification.DensityThresholds == nil {
		cfg.Verification.DensityThresholds = map[string]DensityThreshold{}
	}
	if cfg.Geocoder.Limit <= 0 {
		cfg.Geocoder.Limit = 5
	}
	if cfg.Geocoder.Limit > 10 {
		cfg.Geocoder.Limit = 10
	}
	if cfg.Geocoder.TTLVerified == "" {
		cfg.Geocoder.TTLVerified = "2160h"
	}
	if cfg.Geocoder.TTLDisputed == "" {
		cfg.Geocoder.TTLDisputed = "168h"
	}
	if cfg.GTFS.TmpDir == "" {
		cfg.GTFS.TmpDir = "data/tmp/gtfs"
	}
	if cfg.Sync.LogDir == "" {
		cfg.Sync.LogDir = "data/logs"
	}
	if cfg.Sync.SkeletonChunkSize <= 0 {
		cfg.Sync.SkeletonChunkSize = 100
	}
	if cfg.Sync.SkeletonChunkSize > 100 {
		cfg.Sync.SkeletonChunkSize = 100
	}
	if cfg.Sync.OsmPath == "" {
		cfg.Sync.OsmPath = "data/osm/stations.json"
	}
	if cfg.Sync.YandexDumpPath == "" {
		cfg.Sync.YandexDumpPath = "data/yandex/cache/global_stations_list.json"
	}
	if cfg.Sync.SkeletonRegion == "" {
		cfg.Sync.SkeletonRegion = "Кемеровская область - Кузбасс"
	}
	if cfg.Sync.Bbox == "" {
		cfg.Sync.Bbox = "53.5,84.0,57.0,88.5"
	} else if strings.EqualFold(cfg.Sync.Bbox, "none") {
		cfg.Sync.Bbox = ""
	}
	if cfg.Sync.LegacyThreshold == 0 {
		cfg.Sync.LegacyThreshold = 0.6
	}
	if cfg.Sync.CoverageSoftScore == 0 {
		cfg.Sync.CoverageSoftScore = 0.4
	}
	if cfg.Sync.TripsChurnThreshold == 0 {
		cfg.Sync.TripsChurnThreshold = 0.2
	}
	if cfg.Sync.TripsMaxSpeedKmh == 0 {
		cfg.Sync.TripsMaxSpeedKmh = 200
	}
	if cfg.Sync.OverpassMax == 0 {
		cfg.Sync.OverpassMax = 200
	}
	if cfg.Pricing.DefaultCurrency == "" {
		cfg.Pricing.DefaultCurrency = "RUB"
	}
	if cfg.Planner.ArrivalWindowHours <= 0 {
		cfg.Planner.ArrivalWindowHours = 24
	}
	if cfg.Planner.ArrivalStepMinutes <= 0 {
		cfg.Planner.ArrivalStepMinutes = 30
	}
	if cfg.Sync.HygieneRetentionDays <= 0 {
		cfg.Sync.HygieneRetentionDays = 90
	}
	if cfg.Sync.QuotaHistoryKeepDays <= 0 {
		cfg.Sync.QuotaHistoryKeepDays = 7
	}
}
