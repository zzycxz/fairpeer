package netdev

// logsearch.go — cross-device fan-out log search (NETDEV_SPEC_V2 §3.3): the
// IOC sweep. One pattern, many hosts — "given this IP / hash / keyword, who
// mentioned it?" Every fetch is a normal sealed LogRead (guardrails →
// classifier → session → redact → audit all apply); the fan-out is pure
// orchestration with a hard pair cap and honest coverage reporting when the
// per-turn budget stops the sweep midway (§3.3: report how many devices were
// covered, never fail silently).
//
// v1 executes device fetches SEQUENTIALLY on purpose: sessions/VTY caps are
// per-device and the worker-pool version deserves its own soak testing; the
// pair cap bounds the wall clock until then.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// logSearchDefaultSources are the classic syslog/auth files probed per host;
// missing files are non-fatal (IsError → next candidate), so both Debian and
// RHEL-style layouts are covered by one sweep.
var logSearchDefaultSources = []string{
	"file:/var/log/syslog",
	"file:/var/log/messages",
	"file:/var/log/auth.log",
	"file:/var/log/secure",
}

const (
	logSearchMaxPairs = 24  // hard cap on (device, source) fetches per sweep
	logSearchTail     = 200 // lines fetched per source; matching is client-side
	logSearchMaxHits  = 60  // reported hits cap (note explains truncation)
	logSearchCtx      = 2   // context lines each side of a hit
)

// LogSearchHit is one matching line with its ±context.
type LogSearchHit struct {
	Device  string   `json:"device"`
	Source  string   `json:"source"`
	Line    string   `json:"line"`
	Context []string `json:"context,omitempty"`
}

// LogSearchResult is the sweep outcome. Covered/Total + Skipped make partial
// coverage explicit — a budget-stopped sweep reports exactly what it missed.
type LogSearchResult struct {
	Pattern    string         `json:"pattern"`
	Hits       []LogSearchHit `json:"hits"`
	Devices    []string       `json:"devices_searched"`
	Skipped    []string       `json:"skipped"` // device — reason strings
	Covered    int            `json:"covered_devices"`
	Total      int            `json:"total_devices"`
	HitDevice  int            `json:"devices_with_hits"`
	BudgetStop bool           `json:"budget_stopped"`
	Note       string         `json:"note,omitempty"`
}

// LogSearch fans pattern across devices (empty = every linux host in the
// inventory). Custom sources override the default candidates; they ride the
// same per-device whitelist as netdev_log_read.
func (m *Manager) LogSearch(ctx context.Context, pattern string, devices, sources []string, since string) LogSearchResult {
	res := LogSearchResult{Pattern: pattern}
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		res.Note = "pattern is required"
		return res
	}
	if len(sources) == 0 {
		sources = logSearchDefaultSources
	}

	// Device set: explicit names, else every linux host (the default file
	// sources are Linux paths; windows event-log search rides winevt: later).
	if len(devices) == 0 {
		for _, d := range m.cfg.NetDev.Devices {
			if d.Vendor == "linux" {
				devices = append(devices, d.Name)
			}
		}
	}
	res.Total = len(devices)
	if res.Total == 0 {
		res.Note = "no linux hosts in the inventory — register one in 运维设置 or pass explicit device names"
		return res
	}

	re, _ := regexp.Compile(pattern) // invalid regex → literal fallback (same as filterLogLines)
	pairs := 0
	for _, name := range devices {
		d, ok := m.cfg.NetDevDeviceByName(name)
		if !ok {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s — not in inventory", name))
			continue
		}
		if d.Vendor != "linux" && d.Vendor != "vmware" {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s — vendor %s has no file log sources (linux hosts only in v1)", name, d.Vendor))
			continue
		}
		deviceHits := 0
		sourceOK := false
		deviceNoted := false // a skip line already exists for this device
		for _, src := range sources {
			if pairs >= logSearchMaxPairs {
				res.BudgetStop = true
				break
			}
			pairs++
			r := m.LogRead(ctx, name, src, logSearchTail, since, "")
			if r.Refused {
				if strings.Contains(r.Refusal, "budget") {
					// Every later fetch would refuse identically — stop the
					// whole sweep and report what's left uncovered.
					res.BudgetStop = true
					break
				}
				res.Skipped = append(res.Skipped, fmt.Sprintf("%s — %s", name, r.Refusal))
				deviceNoted = true
				continue
			}
			if r.IsError {
				continue // missing file is the normal case for half the candidates
			}
			sourceOK = true
			lines := strings.Split(r.Output, "\n")
			for _, h := range matchLogLines(name, src, lines, pattern, re) {
				if len(res.Hits) >= logSearchMaxHits {
					break
				}
				res.Hits = append(res.Hits, h)
				deviceHits++
			}
			if len(res.Hits) >= logSearchMaxHits {
				break
			}
		}
		if res.BudgetStop {
			break
		}
		if sourceOK {
			res.Devices = append(res.Devices, name)
			res.Covered++
			if deviceHits > 0 {
				res.HitDevice++
			}
		} else if !deviceNoted {
			// No source executed and nothing recorded: the host has none of
			// the default files — one skip line keeps coverage honest.
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s — none of the default log sources were readable", name))
		}
	}
	if len(res.Hits) >= logSearchMaxHits {
		res.Note = fmt.Sprintf("hit cap %d reached — tighten the pattern or the since window", logSearchMaxHits)
	}
	if res.BudgetStop {
		res.Note += " sweep stopped early: pair cap or turn budget reached; uncovered devices are NOT reported as clean"
	}
	return res
}

// matchLogLines returns the hits (with ±logSearchCtx context) for one fetched
// source. re may be nil (literal substring then), mirroring filterLogLines.
func matchLogLines(device, source string, lines []string, pattern string, re *regexp.Regexp) []LogSearchHit {
	var hits []LogSearchHit
	for i, ln := range lines {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" {
			continue
		}
		match := re != nil && re.MatchString(ln)
		if !match && re == nil {
			match = strings.Contains(ln, pattern)
		}
		if !match {
			continue
		}
		h := LogSearchHit{Device: device, Source: source, Line: ln}
		lo := i - logSearchCtx
		if lo < 0 {
			lo = 0
		}
		hi := i + logSearchCtx + 1
		if hi > len(lines) {
			hi = len(lines)
		}
		for _, c := range lines[lo:hi] {
			if c = strings.TrimRight(c, "\r"); c != "" {
				h.Context = append(h.Context, c)
			}
		}
		hits = append(hits, h)
		if len(hits) >= logSearchMaxHits {
			break
		}
	}
	return hits
}

// ── Agent tool ───────────────────────────────────────────────────────────────

// logSearchTool — netdev_log_search: one pattern, every linux host. The IOC
// sweep's engine; sealed LogRead per (device, source) pair, budget-aware.
type logSearchTool struct{ m *Manager }

func (t *logSearchTool) Name() string { return "netdev_log_search" }

func (t *logSearchTool) Description() string {
	return "Search ONE pattern across MANY hosts' logs at once (the IOC sweep: given an IP/hash/keyword, find who mentioned it). " +
		"Fans out sealed log reads over every linux host (or the given device list), default sources /var/log/{syslog,messages,auth.log,secure}, " +
		"and returns hits with ±2 context lines. Reports covered vs total devices — a budget-stopped sweep never implies the rest are clean."
}

func (t *logSearchTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern":  {"type": "string", "description": "regex (invalid → literal substring) to find in log lines"},
			"devices":  {"type": "array", "items": {"type": "string"}, "description": "device names; omit = every linux host"},
			"sources":  {"type": "array", "items": {"type": "string"}, "description": "override defaults with file:/journal:/docker: sources (per-device whitelist applies)"},
			"since":    {"type": "string", "description": "keep lines from this time on: 2026-08-27 10:00:00 or -1h"}
		},
		"required": ["pattern"]
	}`)
}

func (t *logSearchTool) ReadOnly() bool { return true }

func (t *logSearchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Pattern string   `json:"pattern"`
		Devices []string `json:"devices"`
		Sources []string `json:"sources"`
		Since   string   `json:"since"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "", errors.New("netdev_log_search: pattern is required")
	}
	b, err := json.Marshal(t.m.LogSearch(ctx, a.Pattern, a.Devices, a.Sources, strings.TrimSpace(a.Since)))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
