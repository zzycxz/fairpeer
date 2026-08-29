package netdev

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// scanimport.go — bring EXTERNAL scan results into the evidence economy:
// an nmap XML run (produced by the user's own tooling — we never scan beyond
// the configured scopes ourselves from here) parses into a Finding of
// discovered hosts/ports, cross-checked against the inventory so unknown
// hosts stand out as 待确认 (unmanaged). Doctrine unchanged: importing is a
// human action, findings carry evidence, nothing auto-connects.

type nmapRun struct {
	Hosts []nmapHost `xml:"host"`
}

type nmapHost struct {
	Addresses []struct {
		Addr string `xml:"addr,attr"`
		Type string `xml:"addrtype,attr"`
	} `xml:"address"`
	Hostnames []struct {
		Name string `xml:"name,attr"`
	} `xml:"hostnames>hostname"`
	Ports []struct {
		Proto  string `xml:"protocol,attr"`
		PortID string `xml:"portid,attr"`
		State  struct {
			State string `xml:"state,attr"`
		} `xml:"state"`
		Service struct {
			Name string `xml:"name,attr"`
		} `xml:"service"`
	} `xml:"ports>port"`
}

// ImportNmapXML parses an nmap -oX dump and files one Finding: every live
// host with its open ports; hosts missing from the inventory are flagged
// 待确认 (they may be joinable — the user decides, nothing dials).
func ImportNmapXML(xmlText string, cfg inventory) (*Finding, error) {
	var run nmapRun
	if err := xml.Unmarshal([]byte(xmlText), &run); err != nil {
		return nil, fmt.Errorf("nmap xml: %w", err)
	}
	known := map[string]bool{}
	for _, d := range cfg.devices() {
		known[d] = true
	}
	var ev []Evidence
	unknown, total := 0, 0
	for _, h := range run.Hosts {
		ip := ""
		name := ""
		for _, a := range h.Addresses {
			if a.Type == "ipv4" || a.Type == "ipv6" {
				ip = a.Addr
			}
		}
		if len(h.Hostnames) > 0 {
			name = h.Hostnames[0].Name
		}
		if ip == "" {
			continue
		}
		total++
		tag := "已纳管"
		if !known[ip] {
			tag = "待确认（不在设备清单）"
			unknown++
		}
		var openPorts []string
		for _, p := range h.Ports {
			if p.State.State == "open" {
				svc := p.Service.Name
				if svc == "" {
					svc = p.Proto
				}
				openPorts = append(openPorts, fmt.Sprintf("%s/%s(%s)", p.PortID, p.Proto, svc))
			}
		}
		line := fmt.Sprintf("%s %s [%s] 开放端口: %s", ip, name, tag, strings.Join(openPorts, ", "))
		if len(openPorts) == 0 {
			line = fmt.Sprintf("%s %s [%s] 无开放端口（存活）", ip, name, tag)
		}
		ev = append(ev, Evidence{Device: "nmap-import", Command: "nmap -oX (user-run)", Output: line})
	}
	if total == 0 {
		return nil, fmt.Errorf("nmap xml: no hosts with addresses found")
	}
	f := &Finding{
		Title:    fmt.Sprintf("扫描导入：%d 台主机，其中 %d 台待确认（不在清单）", total, unknown),
		Severity: severityForUnknown(unknown),
		Detail:   "来自用户 nmap 导出的发现；待确认主机不自动连接——录入设备清单后才可诊断。",
		Evidence: ev,
	}
	return f, nil
}

func severityForUnknown(n int) string {
	if n > 0 {
		return "warning"
	}
	return "info"
}

// inventory is the tiny seam cfg satisfies (test isolation).
type inventory interface {
	devices() []string
}

type cfgInventory struct{ list []string }

func (c cfgInventory) devices() []string { return c.list }

// ImportNmapForConfig adapts ImportNmapXML to the config type (bridge seam).
func ImportNmapForConfig(xmlText string, deviceAddrs []string) (*Finding, error) {
	return ImportNmapXML(xmlText, cfgInventory{list: deviceAddrs})
}
