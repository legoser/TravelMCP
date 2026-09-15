package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

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

// expandEnvDefaults expands bash-like defaults ${VAR:-default} and
// ${VAR-default}, which os.ExpandEnv does not understand (silently yields "").
// Call BEFORE os.ExpandEnv: an existing variable value takes priority
// over the YAML default.
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

// RegionBBox — region bbox (minLat,minLon,maxLat,maxLon). Priority:
// region_bboxes map → pilot global bbox (fallback). ok=false —
// region not in map: the caller decides whether it is a silent fallback.
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
