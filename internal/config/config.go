package config

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type HTTP struct {
	Addr               string               `yaml:"addr"`
	ReadHeaderTimeout  string               `yaml:"read_header_timeout"`
	ShutdownTimeout    string               `yaml:"shutdown_timeout"`
	RateLimit          RateLimit            `yaml:"rate_limit"`
	RateLimitOverrides map[string]RateLimit `yaml:"rate_limit_overrides"`
}

type RateLimit struct {
	RPS   int `yaml:"rps"`
	Burst int `yaml:"burst"`
}

type Store struct {
	Kind string `yaml:"kind"`
	DSN  string `yaml:"dsn"`
}

type Cache struct {
	Kind string `yaml:"kind"`
	Addr string `yaml:"addr"`
	TTL  string `yaml:"ttl"`
}

type Queue struct {
	Kind string `yaml:"kind"`
	URL  string `yaml:"url"`
}

type GTFS struct {
	Path   string `yaml:"path"`
	TmpDir string `yaml:"tmp_dir"`
}

// Geocoder — всё про геокодинг в одной секции: выбор провайдера
// (kind/attempts/limit), лимит квоты api_quotas (max_calls) и TTL
// geocode_cache (ttl_verified/ttl_disputed).
type Geocoder struct {
	Kind     string `yaml:"kind"`
	Attempts int    `yaml:"attempts"`
	Limit    int    `yaml:"limit"`
	// MaxCalls — лимит квоты api_quotas для геокодеров (nominatim reverse,
	// overpass enrich). 0 = взять дефолт вызывающей стороны.
	MaxCalls    int    `yaml:"max_calls"`
	TTLVerified string `yaml:"ttl_verified"`
	TTLDisputed string `yaml:"ttl_disputed"`
}

type Motis struct {
	URL string `yaml:"url"`
}

type Providers struct {
	Enabled []string `yaml:"enabled"`
	GTFS    GTFS     `yaml:"gtfs"`
}

type Auth struct {
	AdminToken string `yaml:"admin_token"`
}

type Yandex struct {
	RaspKey      string `yaml:"rasp_key"`
	RaspURL      string `yaml:"rasp_url"`
	GeocodeKey   string `yaml:"geocode_key"`
	GeocodeURL   string `yaml:"geocode_url"`
	RaspCacheDir string `yaml:"rasp_cache_dir"`
	// RaspQuotaLimit — суточный лимит квоты api_quotas('yandex_rasp').
	// 0 = дефолт адаптера (DefaultRaspQuotaLimit, 500). Единая точка
	// ответственности за лимит (AGENTS §3.7): адаптер при 0 подставляет
	// свой дефолт, конфиг переопределяет.
	RaspQuotaLimit int `yaml:"rasp_quota_limit"`
}

type Nominatim struct {
	URL string `yaml:"url"`
}

type Overpass struct {
	URL       string `yaml:"url"`
	MirrorURL string `yaml:"mirror_url"`
}

type Planner struct {
	Engine          string `yaml:"engine"`
	SemaphoreSize   int    `yaml:"semaphore_size"`
	SemaphoreEnable bool   `yaml:"semaphore_enable"`
	MaxWalkMinutes  int    `yaml:"max_walk_minutes"`
}

type Deduplication struct {
	DistanceM int `yaml:"distance_m"`
}

type Verification struct {
	ConfidenceThreshold float64                     `yaml:"confidence_threshold"`
	DistanceM           int                         `yaml:"distance_m"`
	StrongDistanceM     int                         `yaml:"strong_distance_m"`
	LevThreshold        float64                     `yaml:"lev_threshold"`
	NameSimilarity      float64                     `yaml:"name_similarity"`
	ScoreMargin         float64                     `yaml:"score_margin"`
	ScoreAmbiguity      float64                     `yaml:"score_ambiguity"`
	DensityThresholds   map[string]DensityThreshold `yaml:"density_thresholds"`
}

type DensityThreshold struct {
	DistanceM           *int     `yaml:"distance_m"`
	StrongDistanceM     *int     `yaml:"strong_distance_m"`
	LevThreshold        *float64 `yaml:"lev_threshold"`
	ConfidenceThreshold *float64 `yaml:"confidence_threshold"`
}

type Log struct {
	Level     string            `yaml:"level"`
	Format    string            `yaml:"format"`
	AddSource bool              `yaml:"add_source"`
	Levels    map[string]string `yaml:"levels"`
	Loki      LokiLog           `yaml:"loki"`
}

type LokiLog struct {
	URL       string `yaml:"url"`
	Enabled   *bool  `yaml:"enabled"`
	BatchSize int    `yaml:"batch_size"`
	BatchWait string `yaml:"batch_wait"`
}

type Telemetry struct {
	PrometheusAddr string `yaml:"prometheus_addr"`
	OTLPMetricsURL string `yaml:"otlp_metrics_url"`
}

type Pricing struct {
	DefaultCurrency string `yaml:"default_currency"`
}

type Sync struct {
	LogDir              string            `yaml:"log_dir"`
	CoverageGate        float64           `yaml:"coverage_gate"`
	AttachWait          string            `yaml:"attach_wait"`
	SkeletonChunkSize   int               `yaml:"skeleton_chunk_size"`
	OsmPath             string            `yaml:"osm_path"`
	YandexDumpPath      string            `yaml:"yandex_dump_path"`
	SkeletonRegion      string            `yaml:"skeleton_region"`
	Bbox                string            `yaml:"bbox"`
	RegionBBoxes        map[string]string `yaml:"region_bboxes"`
	FlatTripsPath       string            `yaml:"flat_trips_path"`
	LegacyThreshold     float64           `yaml:"legacy_threshold"`
	CoverageSoftScore   float64           `yaml:"coverage_soft_score"`
	TripsChurnThreshold float64           `yaml:"trips_churn_threshold"`
	TripsMaxSpeedKmh    float64           `yaml:"trips_max_speed_kmh"`
	OverpassMax         int               `yaml:"overpass_max"`
	StagingExpiryDays   int               `yaml:"staging_expiry_days"`
}

type Config struct {
	HTTP          HTTP          `yaml:"http"`
	Store         Store         `yaml:"store"`
	Cache         Cache         `yaml:"cache"`
	Queue         Queue         `yaml:"queue"`
	Providers     Providers     `yaml:"providers"`
	Auth          Auth          `yaml:"auth"`
	Geocoder      Geocoder      `yaml:"geocoder"`
	Yandex        Yandex        `yaml:"yandex"`
	Nominatim     Nominatim     `yaml:"nominatim"`
	Overpass      Overpass      `yaml:"overpass"`
	Motis         Motis         `yaml:"motis"`
	Planner       Planner       `yaml:"planner"`
	Log           Log           `yaml:"log"`
	Telemetry     Telemetry     `yaml:"telemetry"`
	Verification  Verification  `yaml:"verification"`
	Deduplication Deduplication `yaml:"deduplication"`
	Pricing       Pricing       `yaml:"pricing"`
	GTFS          GTFS          `yaml:"gtfs"`
	Sync          Sync          `yaml:"sync"`
}

func Defaults() *Config {
	return &Config{
		HTTP: HTTP{
			Addr:              ":8080",
			ReadHeaderTimeout: "10s",
			ShutdownTimeout:   "10s",
			RateLimit:         RateLimit{RPS: 100, Burst: 200},
		},
		Store: Store{Kind: "memory"},
		Cache: Cache{Kind: "memory", TTL: "5m"},
		Queue: Queue{Kind: "memory"},
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
		Planner: Planner{Engine: "csa", SemaphoreSize: runtime.NumCPU() * 2, SemaphoreEnable: true, MaxWalkMinutes: 30},
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
		Sync:          Sync{LogDir: "data/logs", CoverageGate: 0, SkeletonChunkSize: 100, OsmPath: "data/osm/stations.json", YandexDumpPath: "data/yandex/cache/global_stations_list.json", SkeletonRegion: "Кемеровская область - Кузбасс", Bbox: "53.5,84.0,57.0,88.5", RegionBBoxes: defaultRegionBBoxes(), LegacyThreshold: 0.6, CoverageSoftScore: 0.4, TripsChurnThreshold: 0.2, TripsMaxSpeedKmh: 200, OverpassMax: 200, StagingExpiryDays: 14},
	}
}

// defaultRegionBBoxes — bbox регионов пилота (СФО): overpass-сбор обязан
// получать станции выбранного региона, а не молчаливый глобальный bbox
// (issue #12: запуск «Республика Алтай» со статическим bbox Кузбасса).
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

func Load(path string) (*Config, error) {
	cfg := Defaults()

	if err := LoadDotenv(); err != nil {
		return nil, fmt.Errorf("config: dotenv: %w", err)
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
		if err == nil {
			expanded := os.ExpandEnv(expandEnvDefaults(string(data)))
			if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
				return nil, fmt.Errorf("config: parse %s: %w", path, err)
			}
		}
	}

	applyEnv(cfg)
	applyPrefixedEnv(cfg)
	applyDefaults(cfg)
	return cfg, nil
}

// expandEnvDefaults раскрывает bash-подобные дефолты ${VAR:-default} и
// ${VAR-default}, которые os.ExpandEnv не понимает (молча даёт "").
// Вызывать ДО os.ExpandEnv: существующее значение переменной имеет
// приоритет над YAML-дефолтом.
func expandEnvDefaults(s string) string {
	for {
		open := strings.LastIndex(s, "${")
		if open < 0 {
			return s
		}
		closeIdx := strings.IndexByte(s[open:], '}')
		if closeIdx < 0 {
			return s
		}
		closeIdx += open
		name := s[open+2 : closeIdx]
		replacement := ""
		if sep := strings.Index(name, ":-"); sep >= 0 {
			replacement = name[sep+2:]
			name = name[:sep]
		} else if sep := strings.IndexByte(name, '-'); sep >= 0 {
			replacement = name[sep+1:]
			name = name[:sep]
		}
		if v, ok := os.LookupEnv(name); ok && v != "" {
			replacement = v
		}
		s = s[:open] + replacement + s[closeIdx+1:]
	}
}

// applyDefaults подставляет дефолты для пустых значений после YAML и env.
// Пустые строки/нули означают «не задано»; явный 0 там, где он осмыслен,
// через YAML задать нельзя — для таких knobs используйте env.
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
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("HTTP_ADDR"); v != "" {
		cfg.HTTP.Addr = v
	}
	if v := os.Getenv("DATABASE_DSN"); v != "" {
		cfg.Store.DSN = v
	}
	if v := os.Getenv("ADMIN_TOKEN"); v != "" {
		cfg.Auth.AdminToken = v
	}
	if v := os.Getenv("PROVIDERS_ENABLED"); v != "" {
		cfg.Providers.Enabled = splitCsv(v)
	}
	if v := os.Getenv("YANDEX_RASP_KEY"); v != "" {
		cfg.Yandex.RaspKey = v
	}
	if v := os.Getenv("YANDEX_RASP_QUOTA_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Yandex.RaspQuotaLimit = n
		}
	}
	if v := os.Getenv("YANDEX_RASP_URL"); v != "" {
		cfg.Yandex.RaspURL = v
	}
	if v := os.Getenv("YANDEX_RASP_CACHE_DIR"); v != "" {
		cfg.Yandex.RaspCacheDir = v
	}
	if v := os.Getenv("YANDEX_GEOCODE_KEY"); v != "" {
		cfg.Yandex.GeocodeKey = v
	}
	if v := os.Getenv("YANDEX_GEOCODE_URL"); v != "" {
		cfg.Yandex.GeocodeURL = v
	}
	if v := os.Getenv("GEOCODER_KIND"); v != "" {
		cfg.Geocoder.Kind = v
	}
	if v := os.Getenv("NOMINATIM_URL"); v != "" {
		cfg.Nominatim.URL = v
	}
	if v := os.Getenv("OVERPASS_URL"); v != "" {
		cfg.Overpass.URL = v
	}
	if v := os.Getenv("OVERPASS_MIRROR_URL"); v != "" {
		cfg.Overpass.MirrorURL = v
	}
	if v := os.Getenv("GEOCODER_ATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Geocoder.Attempts = n
		}
	}
	if v := os.Getenv("PLANNER_ENGINE"); v != "" {
		cfg.Planner.Engine = v
	}
	if v := os.Getenv("PLANNER_SEMAPHORE_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Planner.SemaphoreSize = n
		}
	}
	if v := os.Getenv("PLANNER_SEMAPHORE_ENABLE"); v != "" {
		cfg.Planner.SemaphoreEnable = v == "1" || v == "true"
	}
	if v := os.Getenv("PLANNER_MAX_WALK_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 180 {
			cfg.Planner.MaxWalkMinutes = n
		}
	}
	if v := os.Getenv("HTTP_RATE_LIMIT_RPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.HTTP.RateLimit.RPS = n
		}
	}
	if v := os.Getenv("HTTP_RATE_LIMIT_BURST"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.HTTP.RateLimit.Burst = n
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}
	if v := os.Getenv("LOG_ADD_SOURCE"); v != "" {
		cfg.Log.AddSource = v == "1" || v == "true"
	}
	if v := os.Getenv("LOG_LOKI_URL"); v != "" {
		cfg.Log.Loki.URL = v
	}
	if v := os.Getenv("LOG_LOKI_ENABLED"); v != "" {
		cfg.Log.Loki.Enabled = boolPtr(v == "1" || v == "true")
	}
	if v := os.Getenv("LOG_LOKI_BATCH_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Log.Loki.BatchSize = n
		}
	}
	if v := os.Getenv("LOG_LOKI_BATCH_WAIT"); v != "" {
		cfg.Log.Loki.BatchWait = v
	}
	if v := os.Getenv("MOTIS_URL"); v != "" {
		cfg.Motis.URL = v
	}
	if v := os.Getenv("MOTIS_BASE"); v != "" {
		cfg.Motis.URL = v
	}
	if v := os.Getenv("VERIFICATION_CONFIDENCE_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Verification.ConfidenceThreshold = f
		}
	}
	if v := os.Getenv("VERIFICATION_DISTANCE_M"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Verification.DistanceM = n
		}
	}
	if v := os.Getenv("VERIFICATION_STRONG_DISTANCE_M"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Verification.StrongDistanceM = n
		}
	}
	if v := os.Getenv("VERIFICATION_LEV_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Verification.LevThreshold = f
		}
	}
	if v := os.Getenv("VERIFICATION_NAME_SIMILARITY"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Verification.NameSimilarity = f
		}
	}
	if v := os.Getenv("VERIFICATION_SCORE_MARGIN"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Verification.ScoreMargin = f
		}
	}
	if v := os.Getenv("VERIFICATION_SCORE_AMBIGUITY"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Verification.ScoreAmbiguity = f
		}
	}
	if v := os.Getenv("GEOCODER_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Geocoder.Limit = n
		}
	}
	if v := os.Getenv("GEOCODER_MAX_CALLS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.Geocoder.MaxCalls = n
		}
	}
	if v := os.Getenv("GEOCODER_TTL_VERIFIED"); v != "" {
		cfg.Geocoder.TTLVerified = v
	}
	if v := os.Getenv("GEOCODER_TTL_DISPUTED"); v != "" {
		cfg.Geocoder.TTLDisputed = v
	}
	if v := os.Getenv("GTFS_TMP_DIR"); v != "" {
		cfg.GTFS.TmpDir = v
	}
	if v := os.Getenv("SYNC_LOG_DIR"); v != "" {
		cfg.Sync.LogDir = v
	}
	if v := os.Getenv("SYNC_COVERAGE_GATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Sync.CoverageGate = f
		}
	}
	if v := os.Getenv("SYNC_ATTACH_WAIT"); v != "" {
		cfg.Sync.AttachWait = v
	}
	if v := os.Getenv("SYNC_OSM_PATH"); v != "" {
		cfg.Sync.OsmPath = v
	}
	if v := os.Getenv("SYNC_YANDEX_DUMP_PATH"); v != "" {
		cfg.Sync.YandexDumpPath = v
	}
	if v := os.Getenv("SYNC_SKELETON_REGION"); v != "" {
		cfg.Sync.SkeletonRegion = v
	}
	if v := os.Getenv("SYNC_BBOX"); v != "" {
		cfg.Sync.Bbox = v
	}
	if v := os.Getenv("SYNC_REGION_BBOX"); v != "" {
		// SYNC_REGION_BBOX="Регион=bbox;Регион2=bbox2" — точечное переопределение
		// карт региональных bbox (приоритетнее YAML).
		if cfg.Sync.RegionBBoxes == nil {
			cfg.Sync.RegionBBoxes = map[string]string{}
		}
		for _, kv := range strings.Split(v, ";") {
			if eq := strings.Index(kv, "="); eq > 0 {
				cfg.Sync.RegionBBoxes[strings.TrimSpace(kv[:eq])] = strings.TrimSpace(kv[eq+1:])
			}
		}
	}
	if v := os.Getenv("SYNC_FLAT_TRIPS_PATH"); v != "" {
		cfg.Sync.FlatTripsPath = v
	}
	if v := os.Getenv("SYNC_LEGACY_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Sync.LegacyThreshold = f
		}
	}
	if v := os.Getenv("SYNC_COVERAGE_SOFT_SCORE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Sync.CoverageSoftScore = f
		}
	}
	if v := os.Getenv("SYNC_TRIPS_CHURN_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Sync.TripsChurnThreshold = f
		}
	}
	if v := os.Getenv("SYNC_TRIPS_MAX_SPEED_KMH"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Sync.TripsMaxSpeedKmh = f
		}
	}
	if v := os.Getenv("SYNC_STAGING_EXPIRY_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Sync.StagingExpiryDays = n
		}
	}
	if v := os.Getenv("SYNC_SKELETON_CHUNK_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Sync.SkeletonChunkSize = n
			if cfg.Sync.SkeletonChunkSize > 100 {
				cfg.Sync.SkeletonChunkSize = 100
			}
		}
	}
	if v := os.Getenv("PRICING_DEFAULT_CURRENCY"); v != "" {
		cfg.Pricing.DefaultCurrency = v
	}
	if v := os.Getenv("DEFAULT_CURRENCY"); v != "" {
		cfg.Pricing.DefaultCurrency = v
	}
}

func applyPrefixedEnv(cfg *Config) {
	for _, e := range os.Environ() {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k, v := kv[0], kv[1]
		if !strings.HasPrefix(k, "TRAVELMCP__") {
			continue
		}
		path := strings.ToLower(strings.TrimPrefix(k, "TRAVELMCP__"))
		parts := strings.Split(path, "__")
		setByPath(cfg, parts, v)
	}
}

func setByPath(cfg *Config, parts []string, v string) {
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "http":
		if len(parts) == 2 && parts[1] == "addr" {
			cfg.HTTP.Addr = v
		}
		if len(parts) == 3 && parts[1] == "rate_limit" && parts[2] == "rps" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.HTTP.RateLimit.RPS = n
			}
		}
		if len(parts) == 3 && parts[1] == "rate_limit" && parts[2] == "burst" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.HTTP.RateLimit.Burst = n
			}
		}
	case "store":
		if len(parts) == 2 && parts[1] == "dsn" {
			cfg.Store.DSN = v
		}
		if len(parts) == 2 && parts[1] == "kind" {
			cfg.Store.Kind = v
		}
	case "cache":
		if len(parts) == 2 && parts[1] == "kind" {
			cfg.Cache.Kind = v
		}
		if len(parts) == 2 && parts[1] == "addr" {
			cfg.Cache.Addr = v
		}
	case "log":
		if len(parts) == 2 && parts[1] == "level" {
			cfg.Log.Level = v
		}
		if len(parts) == 2 && parts[1] == "format" {
			cfg.Log.Format = v
		}
		if len(parts) == 3 && parts[1] == "levels" {
			if cfg.Log.Levels == nil {
				cfg.Log.Levels = map[string]string{}
			}
			cfg.Log.Levels[parts[2]] = v
		}
		if len(parts) == 3 && parts[1] == "loki" && parts[2] == "url" {
			cfg.Log.Loki.URL = v
		}
		if len(parts) == 3 && parts[1] == "loki" && parts[2] == "enabled" {
			b := v == "1" || v == "true"
			cfg.Log.Loki.Enabled = &b
		}
	case "providers":
		if len(parts) == 2 && parts[1] == "enabled" {
			cfg.Providers.Enabled = splitCsv(v)
		}
	case "planner":
		if len(parts) == 2 && parts[1] == "engine" {
			cfg.Planner.Engine = v
		}
		if len(parts) == 2 && parts[1] == "semaphore_size" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.Planner.SemaphoreSize = n
			}
		}
		if len(parts) == 2 && parts[1] == "semaphore_enable" {
			cfg.Planner.SemaphoreEnable = v == "1" || v == "true"
		}
		if len(parts) == 2 && parts[1] == "max_walk_minutes" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 180 {
				cfg.Planner.MaxWalkMinutes = n
			}
		}
	case "auth":
		if len(parts) == 2 && parts[1] == "admin_token" {
			cfg.Auth.AdminToken = v
		}
	case "geocoder":
		if len(parts) == 2 && parts[1] == "kind" {
			cfg.Geocoder.Kind = v
		}
		if len(parts) == 2 && parts[1] == "attempts" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.Geocoder.Attempts = n
			}
		}
		if len(parts) == 2 && parts[1] == "limit" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.Geocoder.Limit = n
			}
		}
		if len(parts) == 2 && parts[1] == "ttl_verified" {
			cfg.Geocoder.TTLVerified = v
		}
		if len(parts) == 2 && parts[1] == "ttl_disputed" {
			cfg.Geocoder.TTLDisputed = v
		}
		if len(parts) == 2 && parts[1] == "max_calls" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				cfg.Geocoder.MaxCalls = n
			}
		}
	case "gtfs":
		if len(parts) == 2 && parts[1] == "tmp_dir" {
			cfg.GTFS.TmpDir = v
		}
	case "sync":
		if len(parts) == 2 && parts[1] == "log_dir" {
			cfg.Sync.LogDir = v
		}
		if len(parts) == 2 && parts[1] == "coverage_gate" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Sync.CoverageGate = f
			}
		}
		if len(parts) == 2 && parts[1] == "attach_wait" {
			cfg.Sync.AttachWait = v
		}
		if len(parts) == 2 && parts[1] == "osm_path" {
			cfg.Sync.OsmPath = v
		}
		if len(parts) == 2 && parts[1] == "yandex_dump_path" {
			cfg.Sync.YandexDumpPath = v
		}
		if len(parts) == 2 && parts[1] == "skeleton_region" {
			cfg.Sync.SkeletonRegion = v
		}
		if len(parts) == 2 && parts[1] == "bbox" {
			cfg.Sync.Bbox = v
		}
		if len(parts) == 2 && parts[1] == "flat_trips_path" {
			cfg.Sync.FlatTripsPath = v
		}
		if len(parts) == 2 && parts[1] == "legacy_threshold" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Sync.LegacyThreshold = f
			}
		}
		if len(parts) == 2 && parts[1] == "coverage_soft_score" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Sync.CoverageSoftScore = f
			}
		}
		if len(parts) == 2 && parts[1] == "trips_churn_threshold" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Sync.TripsChurnThreshold = f
			}
		}
		if len(parts) == 2 && parts[1] == "trips_max_speed_kmh" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Sync.TripsMaxSpeedKmh = f
			}
		}
		if len(parts) == 2 && parts[1] == "skeleton_chunk_size" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.Sync.SkeletonChunkSize = n
				if cfg.Sync.SkeletonChunkSize > 100 {
					cfg.Sync.SkeletonChunkSize = 100
				}
			}
		}
		if len(parts) == 2 && parts[1] == "staging_expiry_days" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.Sync.StagingExpiryDays = n
			}
		}
	case "yandex":
		if len(parts) == 2 && parts[1] == "rasp_key" {
			cfg.Yandex.RaspKey = v
		}
		if len(parts) == 2 && parts[1] == "rasp_url" {
			cfg.Yandex.RaspURL = v
		}
		if len(parts) == 2 && parts[1] == "rasp_cache_dir" {
			cfg.Yandex.RaspCacheDir = v
		}
		if len(parts) == 2 && parts[1] == "geocode_key" {
			cfg.Yandex.GeocodeKey = v
		}
		if len(parts) == 2 && parts[1] == "geocode_url" {
			cfg.Yandex.GeocodeURL = v
		}
		if len(parts) == 2 && parts[1] == "rasp_quota_limit" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.Yandex.RaspQuotaLimit = n
			}
		}
	case "nominatim":
		if len(parts) == 2 && parts[1] == "url" {
			cfg.Nominatim.URL = v
		}
	case "overpass":
		if len(parts) == 2 && parts[1] == "url" {
			cfg.Overpass.URL = v
		}
		if len(parts) == 2 && parts[1] == "mirror_url" {
			cfg.Overpass.MirrorURL = v
		}
	case "motis":
		if len(parts) == 2 && parts[1] == "url" {
			cfg.Motis.URL = v
		}
	case "verification":
		if len(parts) == 2 && parts[1] == "confidence_threshold" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Verification.ConfidenceThreshold = f
			}
		}
		if len(parts) == 2 && parts[1] == "distance_m" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.Verification.DistanceM = n
			}
		}
		if len(parts) == 2 && parts[1] == "strong_distance_m" {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.Verification.StrongDistanceM = n
			}
		}
		if len(parts) == 2 && parts[1] == "lev_threshold" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Verification.LevThreshold = f
			}
		}
		if len(parts) == 2 && parts[1] == "name_similarity" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Verification.NameSimilarity = f
			}
		}
		if len(parts) == 2 && parts[1] == "score_margin" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Verification.ScoreMargin = f
			}
		}
		if len(parts) == 2 && parts[1] == "score_ambiguity" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.Verification.ScoreAmbiguity = f
			}
		}
	case "pricing":
		if len(parts) == 2 && parts[1] == "default_currency" {
			cfg.Pricing.DefaultCurrency = v
		}
	}
}

func boolPtr(b bool) *bool { return &b }

func (g Geocoder) TTL() (verified, disputed time.Duration, err error) {
	verified, err = time.ParseDuration(g.TTLVerified)
	if err != nil {
		return 0, 0, fmt.Errorf("config: geocode.ttl_verified: %w", err)
	}
	disputed, err = time.ParseDuration(g.TTLDisputed)
	if err != nil {
		return 0, 0, fmt.Errorf("config: geocode.ttl_disputed: %w", err)
	}
	return verified, disputed, nil
}

func splitCsv(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// RegionBBox — bbox региона (minLat,minLon,maxLat,maxLon). Приоритет:
// карта region_bboxes → глобальный bbox пилота (fallback). ok=false —
// региона нет в карте: вызывающий решает, молчаливый ли это fallback.
func (s Sync) RegionBBox(region string) (string, bool) {
	if s.RegionBBoxes != nil {
		if v, ok := s.RegionBBoxes[region]; ok && v != "" {
			return v, true
		}
	}
	if s.Bbox != "" {
		return s.Bbox, false
	}
	return "", false
}
