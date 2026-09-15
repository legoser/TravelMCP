package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminLimitOffsetEdge(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		wantLimit  int
		wantOffset int
	}{
		{"defaults", "", 20, 0},
		{"normal", "limit=10&offset=30", 10, 30},
		{"zero limit ignored", "limit=0", 20, 0},
		{"negative limit ignored", "limit=-5", 20, 0},
		{"over cap ignored", "limit=101", 20, 0},
		{"cap boundary ok", "limit=100", 100, 0},
		{"negative offset ignored", "offset=-1", 20, 0},
		{"garbage ignored", "limit=abc&offset=1.5", 20, 0},
		{"injection ignored", "limit=10;DROP&offset=0", 20, 0},
		{"huge offset passes", "offset=999999999", 20, 999999999},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/routes?"+tc.query, nil)
			limit, offset := adminLimitOffset(r)
			if limit != tc.wantLimit || offset != tc.wantOffset {
				t.Fatalf("got (%d,%d), want (%d,%d)", limit, offset, tc.wantLimit, tc.wantOffset)
			}
		})
	}
}
