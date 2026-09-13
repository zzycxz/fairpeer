package driver

import "testing"

// TestPrefixMatches documents the boundary semantics the classifier tables
// depend on: a prefix matches when the remainder starts at a "space / dash"
// boundary. The dash-stem lesson (set- vs set-service) is what fixed the
// Windows write tier — keep these as the executable record.
func TestPrefixMatches(t *testing.T) {
	cases := []struct {
		cmd, prefix string
		want        bool
	}{
		{"reboot", "reboot", true},
		{"rebootx", "reboot", false},     // no boundary after prefix
		{"set-service a", "set", true},   // dash boundary (bare stem)
		{"set-service a", "set-", false}, // dash-stem entry: boundary would need "set--" — the dead-table bug
		{"remove-item -recurse x", "remove-item -recurse", true},
		{"remove-item -force -recurse x", "remove-item -recurse", false}, // flag order matters — dangerous tier must list variants
		{"cat /proc/net/dev", "cat /proc", true},
		{"", "get", false},
	}
	for _, c := range cases {
		if got := prefixMatches(c.cmd, c.prefix); got != c.want {
			t.Fatalf("prefixMatches(%q, %q) = %v, want %v", c.cmd, c.prefix, got, c.want)
		}
	}
}
