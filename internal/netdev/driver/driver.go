// Package driver holds the per-vendor network-device CLI knowledge: how to
// classify a command (the structural read-only seal), how to disable paging,
// what the prompt looks like, and what error output looks like. One driver
// covers one vendor OS family; in-family version differences live in the
// pattern tables, not in new drivers (NETDEV_SPEC §4).
package driver

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Class is a command's safety classification. The exec tool refuses anything
// that is not Read — Write and Dangerous are proposal material (P4), Unknown
// defaults to the conservative Write handling per the spec's Appendix B-1.
type Class int

const (
	Read Class = iota
	Write
	Dangerous
	Unknown
)

func (c Class) String() string {
	switch c {
	case Read:
		return "read"
	case Write:
		return "write"
	case Dangerous:
		return "dangerous"
	default:
		return "unknown"
	}
}

// Driver describes one vendor OS CLI.
type Driver interface {
	// Key identifies the driver, e.g. "huawei-vrp", "cisco-ios".
	Key() string
	// Classify categorizes one CLI command line (first line, no newline).
	Classify(cmd string) Class
	// PagingOff returns the command(s) to run once at session start so long
	// outputs are not interactively paged.
	PagingOff() []string
	// Prompt matches the device prompt as it appears at the end of output
	// (user view, config view, etc.). The session engine anchors it to the
	// final line.
	Prompt() *regexp.Regexp
	// Errors matches a single output line that indicates the command failed.
	Errors() []*regexp.Regexp
}

// For resolves vendor+os to a driver. OS is matched leniently: "vrp8" and
// "vrp5" both map to the huawei-vrp driver (quirk tables are internal).
func For(vendor, os string) (Driver, bool) {
	key := resolveKey(vendor, os)
	mu.Lock()
	defer mu.Unlock()
	d, ok := registry[key]
	return d, ok
}

// Keys lists registered driver keys (sorted; for error messages and UI).
func Keys() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var (
	mu       sync.Mutex
	registry = map[string]Driver{}
)

func register(d Driver) {
	mu.Lock()
	defer mu.Unlock()
	registry[d.Key()] = d
}

func resolveKey(vendor, os string) string {
	v := strings.ToLower(strings.TrimSpace(vendor))
	o := strings.ToLower(strings.TrimSpace(os))
	switch v {
	case "huawei":
		return "huawei-vrp" // vrp5/vrp8 share the driver; quirks ride in tables
	case "cisco":
		switch {
		case strings.HasPrefix(o, "ios-xe"), strings.HasPrefix(o, "iosxe"):
			return "cisco-ios" // IOS-XE keeps IOS CLI semantics for our surface
		default:
			return "cisco-ios"
		}
	case "zte":
		return "zte-zxr10"
	default:
		return v + "-" + o
	}
}

// firstWords normalizes a command line for prefix classification: collapses
// whitespace and lowercases. Context-sensitive multiword prefixes
// ("terminal length") are matched by the concrete drivers.
func firstWords(cmd string) string {
	return strings.Join(strings.Fields(strings.ToLower(cmd)), " ")
}

// classifyByTables is the shared classification core: dangerous patterns win,
// then write prefixes, then read prefixes; anything else is Unknown. Patterns
// match the normalized command as a word-prefix ("display clock" matches the
// prefix "display"; "displayclock" does not).
type classTables struct {
	dangerous []string // whole-command prefixes
	write     []string
	read      []string
}

func (t classTables) classify(cmd string) Class {
	normalized := firstWords(cmd)
	if normalized == "" {
		return Unknown
	}
	// Longest-prefix-first so "reset saved-configuration" beats "reset".
	for _, group := range []struct {
		prefixes []string
		class    Class
	}{
		{t.dangerous, Dangerous},
		{t.write, Write},
		{t.read, Read},
	} {
		for _, p := range sortedByLen(group.prefixes) {
			if normalized == p || strings.HasPrefix(normalized, p+" ") {
				return group.class
			}
		}
	}
	return Unknown
}

func sortedByLen(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && len(out[j]) > len(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
