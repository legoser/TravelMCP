package config

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

type GTFS struct {
	Path   string `yaml:"path"`
	TmpDir string `yaml:"tmp_dir"`
}

// Geocoder — everything about geocoding in one section: provider choice
// (kind/attempts/limit), the api_quotas limit (max_calls) and the
// geocode_cache TTLs (ttl_verified/ttl_disputed).
type Geocoder struct {
	Kind     string `yaml:"kind"`
	Attempts int    `yaml:"attempts"`
	Limit    int    `yaml:"limit"`
	// MaxCalls — api_quotas limit for geocoders (nominatim reverse,
	// overpass enrich). 0 = caller's default.
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
	// RaspQuotaLimit — daily api_quotas('yandex_rasp') limit.
	// 0 = adapter default (DefaultRaspQuotaLimit, 500). Single ownership
	// point for the limit: the adapter substitutes its default on 0,
	// the config overrides it.
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
	// MinTransferMinutes — minimum trip-to-trip transfer buffer (issue #12):
	// platform/station change + ticket purchase.
	MinTransferMinutes int `yaml:"min_transfer_minutes"`
	// FlightCheckInMinutes — air-leg transfer buffer (issue #12):
	// check-in/boarding/airport travel.
	FlightCheckInMinutes int `yaml:"flight_check_in_minutes"`
	// AltWindowMinutes/AltStepMinutes — planner Pareto-heuristic
	// alternate-departure window and step (pre-McRAPTOR): 360 by 60
	// means up to 6 extra runs per request.
	AltWindowMinutes int `yaml:"alt_window_minutes"`
	AltStepMinutes   int `yaml:"alt_step_minutes"`
	// ArrivalWindowHours/ArrivalStepMinutes — arrival-search backward
	// window (hours) and fallback departure grid step (minutes) in
	// planArrival: how far back from the requested arrival to look for
	// connecting trips when no direct candidate exists.
	ArrivalWindowHours int `yaml:"arrival_window_hours"`
	ArrivalStepMinutes int `yaml:"arrival_step_minutes"`
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
	// JobsPollInterval — jobs-worker queue poll interval ("5s", "1m"...).
	// Empty = 5s fallback at the call site (same convention as AttachWait).
	JobsPollInterval string `yaml:"jobs_poll_interval"`
}

type Config struct {
	HTTP          HTTP          `yaml:"http"`
	Store         Store         `yaml:"store"`
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
