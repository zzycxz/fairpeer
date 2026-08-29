package netdev

// alertqueue.go — 告警队列完整版（NETDEV_SPEC_V2 §4.10 R5）：状态机
// active → ack → resolved/false-positive；误报学习——false-positive 标记会把
// 该 Finding 的 Source 键登记进抑制表，syslog/trap 的自动升级在再次触发时
// 降级为 info 并附「此前被标记误报 N 次」（附录 B-14：人工调整记忆化）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Finding lifecycle states (beyond "" for human/AI findings).
const (
	FindingActive   = "active"
	FindingAck      = "ack"
	FindingResolved = "resolved"
	FindingFalsePos = "false-positive"
)

var (
	suppressMu   sync.Mutex
	suppressPath string
)

// suppressionTable maps Source key → false-positive count.
type suppressionTable map[string]int

func suppressionFile() string {
	if suppressPath == "" {
		suppressPath = filepath.Join(netdevStateDir(), "suppressed.json")
	}
	return suppressPath
}

func loadSuppressions() suppressionTable {
	raw, err := os.ReadFile(suppressionFile())
	if err != nil {
		return suppressionTable{}
	}
	var t suppressionTable
	if json.Unmarshal(raw, &t) != nil {
		return suppressionTable{}
	}
	return t
}

func saveSuppressions(t suppressionTable) {
	_ = os.MkdirAll(netdevStateDir(), 0o700)
	body, _ := json.Marshal(t)
	_ = os.WriteFile(suppressionFile(), body, 0o600)
}

// suppressCount returns the recorded false-positive count for a Source key.
func suppressCount(source string) int {
	suppressMu.Lock()
	defer suppressMu.Unlock()
	return loadSuppressions()[source]
}

// suppressIncr bumps the false-positive count for a Source key.
func suppressIncr(source string) {
	if source == "" {
		return
	}
	suppressMu.Lock()
	defer suppressMu.Unlock()
	t := loadSuppressions()
	t[source]++
	saveSuppressions(t)
}

// suppressedSeverity degrades an auto-finding per §4.10 误报学习.
func suppressedSeverity(source, sev string) (string, bool) {
	if source == "" {
		return sev, false
	}
	if n := suppressCount(source); n >= 2 {
		return SeverityInfo, true
	}
	return sev, false
}

// AckFindingByID marks one finding acknowledged (seen, being worked).
func AckFindingByID(id string) error {
	return transitionFinding(id, FindingAck, false)
}

// FalsePositiveFindingByID marks one finding a false positive AND learns the
// Source key into the suppression table.
func FalsePositiveFindingByID(id string) error {
	return transitionFinding(id, FindingFalsePos, true)
}

// ResolveFindingByID is the existing resolve (kept in alert.go); the state
// machine helper below covers ack/false-positive with optional learning.
func transitionFinding(id, to string, learn bool) error {
	fs, err := ListFindings()
	if err != nil {
		return err
	}
	for _, f := range fs {
		if f.ID != id {
			continue
		}
		if f.Status == to {
			return nil
		}
		f.Status = to
		if to == FindingResolved {
			now := time.Now()
			f.ResolvedAt = &now
		}
		if learn && f.Source != "" {
			suppressIncr(f.Source)
			f.Detail += "\n[误报学习] 已登记抑制键 " + f.Source + "；同类自动告警将降级。"
		}
		return SaveFinding(f)
	}
	return nil
}

// AggregatedFinding is the queue's collapsed view: one row per 根因键
// (Source；人工/AI Finding 用 Title), with the member findings hidden inside.
type AggregatedFinding struct {
	Key        string     `json:"key"` // Source or title
	Count      int        `json:"count"`
	Open       int        `json:"open"`     // active+ack
	Severity   string     `json:"severity"` // highest among members
	Devices    []string   `json:"devices"`
	Title      string     `json:"title"` // representative title
	Newest     time.Time  `json:"newest"`
	Members    []*Finding `json:"members,omitempty"` // newest first, capped
	Suppressed int        `json:"suppressed"`        // ≥2 = 此键已被误报学习降级
}

const aggregateMembersCap = 20

// AggregateFindings collapses the finding list by 根因键 (§4.10 同类聚合).
func AggregateFindings() []AggregatedFinding {
	fs, err := ListFindings()
	if err != nil {
		return nil
	}
	order := []string{}
	byKey := map[string]*AggregatedFinding{}
	for _, f := range fs {
		key := f.Source
		if key == "" {
			key = "title:" + f.Title
		}
		agg, ok := byKey[key]
		if !ok {
			agg = &AggregatedFinding{Key: key, Title: f.Title, Severity: f.Severity, Newest: f.CreatedAt}
			byKey[key] = agg
			order = append(order, key)
		}
		agg.Count++
		if f.Status == FindingActive || f.Status == FindingAck {
			agg.Open++
		}
		if sevRank[f.Severity] > sevRank[agg.Severity] {
			agg.Severity = f.Severity
			agg.Title = f.Title
		}
		if f.CreatedAt.After(agg.Newest) {
			agg.Newest = f.CreatedAt
		}
		for _, d := range f.Devices {
			if !containsStr(agg.Devices, d) {
				agg.Devices = append(agg.Devices, d)
			}
		}
		if len(agg.Members) < aggregateMembersCap {
			agg.Members = append(agg.Members, f)
		}
	}
	out := make([]AggregatedFinding, 0, len(order))
	for _, k := range order {
		a := byKey[k]
		if strings.HasPrefix(a.Key, "syslog:") || strings.HasPrefix(a.Key, "trap:") {
			a.Suppressed = suppressCount(a.Key)
		}
		out = append(out, *a)
	}
	return out
}
