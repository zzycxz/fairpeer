package main

import "testing"

func TestParseGitNumstat(t *testing.T) {
	cases := []struct {
		name           string
		out            string
		added, removed int
	}{
		{"empty", "", 0, 0},
		{"single", "3\t1\ta.txt\n", 3, 1},
		{"multi", "10\t2\ta.txt\n0\t5\tb/c.go\n", 10, 7},
		// Binary files report "-" for both columns and must be skipped.
		{"binary", "-\t-\timg.png\n4\t0\tcode.go\n", 4, 0},
		{"binary-only", "-\t-\timg.png\n", 0, 0},
		{"renamed", "1\t1\tb.txt\t{a.txt => b.txt}\n", 1, 1},
		{"blank-lines", "1\t1\ta.txt\n\n2\t2\tb.txt\n", 3, 3},
		{"malformed", "garbage\n1\t1\ta.txt\n", 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			added, removed := parseGitNumstat(c.out)
			if added != c.added || removed != c.removed {
				t.Fatalf("parseGitNumstat(%q) = %d/%d, want %d/%d", c.out, added, removed, c.added, c.removed)
			}
		})
	}
}
