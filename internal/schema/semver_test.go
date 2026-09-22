package schema

import "testing"

func TestParseVersion(t *testing.T) {
	ok := []string{"0.0.0", "0.1.0", "1.2.3", "10.20.30"}
	for _, s := range ok {
		if _, err := parseVersion(s); err != nil {
			t.Errorf("parseVersion(%q): unexpected error %v", s, err)
		}
	}

	bad := []string{"", "1", "1.2", "v1.2.3", "1.2.3.4", "1.2.x", "latest", "1.-2.3", "01.2.3"}
	for _, s := range bad {
		if _, err := parseVersion(s); err == nil {
			t.Errorf("parseVersion(%q): expected an error, got none", s)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.1.1", -1},
		{"0.1.1", "0.1.0", 1},
		{"0.1.0", "0.2.0", -1},
		{"0.9.0", "1.0.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.10.0", "1.9.0", 1}, // numeric, not lexical
	}
	for _, tt := range tests {
		a, err := parseVersion(tt.a)
		if err != nil {
			t.Fatal(err)
		}
		b, err := parseVersion(tt.b)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.compare(b); got != tt.want {
			t.Errorf("compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
