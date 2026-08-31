package netdev

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// topoimport.go — T2a: the draw.io design-import pipeline (spec §3).
// Deterministic layers, degrade never fail:
//   L0 container — mxfile XML, base64+deflate diagram variant, multi-page
//                  (page with the most nodes wins; the rest warn)
//   L1 structure — labeled vertices + edge cells (100% deterministic)
//   L2 semantics — shape-library name > label words (the §2.3 word table)
//   L3 fusion    — inventory match by name (case-folded) or embedded address
//
// The parser is untrusted-input code: malformed XML returns an error, never
// a panic (invariant 7 — fuzzed in tests). vsdx arrives with T2b behind the
// same Preview/Apply seam.

// TopoImportStats summarizes one preview.
type TopoImportStats struct {
	Total          int    `json:"total"`
	Managed        int    `json:"managed"`
	NewNodes       int    `json:"new_nodes"`
	UnresolvedRole int    `json:"unresolved_role"`
	Pages          int    `json:"pages"`
	UsedPage       string `json:"used_page"`
}

// ImportTopoPreview is the no-side-effect parse result the UI confirms.
type ImportTopoPreview struct {
	Graph    TopologyGraph   `json:"graph"`
	Stats    TopoImportStats `json:"stats"`
	Warnings []string        `json:"warnings"`
}

// TopologyDesign is the persisted third source (plan | design | snapshot).
type TopologyDesign struct {
	ImportedAt string        `json:"imported_at"`
	SourceFile string        `json:"source_file"`
	Graph      TopologyGraph `json:"graph"`
}

var (
	designMu        chan struct{} = make(chan struct{}, 1)
	designFileOverr string
)

func designFile() string {
	if designFileOverr != "" {
		return designFileOverr
	}
	return filepath.Join(netdevStateDir(), "topology-design.json")
}

// SaveTopologyDesign persists the confirmed design snapshot (idempotent
// overwrite; audited by the bridge caller).
func SaveTopologyDesign(d *TopologyDesign) error {
	designMu <- struct{}{}
	defer func() { <-designMu }()
	if d.ImportedAt == "" {
		d.ImportedAt = time.Now().Format(time.RFC3339)
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	StateEventSnap(StateEventTopo, "", StateActorUser, designFile())
	return os.WriteFile(designFile(), b, 0o600)
}

// LoadTopologyDesign returns the stored design, or nil when none exists.
func LoadTopologyDesign() (*TopologyDesign, error) {
	designMu <- struct{}{}
	defer func() { <-designMu }()
	b, err := os.ReadFile(designFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var d TopologyDesign
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("topology-design.json: %w", err)
	}
	return &d, nil
}

// ── L0: the mxfile container ────────────────────────────────────────────────

type mxFile struct {
	Diagrams []mxDiagram `xml:"diagram"`
}

type mxDiagram struct {
	Name  string        `xml:"name,attr"`
	Model *mxGraphModel `xml:"mxGraphModel"`
	// Compressed variant: the model rides as base64(deflate(xml)) text.
	Content    string `xml:",chardata"`
	Style      string `xml:"style,attr"` // host="app.diagrams.net" etc.
	Compressed bool   `xml:"compressed,attr"`
}

type mxGraphModel struct {
	Root struct {
		Cells []mxCell `xml:"mxCell"`
	} `xml:"root"`
}

type mxCell struct {
	ID     string      `xml:"id,attr"`
	Value  string      `xml:"value,attr"`
	Style  string      `xml:"style,attr"`
	Vertex string      `xml:"vertex,attr"`
	Edge   string      `xml:"edge,attr"`
	Source string      `xml:"source,attr"`
	Target string      `xml:"target,attr"`
	Geom   *mxGeometry `xml:"mxGeometry"`
}

type mxGeometry struct {
	X      float64 `xml:"x,attr"`
	Y      float64 `xml:"y,attr"`
	Width  float64 `xml:"width,attr"`
	Height float64 `xml:"height,attr"`
}

// diagramModel decodes one diagram to its graph model, inflating the
// base64+deflate variant when present.
func diagramModel(d mxDiagram) (*mxGraphModel, error) {
	if d.Model != nil {
		return d.Model, nil
	}
	text := strings.TrimSpace(d.Content)
	if text == "" {
		return nil, fmt.Errorf("diagram %q: no model content", d.Name)
	}
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		// Plain XML without the wrapper also parses (some exporters).
		var m mxGraphModel
		if xerr := xml.Unmarshal([]byte(text), &m); xerr == nil && len(m.Root.Cells) > 0 {
			return &m, nil
		}
		return nil, fmt.Errorf("diagram %q: content is neither base64 nor XML: %w", d.Name, err)
	}
	fr := flate.NewReader(bytes.NewReader(raw))
	defer fr.Close()
	var m mxGraphModel
	if err := xml.NewDecoder(fr).Decode(&m); err != nil {
		return nil, fmt.Errorf("diagram %q: inflate: %w", d.Name, err)
	}
	return &m, nil
}

// ── L2: shape-name seam ─────────────────────────────────────────────────────

var shapeNameRe = regexp.MustCompile(`shape=([A-Za-z0-9_.-]+)`)

// roleFromStyleAndLabel: the §2.3 table over "shape name label" as one string
// — shape-library names are English device nouns ("…cisco.routers.router"),
// labels carry the bilingual words. Shape hit wins on provenance.
func roleFromStyleAndLabel(style, label string) (role, source string) {
	shape := ""
	if m := shapeNameRe.FindStringSubmatch(style); m != nil {
		shape = m[1]
	}
	if r, ok := roleFromWords(shape); ok {
		return r, "shape"
	}
	if r, ok := roleFromWords(label); ok {
		return r, RoleSourceLabel
	}
	return RoleUnknown, RoleSourceNone
}

// ── the pipeline ────────────────────────────────────────────────────────────

// ImportDrawio parses a .drawio/.xml design into a preview. deviceNames and
// deviceAddrs drive the L3 fusion (managed markers); empty slices are fine.
func ImportDrawio(xmlText string, deviceNames []string, deviceAddrs map[string]string) (*ImportTopoPreview, error) {
	var file mxFile
	if err := xml.Unmarshal([]byte(xmlText), &file); err != nil {
		return nil, fmt.Errorf("drawio: %w", err)
	}
	if len(file.Diagrams) == 0 {
		// A bare <mxGraphModel> is also a legal single-page file.
		var m mxGraphModel
		if err := xml.Unmarshal([]byte(xmlText), &m); err == nil && len(m.Root.Cells) > 0 {
			file.Diagrams = append(file.Diagrams, mxDiagram{Name: "page-1", Model: &m})
		} else {
			return nil, fmt.Errorf("drawio: no <diagram> pages found")
		}
	}

	pv := &ImportTopoPreview{Graph: TopologyGraph{At: time.Now().Format("15:04:05"), Nodes: []TopologyNode{}, Edges: []TopologyEdge{}}, Warnings: []string{}}
	pv.Stats.Pages = len(file.Diagrams)

	// Pick the page with the most vertex cells; the others warn.
	var best *mxGraphModel
	var bestName string
	var bestCount int
	for i := range file.Diagrams {
		m, err := diagramModel(file.Diagrams[i])
		if err != nil {
			pv.Warnings = append(pv.Warnings, err.Error())
			continue
		}
		n := 0
		for _, c := range m.Root.Cells {
			if c.Vertex == "1" {
				n++
			}
		}
		if n > bestCount {
			best, bestName, bestCount = m, file.Diagrams[i].Name, n
		}
	}
	if best == nil {
		return nil, fmt.Errorf("drawio: no parseable pages (%d tried)", len(file.Diagrams))
	}
	pv.Stats.UsedPage = bestName
	if pv.Stats.Pages > 1 {
		pv.Warnings = append(pv.Warnings, fmt.Sprintf("多页文件：已取节点最多的页 %q（其余 %d 页未导入）", bestName, pv.Stats.Pages-1))
	}

	knownLower := map[string]bool{}
	for _, n := range deviceNames {
		knownLower[strings.ToLower(strings.TrimSpace(n))] = true
	}
	addrByNameLower := map[string]string{}
	for _, n := range deviceNames {
		addrByNameLower[strings.ToLower(strings.TrimSpace(n))] = n
	}
	addrToDevice := map[string]string{}
	for addr, name := range deviceAddrs {
		addrToDevice[strings.TrimSpace(addr)] = name
	}

	// L1+L2: vertices with labels become nodes; geometry rides along inside
	// Subnet (reused as the y-rank carrier for tier banding).
	verts := map[string]TopologyNode{}
	geom := map[string]vref{}
	for _, c := range best.Root.Cells {
		if c.Vertex != "1" {
			continue
		}
		label := strings.TrimSpace(htmlUnescape(c.Value))
		if label == "" {
			continue // decorative shapes stay out of the graph
		}
		role, src := roleFromStyleAndLabel(c.Style, label)
		node := TopologyNode{Name: label, Managed: false, Tier: -1, Role: role, RoleSource: src}
		// L3: exact/case-folded name, or an address embedded in the label.
		if knownLower[strings.ToLower(label)] {
			node.Managed = true
			if canon, ok := addrByNameLower[strings.ToLower(label)]; ok {
				node.Name = canon
			}
		} else if addr := firstIPv4In(label); addr != "" {
			if name, ok := addrToDevice[addr]; ok {
				node.Managed = true
				node.Name = name
				node.DeviceIP = addr
			} else {
				node.DeviceIP = addr
			}
		}
		if c.Geom != nil {
			geom[c.ID] = vref{x: c.Geom.X, y: c.Geom.Y}
		}
		if _, dup := verts[c.ID]; dup {
			continue
		}
		verts[c.ID] = node
	}
	// L1: edges reference vertices by id.
	var edgePairs [][2]string
	for _, c := range best.Root.Cells {
		if c.Edge != "1" || c.Source == "" || c.Target == "" {
			continue
		}
		if _, ok := verts[c.Source]; !ok {
			continue
		}
		if _, ok := verts[c.Target]; !ok {
			continue
		}
		edgePairs = append(edgePairs, [2]string{c.Source, c.Target})
	}
	return assembleDesign(verts, geom, edgePairs, pv)
}

// nowClock is the graphs' display timestamp format.
func nowClock() string { return time.Now().Format("15:04:05") }

// vref is one vertex's design-canvas geometry (the y-rank feeds tier banding).
type vref struct {
	x, y float64
}

// assembleDesign is the shared L1-L3 tail of both importers (drawio now,
// vsdx in T2b): edges + dedup, y-rank tiering, stats, warnings.
func assembleDesign(verts map[string]TopologyNode, geom map[string]vref, edgePairs [][2]string, pv *ImportTopoPreview) (*ImportTopoPreview, error) {
	for _, pair := range edgePairs {
		s, t := verts[pair[0]], verts[pair[1]]
		if s.Name == t.Name {
			continue
		}
		pv.Graph.Edges = append(pv.Graph.Edges, TopologyEdge{
			LocalDevice:  s.Name,
			RemoteDevice: t.Name,
			Source:       "design",
		})
	}
	// Dedup edges (two shapes connected by two lines is common).
	seenEdge := map[string]bool{}
	dedup := pv.Graph.Edges[:0]
	for _, e := range pv.Graph.Edges {
		k := e.LocalDevice + "\x00" + e.RemoteDevice
		k2 := e.RemoteDevice + "\x00" + e.LocalDevice
		if seenEdge[k] || seenEdge[k2] {
			continue
		}
		seenEdge[k] = true
		dedup = append(dedup, e)
	}
	pv.Graph.Edges = dedup

	// Tier from the y-position rank: the design's vertical order survives as
	// band assignment (L2 position fallback, spec §3.2).
	ys := map[float64]bool{}
	ids := make([]string, 0, len(verts))
	for id := range verts {
		ids = append(ids, id)
		if g, ok := geom[id]; ok {
			ys[g.y] = true
		}
	}
	sorted := make([]float64, 0, len(ys))
	for y := range ys {
		sorted = append(sorted, y)
	}
	sort.Float64s(sorted)
	rank := map[float64]int{}
	for i, y := range sorted {
		if _, dup := rank[y]; !dup {
			rank[y] = i
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := geom[ids[i]], geom[ids[j]]
		if a.y != b.y {
			return a.y < b.y
		}
		return a.x < b.x
	})
	for _, id := range ids {
		n := verts[id]
		if n.Managed && n.Tier == -1 {
			if g, ok := geom[id]; ok && len(sorted) > 1 {
				n.Tier = rank[g.y] * 3 / len(sorted) // 0..2 band by height
			}
		}
		if n.Role == RoleUnknown {
			pv.Stats.UnresolvedRole++
		}
		if n.Managed {
			pv.Stats.Managed++
		} else {
			pv.Stats.NewNodes++
		}
		pv.Stats.Total++
		pv.Graph.Nodes = append(pv.Graph.Nodes, n)
	}
	if pv.Stats.UnresolvedRole > 0 {
		pv.Warnings = append(pv.Warnings, fmt.Sprintf("%d 个节点未识别设备类型（显示为未知图标）——清单里配置 role 可修正", pv.Stats.UnresolvedRole))
	}
	return pv, nil
}

var ipv4Re = regexp.MustCompile(`\b(\d{1,3}(?:\.\d{1,3}){3})\b`)

func firstIPv4In(s string) string {
	return ipv4Re.FindString(s)
}

// htmlUnescape covers the five entities draw.io uses in value attributes.
func htmlUnescape(s string) string {
	r := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&amp;", "&")
	return r.Replace(s)
}
