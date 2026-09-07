package postgres

import "testing"

func TestIdentifierSystemRank(t *testing.T) {
	order := []string{"mintrans", "osm", "yandex", "manual"}
	prev := -1
	for _, s := range order {
		r := identifierSystemRank(s)
		if r <= prev {
			t.Fatalf("rank(%q)=%d not above prev %d", s, r, prev)
		}
		prev = r
	}
	if got := identifierSystemRank("gtfs"); got != 0 {
		t.Fatalf("rank(gtfs)=%d want 0", got)
	}
	if got := identifierSystemRank(""); got != 0 {
		t.Fatalf("rank(empty)=%d want 0", got)
	}
}
