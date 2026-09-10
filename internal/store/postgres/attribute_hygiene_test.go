package postgres

import "testing"

func TestUnionTypes(t *testing.T) {
	cases := []struct{ stored, fact, want string }{
		{"", "bus", "bus"},
		{"bus", "bus", "bus"},
		{"bus+rail", "bus", "bus+rail"},
		{"flight", "bus", "bus+flight"},
		{"", "", ""},
		{"tram", "bus+tram", "bus+tram"},
		{"trolley", "bus", "bus+trolley"},
	}
	for _, c := range cases {
		if got := unionTypes(c.stored, c.fact); got != c.want {
			t.Errorf("unionTypes(%q, %q) = %q, want %q", c.stored, c.fact, got, c.want)
		}
	}
}
