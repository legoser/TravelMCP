package httpx

import (
	"strings"
	"testing"
)

func TestRedactURLEdge(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		keep     []string
		drop     []string
		wantFull string
	}{
		{"no query untouched", "https://x.example/search", nil, nil, "https://x.example/search"},
		{"safe kept", "https://x.example/search?q=Кемерово&limit=5", []string{"q=", "limit=5"}, nil, ""},
		{"key redacted", "https://x.example/search?q=a&key=secret", []string{"q=a"}, []string{"secret"}, ""},
		{"case-insensitive key", "https://x.example/search?KEY=secret&q=a", []string{"q=a"}, []string{"secret"}, ""},
		{"apikey redacted", "https://x.example/search?apikey=s&lat=55.3&lon=86.0", []string{"lat=55.3"}, []string{"apikey=s"}, ""},
		{"bad url", "://bad", nil, nil, "***"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactURL(tc.in)
			if tc.wantFull != "" && got != tc.wantFull {
				t.Fatalf("got %q want %q", got, tc.wantFull)
			}
			for _, k := range tc.keep {
				if !strings.Contains(got, k) {
					t.Fatalf("%q must survive in %q", k, got)
				}
			}
			for _, d := range tc.drop {
				if strings.Contains(got, d) {
					t.Fatalf("%q must be redacted in %q", d, got)
				}
			}
		})
	}
}
