package netdev

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildVsdx assembles a minimal OOXML package in memory.
func buildVsdx(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const vsdxMasters = `<?xml version="1.0"?>
<Masters>
  <Master ID="1" NameU="Router" Name="路由器"/>
  <Master ID="2" NameU="Firewall" Name="防火墙"/>
</Masters>`

const vsdxPage1 = `<?xml version="1.0"?>
<Page Contents="1">
  <Shapes>
    <Shape ID="10" Type="Shape" Master="2">
      <PinX>4</PinX><PinY>10</PinY>
      <Text>FW-01</Text>
    </Shape>
    <Shape ID="11" Type="Shape" Master="1">
      <PinX>4</PinX><PinY>7</PinY>
      <Text>R1-出口</Text>
    </Shape>
    <Shape ID="12" Type="Shape">
      <PinX>4</PinX><PinY>4</PinY>
      <Text>SRV-10.0.2.31</Text>
      <Shapes>
        <Shape ID="13" Type="Shape"><PinX>5</PinX><PinY>3</PinY><Text>嵌套子形状-忽略</Text></Shape>
      </Shapes>
    </Shape>
  </Shapes>
  <Connects>
    <Connect FromSheet="20" FromCell="BeginX" ToSheet="10"/>
    <Connect FromSheet="20" FromCell="EndX" ToSheet="11"/>
    <Connect FromSheet="21" FromCell="BeginX" ToSheet="11"/>
    <Connect FromSheet="21" FromCell="EndX" ToSheet="12"/>
  </Connects>
</Page>`

const vsdxPage2 = `<?xml version="1.0"?>
<Page><Shapes><Shape ID="90" Type="Shape"><Text>只有一个形状的次页</Text></Shape></Shapes></Page>`

func TestImportVsdx(t *testing.T) {
	data := buildVsdx(t, map[string]string{
		"visio/masters/masters.xml": vsdxMasters,
		"visio/pages/page1.xml":     vsdxPage1,
		"visio/pages/page2.xml":     vsdxPage2,
	})
	names := []string{"FW-01"}
	addrs := map[string]string{"10.0.2.31": "WEB-1"}
	pv, err := ImportVsdx(data, names, addrs)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Stats.Pages != 2 || pv.Stats.UsedPage != "page1.xml" {
		t.Errorf("page pick = %+v", pv.Stats)
	}
	// Four nodes (nested child included — flatten keeps it; it is a labeled shape).
	if pv.Stats.Total != 4 {
		t.Fatalf("total = %d nodes = %+v", pv.Stats.Total, pv.Graph.Nodes)
	}
	byName := map[string]TopologyNode{}
	for _, n := range pv.Graph.Nodes {
		byName[n.Name] = n
	}
	if n := byName["FW-01"]; !n.Managed || n.Role != RoleFirewall {
		t.Errorf("FW-01 = %+v (master=Firewall → firewall role, inventory match)", n)
	}
	if n := byName["R1-出口"]; n.Managed || n.Role != RoleRouter {
		t.Errorf("R1 = %+v (master=Router)", n)
	}
	if n := byName["WEB-1"]; !n.Managed || n.DeviceIP != "10.0.2.31" {
		t.Errorf("WEB-1 = %+v (address fusion)", n)
	}
	if len(pv.Graph.Edges) != 2 {
		t.Errorf("edges = %+v (two connector pairs)", pv.Graph.Edges)
	}
	for _, e := range pv.Graph.Edges {
		if e.Source != "design" {
			t.Errorf("edge source = %q", e.Source)
		}
	}
	if !hasWarningContaining(pv.Warnings, "多页") {
		t.Errorf("warnings = %v", pv.Warnings)
	}
}

func TestImportVsdxRejectsLegacyVsd(t *testing.T) {
	_, err := ImportVsdx([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1, 'j', 'u', 'n', 'k'}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), ".vsd") {
		t.Fatalf("legacy .vsd must refuse with guidance, got: %v", err)
	}
}

func TestImportVsdxRobustness(t *testing.T) {
	// A zip without visio pages errors cleanly.
	data := buildVsdx(t, map[string]string{"docProps/app.xml": "<Properties/>"})
	if _, err := ImportVsdx(data, nil, nil); err == nil {
		t.Error("pageless vsdx should error")
	}
	// Truncated zip stream.
	if _, err := ImportVsdx([]byte("PK\x03\x04truncated"), nil, nil); err == nil {
		t.Error("truncated zip should error")
	}
}

func hasWarningContaining(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
