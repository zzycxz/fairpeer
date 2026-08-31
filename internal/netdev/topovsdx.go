package netdev

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

// topovsdx.go — T2b: the .vsdx (Visio 2013+ OOXML) container feeding the
// SAME L1-L3 assembly as drawio. The legacy binary .vsd gets a clear
// refusal (spec §3.2 L0): "not a zip" is the tell.

// ── Visio page XML shapes ───────────────────────────────────────────────────

type vsPage struct {
	Shapes   []vsShape   `xml:"Shapes>Shape"`
	Connects []vsConnect `xml:"Connects>Connect"`
}

type vsShape struct {
	ID     string    `xml:"ID,attr"`
	Name   string    `xml:"Name,attr"`
	NameU  string    `xml:"NameU,attr"`
	Master string    `xml:"Master,attr"`
	Text   string    `xml:"Text"`
	PinX   float64   `xml:"PinX"`
	PinY   float64   `xml:"PinY"`
	Shapes []vsShape `xml:"Shapes>Shape"` // groups nest
}

type vsConnect struct {
	FromSheet string `xml:"FromSheet,attr"`
	FromCell  string `xml:"FromCell,attr"` // BeginX / EndX
	ToSheet   string `xml:"ToSheet,attr"`
}

type vsMasters struct {
	Masters []struct {
		ID    string `xml:"ID,attr"`
		Name  string `xml:"Name,attr"`
		NameU string `xml:"NameU,attr"`
	} `xml:"Master"`
}

var vsPageRe = regexp.MustCompile(`^visio/pages/page\d+\.xml$`)

// ImportVsdx parses a .vsdx file into the standard preview. deviceNames /
// deviceAddrs drive the L3 fusion, exactly like ImportDrawio.
func ImportVsdx(data []byte, deviceNames []string, deviceAddrs map[string]string) (*ImportTopoPreview, error) {
	if len(data) < 4 || !bytes.HasPrefix(data, []byte("PK")) {
		return nil, fmt.Errorf("vsdx: 文件不是 OOXML 包——Visio 老二进制 .vsd 不受支持，请用 Visio 2013+ 另存为 .vsdx")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("vsdx: %w", err)
	}
	var mastersFile []byte
	var pages []*zip.File
	for _, f := range zr.File {
		if strings.EqualFold(path.Base(f.Name), "masters.xml") && strings.Contains(f.Name, "masters") {
			mastersFile, _ = readZipFile(f)
		}
		if vsPageRe.MatchString(strings.ToLower(f.Name)) {
			pages = append(pages, f)
		}
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("vsdx: no visio/pages/page*.xml found (is this a network drawing?)")
	}
	masterNames := map[string]string{}
	if len(mastersFile) > 0 {
		var ms vsMasters
		if err := xml.Unmarshal(mastersFile, &ms); err == nil {
			for _, m := range ms.Masters {
				name := m.NameU
				if name == "" {
					name = m.Name
				}
				masterNames[m.ID] = name
			}
		}
	}

	pv := &ImportTopoPreview{Graph: TopologyGraph{At: nowClock(), Nodes: []TopologyNode{}, Edges: []TopologyEdge{}}, Warnings: []string{}}
	pv.Stats.Pages = len(pages)

	// Pick the page with the most shapes (flattened).
	var best vsPage
	var bestName string
	bestCount := -1
	for i := range pages {
		f := pages[i]
		rc, err := f.Open()
		if err != nil {
			pv.Warnings = append(pv.Warnings, fmt.Sprintf("page %s: %v", f.Name, err))
			continue
		}
		raw, _ := io.ReadAll(rc)
		rc.Close()
		var page vsPage
		if err := xml.Unmarshal(raw, &page); err != nil {
			pv.Warnings = append(pv.Warnings, fmt.Sprintf("page %s: %v", f.Name, err))
			continue
		}
		if n := len(flattenVsShapes(page.Shapes)); n > bestCount {
			best, bestName, bestCount = page, path.Base(f.Name), n
		}
	}
	if bestCount < 0 {
		return nil, fmt.Errorf("vsdx: no parseable pages")
	}
	pv.Stats.UsedPage = bestName
	if pv.Stats.Pages > 1 {
		pv.Warnings = append(pv.Warnings, fmt.Sprintf("多页文件：已取形状最多的页 %q（其余 %d 页未导入）", bestName, pv.Stats.Pages-1))
	}

	// L1+L2: flattened shapes with text (or master names) become nodes.
	verts := map[string]TopologyNode{}
	geom := map[string]vref{}
	knownLower := map[string]bool{}
	for _, n := range deviceNames {
		knownLower[strings.ToLower(strings.TrimSpace(n))] = true
	}
	addrToDevice := map[string]string{}
	for addr, name := range deviceAddrs {
		addrToDevice[strings.TrimSpace(addr)] = name
	}
	for _, s := range flattenVsShapes(best.Shapes) {
		label := strings.TrimSpace(s.Text)
		shapeName := strings.TrimSpace(s.NameU)
		if shapeName == "" {
			shapeName = strings.TrimSpace(s.Name)
		}
		if m := masterNames[s.Master]; m != "" {
			shapeName = shapeName + " " + m
		}
		if label == "" {
			label = shapeName // masterless text-less shapes still carry a name
		}
		if label == "" {
			continue
		}
		// Visio carries device class in master/shape NAMES (not the drawio
		// `shape=` style syntax) — run the word table directly.
		role, src := RoleUnknown, RoleSourceNone
		if r, ok := roleFromWords(shapeName); ok {
			role, src = r, "shape"
		} else if r, ok := roleFromWords(label); ok {
			role, src = r, RoleSourceLabel
		}
		node := TopologyNode{Name: label, Managed: false, Tier: -1, Role: role, RoleSource: src}
		if knownLower[strings.ToLower(label)] {
			node.Managed = true
		} else if addr := firstIPv4In(label); addr != "" {
			if name, ok := addrToDevice[addr]; ok {
				node.Managed = true
				node.Name = name
				node.DeviceIP = addr
			} else {
				node.DeviceIP = addr
			}
		}
		verts[s.ID] = node
		geom[s.ID] = vref{x: s.PinX, y: s.PinY}
	}

	// L1 edges: Visio stores one Connect per endpoint (BeginX/EndX) sharing
	// the connector's FromSheet — pair them up per connector.
	type ep struct{ cell, sheet string }
	byConn := map[string][]ep{}
	for _, c := range best.Connects {
		byConn[c.FromSheet] = append(byConn[c.FromSheet], ep{c.FromCell, c.ToSheet})
	}
	var edgePairs [][2]string
	for _, eps := range byConn {
		var begin, end string
		for _, e := range eps {
			switch {
			case strings.HasPrefix(strings.ToLower(e.cell), "begin"):
				begin = e.sheet
			case strings.HasPrefix(strings.ToLower(e.cell), "end"):
				end = e.sheet
			}
		}
		if begin != "" && end != "" && begin != end {
			if _, ok := verts[begin]; ok {
				if _, ok := verts[end]; ok {
					edgePairs = append(edgePairs, [2]string{begin, end})
				}
			}
		}
	}
	return assembleDesign(verts, geom, edgePairs, pv)
}

func flattenVsShapes(shapes []vsShape) []vsShape {
	var out []vsShape
	for _, s := range shapes {
		out = append(out, s)
		if len(s.Shapes) > 0 {
			out = append(out, flattenVsShapes(s.Shapes)...)
		}
	}
	return out
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 8<<20)) // 8MB page cap — guardrail
}
