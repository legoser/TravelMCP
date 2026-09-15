package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestRedactedHeadersHidesSecrets(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret")
	h.Set("X-API-Key", "tm_abc")
	h.Set("Content-Type", "application/json")
	h.Set("X-Request-ID", "r1")
	out := redactedHeaders(h)
	if _, ok := out["Authorization"]; ok {
		t.Fatal("Authorization must not appear in logs")
	}
	if _, ok := out["X-Api-Key"]; ok {
		t.Fatal("X-API-Key must not appear in logs")
	}
	if out["Content-Type"] != "application/json" || out["X-Request-ID"] != "r1" {
		t.Fatalf("safe headers must keep values: %+v", out)
	}
}

func TestRedactedQueryKeepsKeysOnly(t *testing.T) {
	got := redactedQuery("region=42&token=secret")
	if strings.Contains(got, "secret") || strings.Contains(got, "42") {
		t.Fatalf("query values must be redacted: %q", got)
	}
	if !strings.Contains(got, "region=***") || !strings.Contains(got, "token=***") {
		t.Fatalf("query keys must be preserved: %q", got)
	}
	if redactedQuery("") != "" || redactedQuery("%%%") != "***" {
		t.Fatal("empty/invalid query edge cases broken")
	}
}
