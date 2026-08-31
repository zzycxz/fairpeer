package netdev

// escalate.go — 通知升级链最小版（NETDEV_SPEC_V2 §5.2 / completion-spec §6 #8）：
// critical Finding 超时未处理（未被 resolve）→ 自动重发一次升级通知。
// 「按序重发下一出口」的最小实现：复用 5 分钟合并窗口之后的 NotifyPushText
// 全出口推送（SMTP + bot + webhook 同时收到即为「序列中的下一人」的并集），
// 每 Finding 只升级一次——二次升级（升级到主管/电话）留给 R6 分域值班表。

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// EscalationTimeout: critical finding unhandled for this long → escalate.
const EscalationTimeout = 15 * time.Minute

var (
	escalateMu      sync.Mutex
	escalatedIDs    = map[string]bool{} // finding ID → already escalated
	escalateStarted bool
)

// StartEscalationWatcher launches the escalation sweep goroutine once per
// process. Sweep cadence is half the timeout so the worst-case detection lag
// is timeout + half — acceptable for the minimal version.
func StartEscalationWatcher() {
	escalateMu.Lock()
	if escalateStarted {
		escalateMu.Unlock()
		return
	}
	escalateStarted = true
	escalateMu.Unlock()
	go func() {
		t := time.NewTicker(EscalationTimeout / 2)
		defer t.Stop()
		for range t.C {
			sweepEscalations()
		}
	}()
}

// sweepEscalations escalates one round: every ACTIVE critical finding older
// than the timeout that has not been escalated yet. Pure enough to test
// directly (clock injected).
func sweepEscalations() {
	sweepEscalationsAt(time.Now())
}

func sweepEscalationsAt(now time.Time) {
	fs, err := ListFindings()
	if err != nil {
		return
	}
	for _, f := range fs {
		if f.Severity != SeverityCritical || f.Status != FindingActive {
			continue
		}
		if now.Sub(f.CreatedAt) < EscalationTimeout {
			continue
		}
		escalateMu.Lock()
		if escalatedIDs[f.ID] {
			escalateMu.Unlock()
			continue
		}
		escalatedIDs[f.ID] = true
		escalateMu.Unlock()

		age := now.Sub(f.CreatedAt).Round(time.Minute)
		NotifyPushText("escalation",
			fmt.Sprintf("⏰ 升级：%s 仍未处理（%s）", f.Title, age),
			fmt.Sprintf("critical Finding %s 已超过 %s 未 resolve，按升级链重发。\n建议：%s\n（每条 Finding 只升级一次；处理后在「发现」页签 resolve。）",
				f.ID, EscalationTimeout, f.Suggestion))
		_ = AppendAudit(Audit{Device: "(escalation)", Command: "escalate " + f.ID, Class: "read", Status: AuditFailure, Error: "critical unhandled: " + age.String()})
		slog.Info("netdev: escalated unhandled critical finding", "id", f.ID, "age", age.String())
	}
}

// ResetEscalationStateForTest clears the once-map (tests only).
func ResetEscalationStateForTest() {
	escalateMu.Lock()
	defer escalateMu.Unlock()
	escalatedIDs = map[string]bool{}
}

