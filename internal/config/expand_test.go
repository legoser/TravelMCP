package config

import (
	"os"
	"testing"
)

func TestExpandEnvDefaults(t *testing.T) {
	os.Unsetenv("TEST_ABSENT_VAR")
	os.Setenv("TEST_SET_VAR", "from-env")
	t.Setenv("TEST_SET_VAR", "from-env")

	cases := []struct {
		in   string
		want string
	}{
		{"plain", "plain"},
		{"${TEST_ABSENT_VAR:-http://default:3100}", "http://default:3100"},
		{"${TEST_SET_VAR:-http://default:3100}", "from-env"},
		{"pre ${TEST_ABSENT_VAR:-def} post", "pre def post"},
		{"${TEST_ABSENT_VAR-}", ""},
		{"${TEST_SET_VAR}", "from-env"},
		{"no-braces", "no-braces"},
		{"${UNCLOSED", "${UNCLOSED"},
		{"$SIMPLE", "$SIMPLE"},
	}
	for _, c := range cases {
		if got := expandEnvDefaults(c.in); got != c.want {
			t.Errorf("expandEnvDefaults(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExpandEnvDefaultsChain(t *testing.T) {
	os.Unsetenv("A1")
	t.Setenv("B1", "x")
	in := "url: ${A1:-${B1:-fallback}}"
	want := "url: x"
	if got := expandEnvDefaults(in); got != want {
		t.Errorf("nested defaults: got %q, want %q", got, want)
	}
}
