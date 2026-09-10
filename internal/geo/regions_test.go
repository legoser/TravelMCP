package geo

import "testing"

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
