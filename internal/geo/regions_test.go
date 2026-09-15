package geo

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func TestRegionName(t *testing.T) {
	if got := RegionName("54"); got != "Новосибирская область" {
		t.Fatalf("RegionName(54) = %q", got)
	}
	if got := RegionName("25"); got != "Приморский край" {
		t.Fatalf("RegionName(25) = %q", got)
	}
	if got := RegionName("99"); got != "" {
		t.Fatalf("unknown code must be empty, got %q", got)
	}
}

func TestRegionSeedMatchesMigration(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", "001_initial.sql"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\('(\d+)','([^']+)', '(rural|suburban|urban|metro)'\)`)
	found := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		found[m[1]] = m[2]
	}
	if len(found) != len(RegionNames) {
		t.Fatalf("migration seed has %d regions, RegionNames has %d", len(found), len(RegionNames))
	}
	for code, name := range RegionNames {
		if got, ok := found[code]; !ok || got != name {
			t.Fatalf("code %s: migration=%q map=%q", code, got, name)
		}
	}
}
