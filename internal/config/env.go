package config

import (
	"os"
	"strconv"
	"strings"
)

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
	if v := os.Getenv("PLANNER_MIN_TRANSFER_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 240 {
			cfg.Planner.MinTransferMinutes = n
		}
	}
	if v := os.Getenv("PLANNER_FLIGHT_CHECK_IN_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 600 {
			cfg.Planner.FlightCheckInMinutes = n
		}
	}
	if v := os.Getenv("PLANNER_ALT_WINDOW_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1440 {
			cfg.Planner.AltWindowMinutes = n
		}
	}
	if v := os.Getenv("PLANNER_ALT_STEP_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 360 {
			cfg.Planner.AltStepMinutes = n
		}
	}
	if v := os.Getenv("PLANNER_ARRIVAL_WINDOW_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 168 {
			cfg.Planner.ArrivalWindowHours = n
		}
	}
	if v := os.Getenv("PLANNER_ARRIVAL_STEP_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 120 {
			cfg.Planner.ArrivalStepMinutes = n
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
		// SYNC_REGION_BBOX="Region=bbox;Region2=bbox2" — targeted override
		// of the regional bbox map (takes priority over YAML).
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
	if v := os.Getenv("SYNC_JOBS_POLL_INTERVAL"); v != "" {
		cfg.Sync.JobsPollInterval = v
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
		if len(parts) == 2 && parts[1] == "alt_window_minutes" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1440 {
				cfg.Planner.AltWindowMinutes = n
			}
		}
		if len(parts) == 2 && parts[1] == "alt_step_minutes" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 360 {
				cfg.Planner.AltStepMinutes = n
			}
		}
		if len(parts) == 2 && parts[1] == "arrival_window_hours" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 168 {
				cfg.Planner.ArrivalWindowHours = n
			}
		}
		if len(parts) == 2 && parts[1] == "arrival_step_minutes" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 120 {
				cfg.Planner.ArrivalStepMinutes = n
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
		if len(parts) == 2 && parts[1] == "jobs_poll_interval" {
			cfg.Sync.JobsPollInterval = v
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
