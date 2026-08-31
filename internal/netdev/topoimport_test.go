package netdev

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// A cisco-stencil page: shape names carry the roles, labels match inventory
// names case-insensitively, one plain box stays unclassified.
const drawioCiscoFixture = `<?xml version="1.0"?>
<mxfile host="app.diagrams.net">
  <diagram id="p1" name="校园网">
    <mxGraphModel dx="800" dy="600">
      <root>
        <mxCell id="0"/>
        <mxCell id="1" parent="0"/>
        <mxCell id="n1" value="fw-01" style="shape=mxgraph.cisco.security.firewall;html=1" vertex="1" parent="1">
          <mxGeometry x="80" y="20" width="60" height="40"/></mxCell>
        <mxCell id="n2" value="CORE-SW-1" style="shape=mxgraph.cisco.switches.workgroup_switch;html=1" vertex="1" parent="1">
          <mxGeometry x="80" y="140" width="60" height="40"/></mxCell>
        <mxCell id="n3" value="出口路由器 R1" style="rounded=1" vertex="1" parent="1">
          <mxGeometry x="80" y="260" width="60" height="40"/></mxCell>
        <mxCell id="n4" value="web-10.0.2.31" style="rounded=0" vertex="1" parent="1">
          <mxGeometry x="300" y="260" width="60" height="40"/></mxCell>
        <mxCell id="e1" style="edgeStyle=none" edge="1" parent="1" source="n1" target="n2"><mxGeometry relative="1"/></mxCell>
        <mxCell id="e2" style="edgeStyle=none" edge="1" parent="1" source="n2" target="n4"><mxGeometry relative="1"/></mxCell>
        <mxCell id="e3" style="edgeStyle=none" edge="1" parent="1" source="n2" target="n4"><mxGeometry relative="1"/></mxCell>
      </root>
    </mxGraphModel>
  </diagram>
</mxfile>`

func TestImportDrawioCisco(t *testing.T) {
	old := designFileOverr
	designFileOverr = filepath.Join(t.TempDir(), "design.json")
	t.Cleanup(func() { designFileOverr = old })

	names := []string{"FW-01", "CORE-SW-1"}
	addrs := map[string]string{"10.0.2.31": "WEB-1"}
	pv, err := ImportDrawio(drawioCiscoFixture, names, addrs)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Stats.Total != 4 {
		t.Fatalf("total = %d, nodes = %+v", pv.Stats.Total, pv.Graph.Nodes)
	}
	if pv.Stats.Managed != 3 || pv.Stats.NewNodes != 1 {
		t.Errorf("managed/new = %d/%d", pv.Stats.Managed, pv.Stats.NewNodes)
	}
	byName := map[string]TopologyNode{}
	for _, n := range pv.Graph.Nodes {
		byName[n.Name] = n
	}
	if n := byName["FW-01"]; !n.Managed || n.Role != RoleFirewall || n.RoleSource != "shape" {
		t.Errorf("FW-01 = %+v (case-folded name match + cisco shape role)", n)
	}
	if n := byName["CORE-SW-1"]; !n.Managed || n.Role != RoleSwitch {
		t.Errorf("CORE-SW-1 = %+v", n)
	}
	if n := byName["出口路由器 R1"]; n.Managed || n.Role != RoleRouter || n.RoleSource != RoleSourceLabel {
		t.Errorf("R1 = %+v (label-word role, unmanaged)", n)
	}
	if n := byName["WEB-1"]; !n.Managed || n.DeviceIP != "10.0.2.31" {
		t.Errorf("WEB-1 = %+v (address-in-label fusion)", n)
	}
	// Duplicate edge n2→n4 collapses; the firewall edge survives.
	if len(pv.Graph.Edges) != 2 {
		t.Errorf("edges = %+v (dup not collapsed)", pv.Graph.Edges)
	}
	if pv.Graph.Edges[0].Source != "design" {
		t.Errorf("edge source = %q", pv.Graph.Edges[0].Source)
	}
	// The y-order survives as tier: firewall (y=20) ranks above the rest.
	if byName["FW-01"].Tier != 0 {
		t.Errorf("FW-01 tier = %d, want 0 (top of the design)", byName["FW-01"].Tier)
	}

	// Persist + reload roundtrip (the Apply half).
	d := &TopologyDesign{SourceFile: "campus.drawio", Graph: pv.Graph}
	if err := SaveTopologyDesign(d); err != nil {
		t.Fatal(err)
	}
	got, err := LoadTopologyDesign()
	if err != nil || got == nil {
		t.Fatalf("load: %v %v", got, err)
	}
	if got.SourceFile != "campus.drawio" || len(got.Graph.Nodes) != 4 || got.ImportedAt == "" {
		t.Errorf("roundtrip = %+v", got)
	}
}

// The compressed variant: base64(deflate(mxGraphModel)) inside <diagram>.
func TestImportDrawioCompressed(t *testing.T) {
	model := `<mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/>
<mxCell id="a" value="SW-A" style="shape=mxgraph.cisco.switches.switch" vertex="1" parent="1"><mxGeometry x="10" y="10" width="60" height="40"/></mxCell>
<mxCell id="b" value="SW-B" style="rounded=1" vertex="1" parent="1"><mxGeometry x="10" y="110" width="60" height="40"/></mxCell>
<mxCell id="e" edge="1" parent="1" source="a" target="b"/></root></mxGraphModel>`
	var buf bytes.Buffer
	fw, _ := flate.NewWriter(&buf, flate.DefaultCompression)
	fw.Write([]byte(model))
	fw.Close()
	enc := base64.StdEncoding.EncodeToString(buf.Bytes())
	doc := fmt.Sprintf(`<mxfile><diagram id="c1" name="压缩页">%s</diagram><diagram id="c2" name="空页"><mxGraphModel><root><mxCell id="0"/></root></mxGraphModel></diagram></mxfile>`, enc)

	pv, err := ImportDrawio(doc, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Stats.Total != 2 || len(pv.Graph.Edges) != 1 {
		t.Fatalf("stats = %+v edges = %+v", pv.Stats, pv.Graph.Edges)
	}
	if pv.Stats.Pages != 2 || pv.Stats.UsedPage != "压缩页" {
		t.Errorf("pages = %+v", pv.Stats)
	}
	if len(pv.Warnings) == 0 || !strings.Contains(pv.Warnings[0], "多页") {
		t.Errorf("warnings = %v (multi-page note expected)", pv.Warnings)
	}
}

// Robustness (invariant 7): garbage, truncation and empty files error out
// cleanly — never panic, never return a graph.
func TestImportDrawioRobustness(t *testing.T) {
	bad := []string{
		"", "   ", "not xml at all", "<mxfile>", "<mxfile><diagram>zzzz</diagram></mxfile>",
		"<mxfile><diagram name='p'>!!!not-base64!!!</diagram></mxfile>",
		"<mxfile><diagram>PDw8</diagram></mxfile>", // valid b64, garbage bytes
		strings.Repeat("<mxCell ", 5000),
	}
	for _, s := range bad {
		if pv, err := ImportDrawio(s, nil, nil); err == nil {
			t.Errorf("ImportDrawio(%q...) succeeded: %+v", s[:min(len(s), 20)], pv)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
