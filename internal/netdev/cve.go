package netdev

// cve.go — 清单 × CVE 匹配（NETDEV_SPEC_V2 §4.5）：feed 用户自备（附录 B-4
// 原则：产品不分发 feed），简化 NVD 风格 JSON → 本地缓存 → 逐台匹配
// vendor+os/model → 命中生成 Finding。暴露面视图随 v1.1。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CVEEntry is one simplified feed item.
type CVEEntry struct {
	ID       string   `json:"id"`       // CVE-2024-xxxx
	Desc     string   `json:"desc"`
	Products []string `json:"products"` // lowercase vendor/product substrings
	Severity string   `json:"severity"` // critical | high | medium | low
}

type cveFeed struct {
	CVEs []CVEEntry `json:"cves"`
}

func cveFile() string {
	return filepath.Join(netdevStateDir(), "cves.json")
}

// ImportCVEFeed validates and caches a user-supplied feed (JSON string).
// Returns the imported count.
func ImportCVEFeed(raw string) (int, error) {
	var f cveFeed
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return 0, fmt.Errorf("feed parse: %v (expected {\"cves\":[{\"id\",\"desc\",\"products\":[],\"severity\"}]})", err)
	}
	n := 0
	for _, c := range f.CVEs {
		if strings.TrimSpace(c.ID) != "" && len(c.Products) > 0 {
			n++
		}
	}
	if n == 0 {
		return 0, fmt.Errorf("feed has no valid entries (need id + at least one product)")
	}
	if err := os.MkdirAll(netdevStateDir(), 0o700); err != nil {
		return 0, err
	}
	body, _ := json.Marshal(f)
	return n, os.WriteFile(cveFile(), body, 0o600)
}

// CVEMatch is one device ↔ CVE hit.
type CVEMatch struct {
	Device   string `json:"device"`
	CVEID    string `json:"cve_id"`
	Desc     string `json:"desc"`
	Severity string `json:"severity"`
	Product  string `json:"product"` // matched product substring
}

// MatchCVEs runs the inventory against the cached feed.
func (m *Manager) MatchCVEs() ([]CVEMatch, error) {
	raw, err := os.ReadFile(cveFile())
	if err != nil {
		return nil, fmt.Errorf("no CVE feed imported — paste one via the 运维 settings or NetDevImportCVEs")
	}
	var f cveFeed
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	var out []CVEMatch
	for _, d := range m.cfg.NetDev.Devices {
		hay := strings.ToLower(d.Vendor + " " + d.OS + " " + d.Model)
		if hay == "  " {
			continue
		}
		for _, c := range f.CVEs {
			for _, p := range c.Products {
				p = strings.ToLower(strings.TrimSpace(p))
				if p != "" && strings.Contains(hay, p) {
					out = append(out, CVEMatch{Device: d.Name, CVEID: c.ID, Desc: c.Desc, Severity: c.Severity, Product: p})
					break
				}
			}
		}
	}
	return out, nil
}

// MatchCVEsToFindings runs the match and files each device's hits as ONE
// Finding (dedup key: "cve:" + device — re-runs update rather than pile up).
func (m *Manager) MatchCVEsToFindings() (*Finding, error) {
	matches, err := m.MatchCVEs()
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return &Finding{Title: "CVE 匹配：无命中", Severity: SeverityInfo,
			Detail: "清单与已导入 feed 无交集（注意：匹配依赖 vendor/os/model 字段完整）。",
			Devices: []string{"(all)"}, Evidence: nil, Source: "cve:sweep",
			Status: "active", CreatedAt: time.Now()}, nil
	}
	byDev := map[string][]CVEMatch{}
	for _, h := range matches {
		byDev[h.Device] = append(byDev[h.Device], h)
	}
	var summary strings.Builder
	var devs []string
	for d, hits := range byDev {
		devs = append(devs, d)
		summary.WriteString(fmt.Sprintf("%s: %d 条", d, len(hits)))
		for i, h := range hits {
			if i >= 5 {
				summary.WriteString(fmt.Sprintf(" …另有 %d", len(hits)-5))
				break
			}
			summary.WriteString(fmt.Sprintf("\n  %s [%s] %s（匹配 %q）", h.CVEID, sevToNet(h.Severity), firstLine(h.Desc), h.Product))
		}
		summary.WriteString("\n")
	}
	f := &Finding{
		Title:    fmt.Sprintf("CVE 匹配：%d 台命中", len(devs)),
		Severity: maxSev(matches),
		Devices:  devs,
		Detail:   summary.String(),
		Evidence: []Evidence{{Device: "(cve-feed)", Command: "cve match", Output: fmt.Sprintf("feed %d 条 / 命中 %d", len(matches), len(matches))}},
		Suggestion: "逐条核对版本范围（匹配是 vendor+model 粗匹配，不是精确版本比对）；修复走提案。",
		Source:   "cve:sweep",
		Status:   "active",
	}
	f.CreatedAt = time.Now()
	if err := SaveFinding(f); err != nil {
		return nil, err
	}
	return f, nil
}

func sevToNet(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return SeverityCritical
	case "high", "medium":
		return SeverityWarning
	default:
		return SeverityInfo
	}
}

func maxSev(ms []CVEMatch) string {
	rank := map[string]int{SeverityInfo: 0, SeverityWarning: 1, SeverityCritical: 2}
	out := SeverityInfo
	for _, m := range ms {
		s := sevToNet(m.Severity)
		if rank[s] > rank[out] {
			out = s
		}
	}
	return out
}
