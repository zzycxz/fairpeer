package netdev

import (
	"strings"
	"testing"
	"time"
)

func attackFixtureGraph() TopologyGraph {
	return TopologyGraph{
		Nodes: []TopologyNode{
			{Name: "EDGE-1", Managed: true, Role: RoleFirewall},
			{Name: "CORE-1", Managed: true, Role: RoleSwitch},
			{Name: "AGG-1", Managed: true, Role: RoleSwitch},
			{Name: "SRV-1", Managed: true, Role: RoleServer},
			{Name: "GUEST-9", Managed: false},
		},
		Edges: []TopologyEdge{
			{LocalDevice: "EDGE-1", RemoteDevice: "CORE-1", Source: "bastion"},
			{LocalDevice: "CORE-1", RemoteDevice: "AGG-1", Source: "design"},
			{LocalDevice: "AGG-1", RemoteDevice: "SRV-1", Source: "design"},
			{LocalDevice: "AGG-1", RemoteDevice: "GUEST-9", Source: "lldp"},
		},
	}
}

func TestBuildAttackPaths(t *testing.T) {
	findings := []*Finding{
		{ID: "F1", Title: "弱口令确认：EDGE-1 ssh root/123456", Severity: SeverityCritical, Devices: []string{"EDGE-1"}, CreatedAt: time.Now()},
		{ID: "F2", Title: "telnet-enabled：AGG-1 管理面明文开放", Severity: SeverityWarning, Devices: []string{"AGG-1"}, CreatedAt: time.Now()},
		{ID: "F3", Title: "磁盘水位高", Severity: SeverityWarning, Devices: []string{"SRV-1"}, CreatedAt: time.Now()},                           // not an exposure
		{ID: "F4", Title: "弱口令确认（已修复）", Severity: SeverityCritical, Devices: []string{"SRV-1"}, Status: "resolved", CreatedAt: time.Now()}, // resolved → skipped
	}
	r := BuildAttackPaths(attackFixtureGraph(), findings)
	if !r.Simulated {
		t.Error("report must be labeled simulated (推演)")
	}
	if len(r.EdgeSources) != 3 || r.Edges != 4 || r.Nodes != 5 {
		t.Errorf("meta = %+v", r)
	}
	// Two live exposure points; F3/F4 contribute nothing.
	if len(r.ExposurePoints) != 2 {
		t.Fatalf("exposure points = %+v", r.ExposurePoints)
	}
	// EDGE-1 reaches CORE-1(1), AGG-1(2), SRV-1(3), GUEST-9(3) = 4 paths;
	// AGG-1 reaches CORE-1(1), EDGE-1(2), SRV-1(1), GUEST-9(1) = 4 paths.
	byExp := map[string]int{}
	for _, p := range r.Paths {
		byExp[p.ExposureDevice]++
	}
	if byExp["EDGE-1"] != 4 || byExp["AGG-1"] != 4 {
		t.Errorf("paths per exposure = %v (total %d)", byExp, len(r.Paths))
	}
	// Top-ranked path: a server endpoint (weight 3) — SRV-1 from AGG-1 at 1 hop
	// scores 3*3=9, SRV-1 from EDGE-1 at 3 hops scores 3*1=3.
	top := r.Paths[0]
	if top.EndDevice != "SRV-1" || top.Hops != 1 || top.Score != 9 {
		t.Errorf("top path = %+v", top)
	}
	// Every path carries citable steps with via labels.
	for _, p := range r.Paths {
		if len(p.Steps) != p.Hops {
			t.Fatalf("steps/hops mismatch: %+v", p)
		}
		for _, s := range p.Steps {
			if s.Via == "" {
				t.Fatalf("step without evidence edge: %+v", s)
			}
		}
	}
	// Cut ranking (undirected): CORE-1—AGG-1 carries EDGE-1's three deep
	// paths plus AGG-1's two core-side paths = 5; CORE-1—EDGE-1 ties at 5
	// (4 from EDGE-1's fan + AGG-1's swing). Top slot may hold either.
	c0 := r.Cuts[0]
	if c0.PathsRemoved != 5 {
		t.Errorf("top cut = %+v, want 5 paths", c0)
	}
	if txt := RenderAttackPaths(r); !strings.Contains(txt, "推演") || !strings.Contains(txt, "SRV-1") {
		t.Errorf("render = %q", txt)
	}
}

func TestBuildAttackPathsEmpty(t *testing.T) {
	r := BuildAttackPaths(attackFixtureGraph(), nil)
	if len(r.Paths) != 0 || len(r.ExposurePoints) != 0 {
		t.Errorf("no findings → no paths: %+v", r)
	}
	if RenderAttackPaths(r) != "" {
		t.Error("empty report renders empty")
	}
}
