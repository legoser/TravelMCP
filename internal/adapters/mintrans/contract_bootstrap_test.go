package mintrans

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "test.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

func mutateRaw(t *testing.T, raw []byte, fn func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	fn(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func TestValidateContractFixture(t *testing.T) {
	if err := ValidateDatasetContract(loadFixture(t)); err != nil {
		t.Fatalf("fixture must pass contract: %v", err)
	}
}

func TestValidateContractDrift(t *testing.T) {
	raw := loadFixture(t)
	cases := []struct {
		name string
		fn   func(m map[string]any)
		want string
	}{
		{"no_routes", func(m map[string]any) { delete(m, "routes") }, "routes"},
		{"bad_source", func(m map[string]any) { m["source"] = "other" }, "source"},
		{"bad_snapshot", func(m map[string]any) { m["snapshot"] = "10.05.2026" }, "snapshot"},
		{"bad_reg", func(m map[string]any) {
			m["routes"].([]any)[0].(map[string]any)["reg"] = "BAD"
		}, "reg"},
		{"dup_reg", func(m map[string]any) {
			rs := m["routes"].([]any)
			rs[0].(map[string]any)["reg"] = rs[1].(map[string]any)["reg"]
		}, "дубль"},
		{"bad_stop_region", func(m map[string]any) {
			m["stops"].([]any)[0].(map[string]any)["region"] = "XXX"
		}, "region"},
		{"bad_lat", func(m map[string]any) {
			m["stops"].([]any)[0].(map[string]any)["lat"] = 200.0
		}, "lat"},
		{"bad_direction", func(m map[string]any) {
			m["schedules"].([]any)[0].(map[string]any)["direction"] = "sideways"
		}, "direction"},
		{"dangling_route", func(m map[string]any) {
			m["schedules"].([]any)[0].(map[string]any)["route"] = "99.99.999"
		}, "route"},
		{"dangling_stop", func(m map[string]any) {
			m["schedules"].([]any)[0].(map[string]any)["stops"].([]any)[0].(map[string]any)["stop"] = "op:00:0"
		}, "stop"},
		{"dangling_service", func(m map[string]any) {
			m["schedules"].([]any)[0].(map[string]any)["service_id"] = float64(9999)
		}, "service_id"},
		{"bad_time", func(m map[string]any) {
			stops := m["schedules"].([]any)[0].(map[string]any)["stops"].([]any)
			stops[0].(map[string]any)["winter"].(map[string]any)["dep"] = []any{"25-00"}
		}, "dep"},
		{"bad_weekday", func(m map[string]any) {
			m["service_days"].([]any)[0].(map[string]any)["weekday"] = float64(9)
		}, "weekday"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := mutateRaw(t, raw, tc.fn)
			err := ValidateDatasetContract(bad)
			if err == nil {
				t.Fatalf("want contract error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q must contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateContractRealWorldFormats(t *testing.T) {
	raw := loadFixture(t)
	ok := mutateRaw(t, raw, func(m map[string]any) {
		rs := m["routes"].([]any)
		old := rs[0].(map[string]any)["reg"]
		rs[0].(map[string]any)["reg"] = "42.22.002/2"
		for _, sc := range m["schedules"].([]any) {
			if sc.(map[string]any)["route"] == old {
				sc.(map[string]any)["route"] = "42.22.002/2"
			}
		}
		svcs := m["services"].([]any)
		svcs[0].(map[string]any)["start_date"] = "2026-09-01"
		svcs[0].(map[string]any)["end_date"] = "2026-05-31"
		stops := m["schedules"].([]any)[0].(map[string]any)["stops"].([]any)
		stops[0].(map[string]any)["winter"].(map[string]any)["dep"] = []any{"13:35 (пт,вс)"}
	})
	if err := ValidateDatasetContract(ok); err != nil {
		t.Fatalf("реальные форматы обязаны проходить контракт: %v", err)
	}
	badWrap := mutateRaw(t, raw, func(m map[string]any) {
		svcs := m["services"].([]any)
		svcs[0].(map[string]any)["start_date"] = "2026-03-01"
		svcs[0].(map[string]any)["end_date"] = "2026-01-01"
	})
	if err := ValidateDatasetContract(badWrap); err == nil {
		t.Fatal("несезонный wrap обязан фейлить контракт")
	}
	badDays := mutateRaw(t, raw, func(m map[string]any) {
		stops := m["schedules"].([]any)[0].(map[string]any)["stops"].([]any)
		stops[0].(map[string]any)["winter"].(map[string]any)["dep"] = []any{"13:35 (xx)"}
	})
	if err := ValidateDatasetContract(badDays); err == nil {
		t.Fatal("неизвестные дни обязаны фейлить контракт")
	}
}

func TestBootstrapStable(t *testing.T) {
	raw := loadFixture(t)
	rep, err := CompareDatasets(raw, raw, 0.2)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if rep.Churn != 0 || !rep.TrustNK || rep.Alert {
		t.Fatalf("stable: %+v", rep)
	}
	if rep.OldSHA == "" || rep.OldSHA != rep.NewSHA {
		t.Fatalf("sha: %+v", rep)
	}
}

func TestBootstrapRemovedRoute(t *testing.T) {
	raw := loadFixture(t)
	bad := mutateRaw(t, raw, func(m map[string]any) {
		rs := m["routes"].([]any)
		drop := rs[0].(map[string]any)["reg"]
		m["routes"] = rs[1:]
		kept := []any{}
		for _, s := range m["schedules"].([]any) {
			if s.(map[string]any)["route"] != drop {
				kept = append(kept, s)
			}
		}
		m["schedules"] = kept
	})
	rep, err := CompareDatasets(raw, bad, 0.2)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if len(rep.Removed) != 1 || rep.TrustNK || !rep.Alert {
		t.Fatalf("removed: %+v", rep)
	}
	if rep.Churn < 0.2 {
		t.Fatalf("churn = %v, want >= 0.2", rep.Churn)
	}
}

func TestBootstrapCarrierChange(t *testing.T) {
	raw := loadFixture(t)
	bad := mutateRaw(t, raw, func(m map[string]any) {
		m["routes"].([]any)[0].(map[string]any)["carrier_inn"] = "0000000000"
	})
	rep, err := CompareDatasets(raw, bad, 0.2)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if len(rep.Changed) != 1 {
		t.Fatalf("changed: %+v", rep)
	}
}

func TestSyntheticKey(t *testing.T) {
	a := SyntheticKeyForRoute("54.22.078", "op:54:54099", "op:22:22007", "5410134455")
	b := SyntheticKeyForRoute("54.22.078", "op:54:54099", "op:22:22007", "5410134455")
	if a != b || !strings.HasPrefix(a, "synthetic:54:22-078:") {
		t.Fatalf("key = %q", a)
	}
	c := SyntheticKeyForRoute("54.22.078", "op:54:54099", "op:22:22007", "other")
	if a == c {
		t.Fatalf("carrier must change key: %q", a)
	}
}
