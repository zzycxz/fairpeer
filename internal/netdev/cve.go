package netdev

// cve.go — 清单 × CVE 匹配（NETDEV_SPEC_V2 §4.5）：feed 用户自备（附录 B-4
// 原则：产品不分发 feed），本地缓存 → 逐台匹配 vendor+os/model → 命中生成
// Finding。导入器同时收简化 NVD 风格 JSON 与 NVD 原生导出（legacy 1.1
// CVE_Items / API 2.0 vulnerabilities），后者转换为简化格式后缓存。
// 暴露面视图随 v1.1。

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
	ID       string   `json:"id"` // CVE-2024-xxxx
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
// Accepts the simplified schema or a raw NVD JSON export (both converted to
// the simplified cache format). Returns the imported count.
func ImportCVEFeed(raw string) (int, error) {
	entries, err := parseCVEFeed(raw)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range entries {
		if strings.TrimSpace(c.ID) != "" && len(c.Products) > 0 {
			n++
		}
	}
	if n == 0 {
		return 0, fmt.Errorf("feed has no valid entries (need id + at least one product; NVD entries need CPE configurations)")
	}
	if err := os.MkdirAll(netdevStateDir(), 0o700); err != nil {
		return 0, err
	}
	body, _ := json.Marshal(cveFeed{CVEs: entries})
	return n, os.WriteFile(cveFile(), body, 0o600)
}

// parseCVEFeed sniffs the schema: {"cves":[...]} (simplified/cache format),
// {"CVE_Items":[...]} (NVD legacy 1.1 feed export) or {"vulnerabilities":[...]}
// (NVD API 2.0 response export).
func parseCVEFeed(raw string) ([]CVEEntry, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return nil, fmt.Errorf("feed parse: %v (expected {\"cves\":[{\"id\",\"desc\",\"products\":[],\"severity\"}]} or an NVD JSON export)", err)
	}
	switch {
	case probe["cves"] != nil:
		var f cveFeed
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			return nil, fmt.Errorf("feed parse: %v", err)
		}
		return f.CVEs, nil
	case probe["CVE_Items"] != nil:
		var f nvd11Feed
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			return nil, fmt.Errorf("NVD 1.1 parse: %v", err)
		}
		return f.convert(), nil
	case probe["vulnerabilities"] != nil:
		var f nvd20Feed
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			return nil, fmt.Errorf("NVD 2.0 parse: %v", err)
		}
		return f.convert(), nil
	}
	return nil, fmt.Errorf("unrecognized feed: expected {\"cves\":[...]}, {\"CVE_Items\":[...]} (NVD 1.1) or {\"vulnerabilities\":[...]} (NVD API 2.0)")
}

// ── NVD 原生格式（转为简化条目；无 CPE 的条目丢弃——无法匹配清单） ──────────

type nvdCPEMatch struct {
	Criteria  string `json:"criteria"` // NVD 2.0
	CPE23URI  string `json:"cpe23Uri"` // NVD 1.1
	Vulnerable *bool `json:"vulnerable"`
}

type nvdDescItem struct {
	Lang  string `json:"lang"`
	Value string `json:"value"`
}

type nvd11Feed struct {
	CVEItems []struct {
		CVE struct {
			Meta struct {
				ID string `json:"ID"`
			} `json:"CVE_data_meta"`
			Desc struct {
				Data []nvdDescItem `json:"description_data"`
			} `json:"description"`
		} `json:"cve"`
		Impact struct {
			BaseMetricV3 struct {
				CVSSV3 struct {
					BaseSeverity string `json:"baseSeverity"`
				} `json:"cvssV3"`
			} `json:"baseMetricV3"`
			BaseMetricV2 struct {
				Severity string `json:"severity"`
			} `json:"baseMetricV2"`
		} `json:"impact"`
		Configurations struct {
			Nodes []struct {
				CPEMatch []nvdCPEMatch `json:"cpe_match"`
			} `json:"nodes"`
		} `json:"configurations"`
	} `json:"CVE_Items"`
}

func (f nvd11Feed) convert() []CVEEntry {
	var out []CVEEntry
	for _, it := range f.CVEItems {
		sev := normSeverity(it.Impact.BaseMetricV3.CVSSV3.BaseSeverity)
		if sev == "" {
			sev = normSeverity(it.Impact.BaseMetricV2.Severity)
		}
		var cpes []nvdCPEMatch
		for _, nd := range it.Configurations.Nodes {
			cpes = append(cpes, nd.CPEMatch...)
		}
		if e := cveFromNVD(it.CVE.Meta.ID, nvdDesc(it.CVE.Desc.Data), sev, cpes); e != nil {
			out = append(out, *e)
		}
	}
	return out
}

type nvd20Feed struct {
	Vulns []struct {
		CVE struct {
			ID           string        `json:"id"`
			Descriptions []nvdDescItem `json:"descriptions"`
			Metrics      struct {
				CVSSMetricV31 []struct {
					CVSSData struct {
						BaseSeverity string `json:"baseSeverity"`
					} `json:"cvssData"`
				} `json:"cvssMetricV31"`
				CVSSMetricV30 []struct {
					CVSSData struct {
						BaseSeverity string `json:"baseSeverity"`
					} `json:"cvssData"`
				} `json:"cvssMetricV30"`
				CVSSMetricV2 []struct {
					CVSSData struct {
						BaseSeverity string `json:"baseSeverity"`
					} `json:"cvssData"`
				} `json:"cvssMetricV2"`
			} `json:"metrics"`
			Configurations []struct {
				Nodes []struct {
					CPEMatch []nvdCPEMatch `json:"cpe_match"`
				} `json:"nodes"`
			} `json:"configurations"`
		} `json:"cve"`
	} `json:"vulnerabilities"`
}

func (f nvd20Feed) convert() []CVEEntry {
	var out []CVEEntry
	for _, v := range f.Vulns {
		sev := ""
		if len(v.CVE.Metrics.CVSSMetricV31) > 0 {
			sev = normSeverity(v.CVE.Metrics.CVSSMetricV31[0].CVSSData.BaseSeverity)
		}
		if sev == "" && len(v.CVE.Metrics.CVSSMetricV30) > 0 {
			sev = normSeverity(v.CVE.Metrics.CVSSMetricV30[0].CVSSData.BaseSeverity)
		}
		if sev == "" && len(v.CVE.Metrics.CVSSMetricV2) > 0 {
			sev = normSeverity(v.CVE.Metrics.CVSSMetricV2[0].CVSSData.BaseSeverity)
		}
		var cpes []nvdCPEMatch
		for _, cfg := range v.CVE.Configurations {
			for _, nd := range cfg.Nodes {
				cpes = append(cpes, nd.CPEMatch...)
			}
		}
		if e := cveFromNVD(v.CVE.ID, nvdDesc(v.CVE.Descriptions), sev, cpes); e != nil {
			out = append(out, *e)
		}
	}
	return out
}

// cveFromNVD builds one simplified entry; nil when nothing matchable.
func cveFromNVD(id, desc, sev string, cpes []nvdCPEMatch) *CVEEntry {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	seen := map[string]bool{}
	var products []string
	for _, cm := range cpes {
		if cm.Vulnerable != nil && !*cm.Vulnerable {
			continue
		}
		uri := cm.Criteria
		if uri == "" {
			uri = cm.CPE23URI
		}
		for _, p := range cpeProducts(uri) {
			if !seen[p] && len(products) < 12 {
				seen[p] = true
				products = append(products, p)
			}
		}
	}
	if len(products) == 0 {
		return nil
	}
	return &CVEEntry{ID: id, Desc: truncStr(desc, 500), Products: products, Severity: sev}
}

// cpeProducts extracts match substrings from a CPE 2.3 URI
// (cpe:2.3:part:vendor:product:...): the vendor plus the product with
// underscores as spaces ("ios_xe" → "ios xe") — 匹配面是清单的
// vendor+os+model 文本，产品名单独保留下划线反而命不中。
func cpeProducts(cpe string) []string {
	f := strings.Split(cpe, ":")
	if len(f) < 5 {
		return nil
	}
	strip := func(s string) string { return strings.ReplaceAll(s, "\\", "") }
	var out []string
	add := func(s string) {
		s = strip(strings.ToLower(s))
		if len(s) < 2 || s == "-" || s == "*" {
			return
		}
		out = append(out, s)
	}
	add(f[3])
	add(strings.ReplaceAll(f[4], "_", " "))
	return out
}

func nvdDesc(items []nvdDescItem) string {
	for _, d := range items {
		if d.Lang == "en" && strings.TrimSpace(d.Value) != "" {
			return d.Value
		}
	}
	if len(items) > 0 {
		return items[0].Value
	}
	return ""
}

func normSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical", "high", "medium", "low":
		return strings.ToLower(strings.TrimSpace(s))
	}
	return ""
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
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
		return nil, fmt.Errorf("no CVE feed imported — paste one via 运维设置 or NetDevImportCVEs")
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
			Detail:  "清单与已导入 feed 无交集（注意：匹配依赖 vendor/os/model 字段完整）。",
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
		Title:      fmt.Sprintf("CVE 匹配：%d 台命中", len(devs)),
		Severity:   maxSev(matches),
		Devices:    devs,
		Detail:     summary.String(),
		Evidence:   []Evidence{{Device: "(cve-feed)", Command: "cve match", Output: fmt.Sprintf("feed %d 条 / 命中 %d", len(matches), len(matches))}},
		Suggestion: "逐条核对版本范围（匹配是 vendor+model 粗匹配，不是精确版本比对）；修复走变更。",
		Source:     "cve:sweep",
		Status:     "active",
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
