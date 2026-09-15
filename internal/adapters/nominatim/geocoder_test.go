package nominatim

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"travelmcp/internal/config"
	"travelmcp/internal/support/httpx"
)

func testGeocoder(t *testing.T, handler http.HandlerFunc) *Geocoder {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cfg := config.Defaults()
	cfg.Nominatim.URL = srv.URL
	return New(*cfg, httpx.New(nil, "test"))
}

func TestGeocodeCandidatesEdge(t *testing.T) {
	t.Run("limit clamp and trim", func(t *testing.T) {
		var gotLimit string
		g := testGeocoder(t, func(w http.ResponseWriter, r *http.Request) {
			gotLimit = r.URL.Query().Get("limit")
			w.Write([]byte(`[{"lat":"55.35","lon":"86.08","name":"Кемерово"},{"lat":"55.36","lon":"86.09","display_name":"Топки, display"}]`))
		})
		cands, err := g.GeocodeCandidates(context.Background(), "Кемерово", 99)
		if err != nil {
			t.Fatal(err)
		}
		if gotLimit != "10" {
			t.Fatalf("limit must clamp to 10, got %q", gotLimit)
		}
		if len(cands) != 2 || cands[0].Name != "Кемерово" || cands[1].Name != "Топки, display" {
			t.Fatalf("name fallback broken: %+v", cands)
		}
	})
	t.Run("zero limit default", func(t *testing.T) {
		var gotLimit string
		g := testGeocoder(t, func(w http.ResponseWriter, r *http.Request) {
			gotLimit = r.URL.Query().Get("limit")
			w.Write([]byte(`[{"lat":"55.35","lon":"86.08","name":"x"}]`))
		})
		if _, err := g.GeocodeCandidates(context.Background(), "x", 0); err != nil {
			t.Fatal(err)
		}
		if gotLimit != "5" {
			t.Fatalf("zero limit must fall back to 5, got %q", gotLimit)
		}
	})
	t.Run("empty result", func(t *testing.T) {
		g := testGeocoder(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`[]`))
		})
		if _, err := g.GeocodeCandidates(context.Background(), "nowhere", 1); err == nil {
			t.Fatal("empty result must error")
		}
	})
	t.Run("http error", func(t *testing.T) {
		g := testGeocoder(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		})
		if _, err := g.GeocodeCandidates(context.Background(), "x", 1); err == nil {
			t.Fatal("429 must error")
		}
	})
	t.Run("bad json", func(t *testing.T) {
		g := testGeocoder(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{broken`))
		})
		if _, err := g.GeocodeCandidates(context.Background(), "x", 1); err == nil {
			t.Fatal("bad json must error")
		}
	})
	t.Run("malformed coords skipped", func(t *testing.T) {
		g := testGeocoder(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`[{"lat":"oops","lon":"86.08","name":"phantom"},{"lat":"55.35","lon":"86.08","name":"real"}]`))
		})
		cands, err := g.GeocodeCandidates(context.Background(), "x", 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(cands) != 1 || cands[0].Name != "real" {
			t.Fatalf("malformed coords must be skipped, no (0,0): %+v", cands)
		}
	})
	t.Run("all malformed", func(t *testing.T) {
		g := testGeocoder(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`[{"lat":"oops","lon":"nope","name":"phantom"}]`))
		})
		if _, err := g.GeocodeCandidates(context.Background(), "x", 5); err == nil {
			t.Fatal("all-malformed must be not found")
		}
	})
}
