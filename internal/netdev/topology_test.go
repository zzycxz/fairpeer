package netdev

import (
	"testing"
)

const huaweiLLDPFixture = `
GigabitEthernet0/0/1 has 1 neighbor(s):
Neighbor index : 1
Chassis ID type : MAC address
Chassis ID : 5cb9-01ed-2f42
Port ID type : Interface name
Port ID : GE0/0/24
Hold-time : 104s
Port description : uplink-to-core
System name : ACCESS-SW-2
System description : S5735-L48P-EI Huawei Versatile Routing Platform
Management address : 10.30.2.12
Expired : 0

GigabitEthernet0/0/24 has 1 neighbor(s):
Neighbor index : 1
Chassis ID type : MAC address
Chassis ID : 4c1f-cc72-9a01
Port ID type : Interface name
Port ID : Gig0/1
Hold-time : 98s
System name : CORE-FW-1
System description : USG6320 Huawei Versatile Routing Platform
Management address : 10.30.2.1

GigabitEthernet0/0/2 has 1 neighbor(s):
Neighbor index : 1
Chassis ID type : MAC address
Chassis ID : 5cb9-01ee-1111
Port ID : 70:b3:d5:de:11:22
Hold-time : 88s
Port ID type : MAC address
System name : UNMANAGED-AP
`

func TestParseHuaweiLLDP(t *testing.T) {
	edges := parseHuaweiLLDP(huaweiLLDPFixture)
	if len(edges) != 3 {
		t.Fatalf("edges = %d, want 3 (got %+v)", len(edges), edges)
	}
	e := edges[0]
	if e.LocalPort != "GigabitEthernet0/0/1" ||
		e.RemoteDevice != "ACCESS-SW-2" ||
		e.RemotePort != "GigabitEthernet0/0/24" || // GE → GigabitEthernet
		e.RemoteIP != "10.30.2.12" ||
		e.Source != "lldp" {
		t.Fatalf("edge[0] = %+v", e)
	}
	if edges[1].RemoteDevice != "CORE-FW-1" || edges[1].RemotePort != "GigabitEthernet0/1" {
		t.Fatalf("edge[1] = %+v", edges[1]) // Gig0/1 → GigabitEthernet0/1
	}
	// F2: the system description rides the edge as Platform.
	if edges[0].Platform != "S5735-L48P-EI Huawei Versatile Routing Platform" {
		t.Fatalf("edge[0].Platform = %q", edges[0].Platform)
	}
	if edges[1].Platform != "USG6320 Huawei Versatile Routing Platform" {
		t.Fatalf("edge[1].Platform = %q", edges[1].Platform)
	}
	if edges[2].RemotePort == "" || edges[2].RemotePort == "GigabitEthernet" {
		t.Fatalf("MAC-typed port id should pass through unchanged: %+v", edges[2])
	}
}

const ciscoCDPFixture = `
-------------------------
Device ID: SW2(9ABCDEF12345)
Entry address(es):
  IP address: 10.30.2.22
Platform: cisco WS-C3750G-24TS,  Capabilities: Switch IGMP
Interface: GigabitEthernet0/1,  Port ID (outgoing port): GigabitEthernet1/0/24
Holdtime : 145 sec
Version :
Cisco IOS Software, C3750 Software (C3750-IPSERVICESK9-M), Version 12.2(55)SE11

-------------------------
Device ID: ROUTER1.corp.example
Entry address(es):
  IP address: 10.30.2.254
Platform: cisco ISR4321/K9,  Capabilities: Router Source-Route-Bridge IGMP
Interface: Gig0/2,  Port ID (outgoing port): Gi0/0/1
Holdtime : 132 sec

-------------------------
Device ID: PHONE-7801
Platform: Cisco IP Phone 7801,  Capabilities: Host Phone
Interface: FastEthernet0/5,  Port ID (outgoing port): Port 1
`

func TestParseCiscoCDP(t *testing.T) {
	edges := parseCiscoCDP(ciscoCDPFixture)
	if len(edges) != 3 {
		t.Fatalf("edges = %d, want 3 (got %+v)", len(edges), edges)
	}
	e := edges[0]
	if e.RemoteDevice != "SW2" || // serial-in-parens stripped
		e.LocalPort != "GigabitEthernet0/1" ||
		e.RemotePort != "GigabitEthernet1/0/24" ||
		e.RemoteIP != "10.30.2.22" ||
		e.Source != "cdp" {
		t.Fatalf("edge[0] = %+v", e)
	}
	if edges[1].RemoteDevice != "ROUTER1.corp.example" || edges[1].LocalPort != "GigabitEthernet0/2" {
		t.Fatalf("edge[1] = %+v", edges[1])
	}
	// F2: CDP Platform line — the previously-dead capture is live now.
	if edges[0].Platform != "cisco WS-C3750G-24TS" {
		t.Fatalf("edge[0].Platform = %q", edges[0].Platform)
	}
	if edges[1].Platform != "cisco ISR4321/K9" {
		t.Fatalf("edge[1].Platform = %q", edges[1].Platform)
	}
	if edges[2].LocalPort != "FastEthernet0/5" || edges[2].RemotePort != "Port 1" {
		t.Fatalf("edge[2] = %+v (phone Port 1 passes through)", edges[2])
	}
}

func TestNormalizeIfName(t *testing.T) {
	cases := map[string]string{
		"GE0/0/24":          "GigabitEthernet0/0/24",
		"Gig0/1":            "GigabitEthernet0/1",
		"Gi1/0/1":           "GigabitEthernet1/0/1",
		"Fa0/5":             "FastEthernet0/5",
		"Te1/1/1":           "TenGigabitEthernet1/1/1",
		"Vl100":             "Vlanif100",
		"Eth-Trunk1":        "Eth-Trunk1",
		"XGig0/1/1":         "TenGigabitEthernet0/1/1",
		"LoopBack0":         "LoopBack0", // unknown prefix passes through
		"70:b3:d5:de:11:22": "70:b3:d5:de:11:22",
	}
	for in, want := range cases {
		if got := normalizeIfName(in); got != want {
			t.Errorf("normalizeIfName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Empty/garbage output must yield empty edges, never an error or bogus rows.
func TestParseNeighborsEmpty(t *testing.T) {
	for _, key := range []string{"huawei-vrp", "cisco-ios"} {
		edges, err := parseNeighbors(key, "\n  \nsome unrelated text\n")
		if err != nil || len(edges) != 0 {
			t.Fatalf("%s: edges=%v err=%v", key, edges, err)
		}
	}
	if _, err := parseNeighbors("zte-zxr10", "x"); err == nil {
		t.Fatal("unknown driver should error")
	}
}
