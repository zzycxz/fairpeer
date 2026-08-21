package main

import (
	"testing"
)

// Session-profile attribution drives ListSessionsForProfile: the directory
// partition wins, the .meta Profile stamp breaks ties for legacy unprofiled
// dirs, and everything else is dev. A workspace literally named "cowork" must
// NOT be misclassified (the classifier trusts exact partition dirs, not path
// patterns).
func TestClassifySessionProfile(t *testing.T) {
	named := map[string]string{
		`C:\u\sessions\cowork`: "cowork",
		`C:\u\sessions\netdev`: "netdev",
		`C:\c\projects\myproj\cowork\sessions`:  "cowork",
		`C:\c\projects\cowork\sessions`:         "", // unprofiled project dir — not a partition
	}

	cases := []struct {
		dir, meta, want string
	}{
		{`C:\u\sessions\cowork`, "", "cowork"},
		{`C:\c\projects\myproj\netdev\sessions`, "", "netdev"},
		// Directory wins over a stale meta stamp.
		{`C:\u\sessions\netdev`, "dev", "netdev"},
		// Legacy unprofiled dirs: meta stamp decides, else dev.
		{`C:\u\sessions`, "cowork", "cowork"},
		{`C:\u\sessions`, "", "dev"},
		{`C:\c\projects\cowork\sessions`, "", "dev"}, // workspace slug happens to be "cowork"
		{`C:\c\projects\cowork\sessions`, "netdev", "netdev"},
	}
	named[`C:\c\projects\myproj\netdev\sessions`] = "netdev"
	for _, c := range cases {
		if got := classifySessionProfile(c.dir, c.meta, named); got != c.want {
			t.Errorf("classifySessionProfile(%q, %q) = %q, want %q", c.dir, c.meta, got, c.want)
		}
	}
}
