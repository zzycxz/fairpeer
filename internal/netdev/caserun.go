package netdev

// caserun.go — 轮次案例开卷（BLUETEAM_SKILL_SPEC §6 批 1.5 / §8④）。seccheck
// 委托的宿主侧接线：boot.go 的 skillRunner 在委派 netdev-seccheck-auto 前后调
// BeginCaseRun/Finish，子代理工具面零新增（案例是本地工作台产物，不是设备写）。
// 每轮自动开/续当日同入口的 open 案例，轮末把 新增/仍在/已恢复 diff 与答复摘要
// 钉进时间线——轮与轮之间可对照，不再从头对表。
//
// 并发口径：caseMu 罩住本包的 List→Save 读改写窗口（防单进程内交错）。跨进程
// （桌面 + fairpeer run 共享 state dir）仍非原子——文件锁超出本批范围，注释为
// 已知不支持项。

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

var caseRunMu sync.Mutex

// CaseRunScope is one seccheck delegation's case bookkeeping. nil-safe: all
// methods tolerate a nil receiver so the runner never branches on it.
type CaseRunScope struct {
	caseID     string
	startedAt  time.Time
	entry      string
	before     map[string]string // finding id → status at run start
	snapFailed bool              // 快照失败：Finish 跳过 diff 只钉摘要（否则存量全被虚记成「新增」）
}

// BeginCaseRun opens (or continues) today's case for one 入口形态 and
// snapshots the findings store for the end-of-run diff. Best-effort: any
// failure yields a scope without a caseID and the delegation proceeds
// unhindered — the case is a convenience, never a gate.
func BeginCaseRun(task string) *CaseRunScope {
	entry := caseRunEntry(task)
	s := &CaseRunScope{startedAt: time.Now(), entry: entry, before: map[string]string{}}
	if fs, err := ListFindings(); err == nil {
		for _, f := range fs {
			s.before[f.ID] = f.Status
		}
	} else {
		s.snapFailed = true
	}
	caseRunMu.Lock()
	defer caseRunMu.Unlock()
	day := s.startedAt.Format("2006-01-02")
	// 续卷：当天同入口的 open 案例接着用（一日多轮同一本账）。
	prefix := "蓝队核查 " + day
	if cases, err := ListCases(); err == nil {
		for _, c := range cases {
			if c.Status == "open" && strings.HasPrefix(c.Title, prefix) && strings.Contains(c.Title, "入口="+entry) {
				s.caseID = c.ID
				break
			}
		}
	}
	if s.caseID == "" {
		c := &IncidentCase{Title: prefix + "（入口=" + entry + "）", Status: "open",
			Entries: []CaseEntry{{Time: s.startedAt, Kind: "note", Text: "轮次开卷：入口=" + entry}}}
		if err := SaveCase(c); err == nil {
			s.caseID = c.ID
		}
	}
	return s
}

// Finish pins the round's finding diff and the answer digest into the case
// timeline. 新增=本轮新立案；仍在=start 时 active、end 仍非 resolved；已恢复=
// 期间转为 resolved。清掉的 finding（人工删除）计入仍在——账面保守。
// 快照失败时跳过 diff 只钉摘要——虚账比没账更糟。
func (s *CaseRunScope) Finish(answer string) {
	if s == nil || s.caseID == "" {
		return
	}
	text := fmt.Sprintf("轮次收尾（%d 分钟）", int(time.Since(s.startedAt).Minutes()))
	if s.snapFailed {
		text += "：快照失败，diff 不可比"
	} else {
		after := map[string]string{}
		if fs, err := ListFindings(); err == nil {
			for _, f := range fs {
				after[f.ID] = f.Status
			}
		}
		added, kept, resolved := 0, 0, 0
		for id, st := range s.before {
			now, ok := after[id]
			switch {
			case !ok:
				kept++
			case st != "resolved" && now == "resolved":
				resolved++
			default:
				kept++
			}
		}
		for id := range after {
			if _, ok := s.before[id]; !ok {
				added++
			}
		}
		text += fmt.Sprintf("：新增 %d / 仍在 %d / 已恢复 %d", added, kept, resolved)
	}
	digest := strings.TrimSpace(answer)
	if runes := []rune(digest); len(runes) > 80 { // 按 rune 截断——字节切片会把多字节汉字切碎成 U+FFFD
		digest = string(runes[:80]) + "…"
	}
	if digest != "" {
		text += "——" + digest
	}
	caseRunMu.Lock()
	defer caseRunMu.Unlock()
	// 原子追加：append 的读改写全程持 casesMu（cases.go），整对象 SaveCase
	// 无法在窗口内插队丢条目；案例已被删时 AppendCaseEntry 返回 false，
	// slog 注明排障。
	if !AppendCaseEntry(s.caseID, CaseEntry{Time: time.Now(), Kind: "triage", Text: text}) {
		slog.Warn("caserun: case vanished before Finish", "caseID", s.caseID)
	}
}

// caseRunEntry sniffs the delegation's 入口形态 from the task text — the
// EARLIEST marker in the text wins (a task that says 网段 first then 清单 is
// a 网段 delegation that ends in the inventory loop).
func caseRunEntry(task string) string {
	best, bestIdx := "未标注", len(task)
	for _, e := range []string{"清单", "套餐", "主机", "网段"} {
		if i := strings.Index(task, "入口="+e); i >= 0 && i < bestIdx {
			best, bestIdx = e, i
		}
	}
	return best
}
