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
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/fileutil"
)

// CVEEntry is one simplified feed item.
type CVEEntry struct {
	ID       string   `json:"id"` // CVE-2024-xxxx
	Desc     string   `json:"desc"`
	Products []string `json:"products"` // lowercase vendor/product substrings
	Severity string   `json:"severity"` // critical | high | medium | low
	// Versions 批 B：NVD CPE 的版本区间（可多段——不同 CPE match 各带边界）。
	// 空 = feed 未提供边界，匹配只做产品粗匹配（现状行为）。
	Versions []CPEVersionRange `json:"versions,omitempty"`
	// Remediation（S2-2）：feed 自备的修复出处，用户自备 feed 可携带。
	Remediation *CVERemediation `json:"remediation,omitempty"`
}

// CPEVersionRange is one CPE match's version window (any field may be empty
// = unbounded on that side).
type CPEVersionRange struct {
	StartIncl string `json:"start_incl,omitempty"`
	StartExcl string `json:"start_excl,omitempty"`
	EndIncl   string `json:"end_incl,omitempty"`
	EndExcl   string `json:"end_excl,omitempty"`
}

// CVERemediation is the feed-supplied fix reference for one CVE (S2-1/2-2).
type CVERemediation struct {
	UpgradeTo string `json:"upgrade_to,omitempty"`
	KB        string `json:"kb,omitempty"`
	RefURL    string `json:"ref_url,omitempty"`
}

type cveFeed struct {
	CVEs []CVEEntry `json:"cves"`
}

func cveFile() string {
	return filepath.Join(netdevStateDir(), "cves.json")
}

// cveCacheCount reports how many entries the merged cache holds (evidence
// honesty: the sweep's "feed N 条" is the cache size, not the hit count).
func cveCacheCount() int {
	b, err := os.ReadFile(cveFile())
	if err != nil {
		return 0
	}
	var f cveFeed
	if json.Unmarshal(b, &f) != nil {
		return 0
	}
	return len(f.CVEs)
}

// ImportCVEFeed validates and caches a user-supplied feed (JSON string).
// Accepts the simplified schema or a raw NVD JSON export (both converted to
// the simplified cache format). Returns the imported count.
//
// Merge semantics (2026-09-08): NVD API 2.0 exports are usually time-windowed
// deltas — a wholesale replace would shrink the cache to the last window. So
// imports merge by CVE-ID: same-ID entries from the new feed win, all other
// existing entries survive. ClearCVEFeed is the only removal path.
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
	merged, idx := make([]CVEEntry, 0, len(entries)), map[string]int{}
	if b, err := os.ReadFile(cveFile()); err == nil {
		var old cveFeed
		if json.Unmarshal(b, &old) == nil {
			for _, e := range old.CVEs {
				if strings.TrimSpace(e.ID) == "" {
					continue
				}
				idx[e.ID] = len(merged)
				merged = append(merged, e)
			}
		}
	}
	for _, e := range entries {
		// 与 n 的计数口径一致：无 ID 或无产品的条目不进缓存——否则一条空
		// products 的同 ID 条目会把既有 CVE 的匹配面整条清空。
		if strings.TrimSpace(e.ID) == "" || len(e.Products) == 0 {
			continue
		}
		if i, ok := idx[e.ID]; ok {
			merged[i] = e
		} else {
			idx[e.ID] = len(merged)
			merged = append(merged, e)
		}
	}
	body, _ := json.Marshal(cveFeed{CVEs: merged})
	return n, fileutil.AtomicWriteFile(cveFile(), body, 0o600)
}

// ClearCVEFeed removes the cached feed entirely — merge imports never delete
// entries, so this is the reset. Absent file is success (idempotent).
func ClearCVEFeed() error {
	if err := os.Remove(cveFile()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ResolveCVESweep resolves the rolling cve:sweep finding — the feed being
// cleared must not leave a stale active hit card behind (the zero-hit sweep
// path resolves internally; the clear bridge funnels here).
func (m *Manager) ResolveCVESweep() {
	m.resolveFindingBySource("cve:sweep")
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
	Criteria   string `json:"criteria"` // NVD 2.0
	CPE23URI   string `json:"cpe23Uri"` // NVD 1.1
	Vulnerable *bool  `json:"vulnerable"`
	// 版本边界（1.1 与 2.0 同名同义）。
	VersionStartIncluding string `json:"versionStartIncluding"`
	VersionStartExcluding string `json:"versionStartExcluding"`
	VersionEndIncluding   string `json:"versionEndIncluding"`
	VersionEndExcluding   string `json:"versionEndExcluding"`
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
	seenRange := map[CPEVersionRange]bool{}
	var products []string
	var ranges []CPEVersionRange
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
		if r := (CPEVersionRange{StartIncl: cm.VersionStartIncluding, StartExcl: cm.VersionStartExcluding, EndIncl: cm.VersionEndIncluding, EndExcl: cm.VersionEndExcluding}); r != (CPEVersionRange{}) && !seenRange[r] {
			seenRange[r] = true
			ranges = append(ranges, r)
		}
	}
	if len(products) == 0 {
		return nil
	}
	return &CVEEntry{ID: id, Desc: truncStr(desc, 500), Products: products, Severity: sev, Versions: ranges}
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
	// vendor 与 product 同样下划线转空格——多词厂商（palo_alto）在清单/
	// 示例 feed 里写作空格形式，保留下划线永远命不中。
	add(strings.ReplaceAll(f[3], "_", " "))
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

// truncStr caps s at n RUNES (byte slicing would cut multi-byte characters
// into invalid UTF-8 — NVD descriptions are frequently non-ASCII).
func truncStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ── 批 B：CPE 版本区间判定 ────────────────────────────────────────────────
// 判定失败（设备无版本、feed 无边界）一律标"需人工比对"——宁多报不漏报；
// 区间外命中也保留在列表里（标"区间外，供核对"），绝静默丢弃。

// versionTokens extracts maximal digit runs as an int sequence:
// "15.2(4)M" → [15 2 4], "V200R005C00" → [200 5 0], "" → nil.
// Runs saturate at 1e15 — a serial-number-length digit run must never
// overflow into a negative token (which would flip interval comparisons).
func versionTokens(s string) []int {
	var out []int
	cur := 0
	has := false
	flush := func() {
		if has {
			out = append(out, cur)
		}
		cur, has = 0, false
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			if cur <= 1000000000000000 { // saturate ≈1e15
				cur = cur*10 + int(r-'0')
			}
			has = true
		} else {
			flush()
		}
	}
	flush()
	return out
}

// cmpTokens compares two padded int sequences: -1/0/1. Trailing zero padding
// means 15.2 == 15.2.0.
func cmpTokens(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

// versionInRange reports whether v satisfies every present bound of r.
// A version v without any digits is undecidable → false; a bound without
// digits is treated as unbounded on that side (CPE 的 "-" 通写法).
func versionInRange(v string, r CPEVersionRange) bool {
	vt := versionTokens(v)
	if len(vt) == 0 {
		return false
	}
	// check compares v against one bound; a bound without digits is UNBOUNDED
	// (CPE 常见 "-" 通配) — only a digit-less VERSION is undecidable (false).
	check := func(bound string, lt func(c int) bool) bool {
		bt := versionTokens(bound)
		if len(bt) == 0 {
			return true // unbounded side
		}
		return lt(cmpTokens(vt, bt))
	}
	if !check(r.StartIncl, func(c int) bool { return c >= 0 }) {
		return false
	}
	if !check(r.StartExcl, func(c int) bool { return c > 0 }) {
		return false
	}
	if !check(r.EndIncl, func(c int) bool { return c <= 0 }) {
		return false
	}
	if !check(r.EndExcl, func(c int) bool { return c < 0 }) {
		return false
	}
	return true
}

// deviceVersionFromModel pulls a version-looking token out of the model
// fingerprint summary ("IOS 15.2(4)M" → "15.2(4)"). Requires a dotted number
// — bare hardware numbers ("S5735") are NOT versions and stay unverified.
// Leftmost match wins; precision is major.minor(+maintenance) — sub-versions
// beyond that need the human-compare path.
func deviceVersionFromModel(model string) string {
	m := regexp.MustCompile(`[0-9]+(?:\.[0-9]+)+(?:\(\d+\))?`).FindString(model)
	return strings.TrimSpace(m)
}

// Version statuses for one device × CVE hit.
const (
	VersionInRange    = "in_range"
	VersionOutOfRange = "out_of_range"
	VersionUnverified = "unverified"
)

// judgeVersion classifies one hit against the entry's CPE ranges.
func judgeVersion(model string, entry CVEEntry) (status, ver string) {
	ver = deviceVersionFromModel(model)
	if len(entry.Versions) == 0 || ver == "" {
		return VersionUnverified, ver
	}
	for _, r := range entry.Versions {
		if versionInRange(ver, r) {
			return VersionInRange, ver
		}
	}
	return VersionOutOfRange, ver
}

// cveSweepFixHint picks the first feed-supplied remediation among matches
// (S2-1)：feed 出处 → verified；全无则 nil（对话研判兜底，模型建议标 model）。
// remediation 是 CVE 级而非命中级——区间外/未核实版本的命中同样可能贡献它。
func cveSweepFixHint(matches []CVEMatch) *FixHint {
	for _, m := range matches {
		if r := m.Remediation; r != nil {
			fx := &FixHint{Type: "upgrade", Confidence: "verified", Link: r.RefURL}
			switch {
			case r.UpgradeTo != "":
				fx.Ref = "升级至 " + r.UpgradeTo
			case r.KB != "":
				fx.Type = "patch"
				fx.Ref = "安装补丁 " + r.KB
			default:
				fx.Ref = "见厂商公告"
			}
			return fx
		}
	}
	return nil
}

// CVEMatch is one device ↔ CVE hit.
type CVEMatch struct {
	Device   string `json:"device"`
	CVEID    string `json:"cve_id"`
	Desc     string `json:"desc"`
	Severity string `json:"severity"`
	Product  string `json:"product"` // matched product substring
	// VersionStatus 批 B：in_range（版本落在 CPE 区间内）/ out_of_range
	//（区间外——保留供核对，不静默丢弃）/ unverified（无版本或 feed 无边界）。
	VersionStatus string `json:"version_status,omitempty"`
	DeviceVersion string `json:"device_version,omitempty"`
	// Remediation 透传 feed 的修复出处（S2-1）；无则空。
	Remediation *CVERemediation `json:"remediation,omitempty"`
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
		if strings.TrimSpace(hay) == "" {
			continue
		}
		for _, c := range f.CVEs {
			for _, p := range c.Products {
				p = strings.ToLower(strings.TrimSpace(p))
				if p != "" && strings.Contains(hay, p) {
					st, ver := judgeVersion(d.Model, c)
					out = append(out, CVEMatch{Device: d.Name, CVEID: c.ID, Desc: c.Desc, Severity: c.Severity, Product: p, Remediation: c.Remediation, VersionStatus: st, DeviceVersion: ver})
					break
				}
			}
		}
	}
	return out, nil
}

// cveSweepMu serializes the sweep's read-modify-write over the rolling card:
// 并发双扫查（手动按钮 × 调度审计）在无存量卡时会双开两张 cve:sweep 卡、
// 零命中回收与命中落卡交错会把新鲜命中压成 resolved——互斥后两窗全闭。
var cveSweepMu sync.Mutex

// MatchCVEsToFindings runs the match and files each device's hits as ONE
// Finding (dedup key: "cve:" + device — re-runs update rather than pile up).
func (m *Manager) MatchCVEsToFindings() (*Finding, error) {
	cveSweepMu.Lock()
	defer cveSweepMu.Unlock()
	matches, err := m.MatchCVEs()
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		// 零命中回收（2026-09-08 收口）：此前直接 return，旧命中卡永远停在
		// active——设备修好/移除后再扫零命中，或 feed 被清空，都走这里闭环，
		// 与告警"条件解除自动恢复"同一语义。
		m.resolveFindingBySource("cve:sweep")
		return &Finding{Title: "CVE 匹配：无命中", Severity: SeverityInfo,
			Detail:  "清单与已导入 feed 无交集（注意：匹配依赖 vendor/os/model 字段完整）。",
			Devices: []string{"(all)"}, Evidence: nil, Source: "cve:sweep",
			Status: "active", CreatedAt: time.Now()}, nil
	}
	byDev := map[string][]CVEMatch{}
	for _, h := range matches {
		byDev[h.Device] = append(byDev[h.Device], h)
	}
	// 三态计数走全量 matches——与每设备 5 条的展示截断无关（截断曾经把
	// 未展示的命中一并跳过计数，导致「命中 10 / 区间内 6」自相矛盾）。
	inRange, outRange, unverified := 0, 0, 0
	for _, h := range matches {
		switch h.VersionStatus {
		case VersionInRange:
			inRange++
		case VersionOutOfRange:
			outRange++
		default:
			unverified++
		}
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
			mark := "需人工比对"
			switch h.VersionStatus {
			case VersionInRange:
				mark = "版本区间内"
			case VersionOutOfRange:
				mark = "版本区间外"
			}
			ver := ""
			if h.DeviceVersion != "" {
				ver = "，设备版本 " + h.DeviceVersion
			}
			summary.WriteString(fmt.Sprintf("\n  %s [%s] %s（匹配 %q；%s%s）", h.CVEID, sevToNet(h.Severity), firstLine(h.Desc), h.Product, mark, ver))
		}
		summary.WriteString("\n")
	}
	f := &Finding{
		Title:      fmt.Sprintf("CVE 匹配：%d 台命中", len(devs)),
		Severity:   maxSev(matches),
		Devices:    devs,
		Detail:     summary.String(),
		Evidence:   []Evidence{{Device: "(cve-feed)", Command: "cve match", Output: fmt.Sprintf("feed %d 条 / 命中 %d（区间内 %d / 区间外 %d / 需人工比对 %d）", cveCacheCount(), len(matches), inRange, outRange, unverified)}},
		Suggestion: fmt.Sprintf("区间内 %d 条按 feed 边界已核实版本；区间外 %d 条保留供核对（修复前重查）；需人工比对 %d 条——补齐设备指纹版本后重扫。修复走变更。", inRange, outRange, unverified),
		Fix:        cveSweepFixHint(matches),
		Source:     "cve:sweep",
		Status:     "active",
	}
	f.CreatedAt = time.Now()
	// 滚动落卡（SaveRollingFinding）：同 Source（cve:sweep）原地更新而非每次
	// 新开一张——重复扫查/转正自动匹配不会把发现中心堆满重复的「CVE 匹配」卡。
	if err := SaveRollingFinding(f); err != nil {
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
