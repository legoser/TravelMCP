package model

import "testing"

func TestLevelMapping(t *testing.T) {
	cases := map[int]Level{
		2:  LevelCountry,
		3:  LevelFederalDistrict,
		4:  LevelRegion,
		6:  LevelDistrict,
		8:  LevelCity,
		9:  LevelCityDistrict,
		10: LevelCityDistrict,
	}
	for admin, lvl := range cases {
		got, ok := LevelForAdminLevel(admin)
		if !ok || got != lvl {
			t.Fatalf("admin %d expected level %d got %d ok=%v", admin, lvl, got, ok)
		}
		if got.AdminLevel() == 0 {
			t.Fatalf("level %d has no admin_level", got)
		}
	}
	if LevelCity.String() != "city" {
		t.Fatalf("unexpected string %s", LevelCity.String())
	}
}
