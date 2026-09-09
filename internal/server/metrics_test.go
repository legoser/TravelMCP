package server

import "testing"

func TestNormalizeRoute(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"POST /mcp", "POST /mcp"},
		{"GET /healthz", "GET /healthz"},
		{"GET /metrics", "GET /metrics"},
		{"GET /api/v1/users/123/keys", "GET /api/v1/users/{id}/keys"},
		{"DELETE /api/v1/keys/456", "DELETE /api/v1/keys/{id}"},
		{"GET /api/v1/admin/terminals/789/schedule", "GET /api/v1/admin/terminals/{id}/schedule"},
		{"GET /api/v1/admin/terminals/{id}/schedule", "GET /api/v1/admin/terminals/{id}/schedule"},
		{"GET /readyz", "GET /readyz"},
	}
	for _, tc := range cases {
		got := normalizeRoute(tc.path)
		if got != tc.want {
			t.Errorf("normalizeRoute(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestNormalizeRouteNoCardinalityLeak(t *testing.T) {
	paths := []string{
		"/api/v1/users/1/keys",
		"/api/v1/users/2/keys",
		"/api/v1/users/3/keys",
		"/api/v1/users/99999/keys",
	}
	seen := map[string]bool{}
	for _, p := range paths {
		normalized := normalizeRoute(p)
		seen[normalized] = true
	}
	if len(seen) != 1 {
		t.Errorf("expected all paths to normalize to 1 pattern, got %d distinct: %v", len(seen), seen)
	}
}
