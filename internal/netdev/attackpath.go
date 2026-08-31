package netdev

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// attackpath.go — F5: attack-path SIMULATION (spec §4.6). Pure data-plane
// graph computation over what the product already knows: exposure findings
// (weak creds, telnet/baseline) × adjacency edges (bastion chains, design
// imports, LLDP snapshots) → "if this exposure point fell, what is within
// reach". ZERO network connections — the report is labeled 推演 and every
// hop cites its evidence edge. The companion red-line: this file never
// dials, so there is nothing to police.

const (
	attackPathMaxHops   = 3
	attackPathCapTotal  = 200 // bounds: wide fan-outs summarize, never flood
	attackPathCapPerExp = 50
)

// AttackPathStep is one hop with the edge that supports it.
type AttackPathStep struct {
	From string `json:"from"`
	To   string `json:"to"`
	Via  string `json:"via"` // edge source: bastion | design | lldp | cdp
}

// AttackPath is one simulated route from an exposure point to a reachable asset.
type AttackPath struct {
	ExposureDevice string           `json:"exposure_device"`
	Reason         string           `json:"reason"` // the finding that marks the exposure
	FindingID      string           `json:"finding_id,omitempty"`
	Steps          []AttackPathStep `json:"steps"`
	EndDevice      string           `json:"end_device"`
	EndRole        string           `json:"end_role,omitempty"`
	EndManaged     bool             `json:"end_managed"`
	Hops           int              `json:"hops"`
	Score          int              `json:"score"`
}

// ExposurePoint is one 起点 with its simulated blast radius.
type ExposurePoint struct {
	Device    string `json:"device"`
	FindingID string `json:"finding_id,omitempty"`
	Reason    string `json:"reason"`
	Paths     int    `json:"paths"`
}

// CutSuggestion ranks edges by how many simulated paths they carry — cutting
// the top edges removes the most reach (a prioritization hint for proposals,
// never an executed change).
type CutSuggestion struct {
	From         string `json:"from"`
	To           string `json:"to"`
	Via          string `json:"via"`
	PathsRemoved int    `json:"paths_removed"`
}

// AttackPathReport is the whole simulation output.
type AttackPathReport struct {
	GeneratedAt    string          `json:"generated_at"`
	Simulated      bool            `json:"simulated"` // constant true — 推演, not 实测
	EdgeSources    []string        `json:"edge_sources"`
	Nodes          int             `json:"nodes"`
	Edges          int             `json:"edges"`
	ExposurePoints []ExposurePoint `json:"exposure_points"`
	Paths          []AttackPath    `json:"paths"`
	Cuts           []CutSuggestion `json:"cut_suggestions"`
}

// exposureTitleRe marks findings that constitute an exposure POINT (weak
// credentials, plaintext management planes, known-vuln confirmations).
var exposureTitleRe = regexp.MustCompile(`(?i)弱口令|weak.?cred|telnet|snmp.?(v1|v2c)|ssh-?v1|明文|密码.*(简|弱)|暴露`)

// roleWeight ranks endpoint value for path scoring — a heuristic ladder, not
// an SLA. Servers/firewalls sit at the top because they concentrate reach
// and data.
func roleWeight(role string) int {
	switch role {
	case RoleServer, RoleFirewall:
		return 3
	case RoleRouter, RoleSwitch:
		return 2
	default:
		return 1
	}
}

// BuildAttackPaths runs the simulation. Pure function: graph + findings in,
// report out. Callers assemble the graph from zero-session sources (IP-plan
// inference, design imports, persisted snapshots) — the sim itself connects
// to nothing.
func BuildAttackPaths(g TopologyGraph, findings []*Finding) *AttackPathReport {
	r := &AttackPathReport{
		GeneratedAt: time.Now().Format("2006-01-02T15:04"),
		Simulated:   true,
		Nodes:       len(g.Nodes),
		Edges:       len(g.Edges),
	}
	// Adjacency (undirected: reachability works both ways) + node meta.
	adj := map[string][]AttackPathStep{}
	nodeMeta := map[string]TopologyNode{}
	edgeSources := map[string]bool{}
	for _, n := range g.Nodes {
		nodeMeta[n.Name] = n
	}
	for _, e := range g.Edges {
		if e.LocalDevice == "" || e.RemoteDevice == "" || e.LocalDevice == e.RemoteDevice {
			continue
		}
		adj[e.LocalDevice] = append(adj[e.LocalDevice], AttackPathStep{From: e.LocalDevice, To: e.RemoteDevice, Via: e.Source})
		adj[e.RemoteDevice] = append(adj[e.RemoteDevice], AttackPathStep{From: e.RemoteDevice, To: e.LocalDevice, Via: e.Source})
		edgeSources[e.Source] = true
	}
	for s := range edgeSources {
		r.EdgeSources = append(r.EdgeSources, s)
	}
	sort.Strings(r.EdgeSources)

	// Exposure points: open findings with exposure-shaped titles.
	type exp struct {
		device, id, reason string
	}
	var exps []exp
	seenExp := map[string]bool{}
	for _, f := range findings {
		if f.Status == "resolved" || len(f.Devices) == 0 {
			continue
		}
		if !exposureTitleRe.MatchString(f.Title) && f.Severity != SeverityCritical {
			continue
		}
		for _, d := range f.Devices {
			key := d + "\x00" + f.ID
			if seenExp[key] {
				continue
			}
			seenExp[key] = true
			exps = append(exps, exp{device: d, id: f.ID, reason: f.Title})
		}
	}

	edgePathCount := map[AttackPathStep]int{}
	for _, e := range exps {
		ep := ExposurePoint{Device: e.device, FindingID: e.id, Reason: e.reason}
		// DFS over simple paths, depth ≤3, per-exposure cap.
		var walk func(node string, trail []AttackPathStep, visited map[string]bool)
		walk = func(node string, trail []AttackPathStep, visited map[string]bool) {
			if len(trail) >= attackPathMaxHops || ep.Paths >= attackPathCapPerExp || len(r.Paths) >= attackPathCapTotal {
				return
			}
			for _, s := range adj[node] {
				if visited[s.To] {
					continue
				}
				next := append(append([]AttackPathStep{}, trail...), s)
				visited[s.To] = true
				end := nodeMeta[s.To]
				path := AttackPath{
					ExposureDevice: e.device,
					Reason:         e.reason,
					FindingID:      e.id,
					Steps:          next,
					EndDevice:      s.To,
					EndRole:        end.Role,
					EndManaged:     end.Managed,
					Hops:           len(next),
				}
				path.Score = roleWeight(end.Role) * (attackPathMaxHops + 1 - len(next))
				r.Paths = append(r.Paths, path)
				ep.Paths++
				for _, st := range next {
					// Cuts aggregate UNDIRECTED: both directions of one link
					// are one cut candidate.
					if st.From > st.To {
						st.From, st.To = st.To, st.From
					}
					edgePathCount[st]++
				}
				walk(s.To, next, visited)
				delete(visited, s.To)
			}
		}
		walk(e.device, nil, map[string]bool{e.device: true})
		if ep.Paths > 0 || nodeExists(nodeMeta, e.device) {
			r.ExposurePoints = append(r.ExposurePoints, ep)
		}
	}
	sort.Slice(r.Paths, func(i, j int) bool {
		if r.Paths[i].Score != r.Paths[j].Score {
			return r.Paths[i].Score > r.Paths[j].Score
		}
		return r.Paths[i].Hops < r.Paths[j].Hops
	})
	// Cut suggestions: edges carrying the most paths.
	for st, n := range edgePathCount {
		r.Cuts = append(r.Cuts, CutSuggestion{From: st.From, To: st.To, Via: st.Via, PathsRemoved: n})
	}
	sort.Slice(r.Cuts, func(i, j int) bool { return r.Cuts[i].PathsRemoved > r.Cuts[j].PathsRemoved })
	if len(r.Cuts) > 5 {
		r.Cuts = r.Cuts[:5]
	}
	return r
}

func nodeExists(meta map[string]TopologyNode, name string) bool {
	_, ok := meta[name]
	return ok
}

// RenderAttackPaths turns a report into the OpsResult-style text block the
// UI and the daily briefing both carry. 推演 watermark first line, always.
func RenderAttackPaths(r *AttackPathReport) string {
	if r == nil || len(r.Paths) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[推演] 暴露面路径 ")
	b.WriteString(strings.Join(r.EdgeSources, "+"))
	b.WriteString(" · 暴露点 ")
	fmt.Fprintf(&b, "%d · 路径 %d\n", len(r.ExposurePoints), len(r.Paths))
	for i, p := range r.Paths {
		if i >= 10 {
			fmt.Fprintf(&b, "… 共 %d 条\n", len(r.Paths))
			break
		}
		hops := make([]string, 0, len(p.Steps))
		for _, s := range p.Steps {
			hops = append(hops, s.To)
		}
		managed := ""
		if !p.EndManaged {
			managed = "·未纳管"
		}
		fmt.Fprintf(&b, "- %s → %s（%d 跳，%s%s，评分 %d）\n",
			p.ExposureDevice, strings.Join(hops, " → "), p.Hops,
			p.EndRole, managed, p.Score)
	}
	if len(r.Cuts) > 0 {
		b.WriteString("优先切断（按消掉路径数）：")
		for i, c := range r.Cuts {
			if i > 0 {
				b.WriteString("；")
			}
			fmt.Fprintf(&b, "%s—%s(%d)", c.From, c.To, c.PathsRemoved)
		}
		b.WriteString("\n")
	}
	return b.String()
}
