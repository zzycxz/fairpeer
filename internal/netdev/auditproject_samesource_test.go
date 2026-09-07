package netdev

// auditproject_samesource_test.go — §3.5-G 两侧同源红测试：风险清单只消费
// ①本轮电池刚立案的 finding（任意 source）与 ②chat 套餐立案（source=audit
// + 项目锚点）；无关噪音（drift/历史 battery finding/别项目的 audit）排除；
// 复扫合并沿用签名。空 Checklist → 电池零拨号，纯 findings 驱动（无网络）。

import (
	"strings"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

func auditSameSourceEnv(t *testing.T) (*Manager, *AuditProject) {
	t.Helper()
	writeAuthTestEnv(t) // backups/opsteps/locks 隔离
	findingsDirOverr = t.TempDir()
	auditProjOverr = t.TempDir()
	t.Cleanup(func() { findingsDirOverr, auditProjOverr = "", "" })
	cfg := &config.Config{}
	cfg.NetDev.Devices = []config.NetDevDevice{labDevice()}
	m := NewManager(cfg)
	p := &AuditProject{Name: "上线项目X", Devices: []string{"sw1"}, Checklist: nil}
	if err := SaveAuditProject(p); err != nil {
		t.Fatal(err)
	}
	return m, p
}

func seedFinding(t *testing.T, f *Finding) string {
	t.Helper()
	f.Devices = []string{"sw1"}
	f.Evidence = []Evidence{{Device: "sw1", Command: "display version", Output: "stub"}}
	f.CreatedAt = time.Now().Add(-time.Hour) // 历史（非本轮电池立案）
	if err := SaveFinding(f); err != nil {
		t.Fatal(err)
	}
	return f.ID
}

func TestAuditConsumesChatAnchoredFindingsOnly(t *testing.T) {
	m, p := auditSameSourceEnv(t)

	chat := seedFinding(t, &Finding{Title: "OpenSSH 过旧", Severity: "critical", Source: "audit",
		Detail: "project:上线项目X 套餐阶段：漏洞核查\n版本 8.0 受 CVE-XXX 影响"})
	seedFinding(t, &Finding{Title: "配置漂移噪音", Severity: "warning", Source: "drift:sw1", Detail: "x"})
	seedFinding(t, &Finding{Title: "上月基线遗留", Severity: "warning", Source: "baseline:summary", Detail: "历史电池立案"})
	seedFinding(t, &Finding{Title: "别项目的套餐风险", Severity: "critical", Source: "audit", Detail: "project:上线项目Y\n..."})
	seedFinding(t, &Finding{Title: "无锚点 audit", Severity: "warning", Source: "audit", Detail: "没写锚点"})

	rep, err := m.RunProjectAudit(p)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(rep.Items) != 1 {
		var titles []string
		for _, it := range rep.Items {
			titles = append(titles, it.Title)
		}
		t.Fatalf("risk list must contain ONLY the anchored chat finding, got %d: %v", len(rep.Items), titles)
	}
	if rep.Items[0].FindingID != chat || rep.Items[0].Status != "open" {
		t.Fatalf("wrong item: %+v", rep.Items[0])
	}
	if rep.BatteryNotes["同源"] == "" {
		t.Fatal("report must document the same-source rule")
	}
}

func TestAuditRescanCarriesFixedAndDropsResolved(t *testing.T) {
	m, p := auditSameSourceEnv(t)

	chat := seedFinding(t, &Finding{Title: "OpenSSH 过旧", Severity: "critical", Source: "audit",
		Detail: "project:上线项目X 套餐阶段：漏洞核查"})
	rep1, err := m.RunProjectAudit(p)
	if err != nil || len(rep1.Items) != 1 {
		t.Fatalf("first scan: %v items=%d", err, len(rep1.Items))
	}
	sig := rep1.Items[0].Signature
	if !strings.Contains(sig, "audit") {
		t.Fatalf("signature must key on source: %s", sig)
	}

	// 修复（finding 消失）→ 复扫自动转 fixed（合并语义：未再出现的 open 项回收为 fixed）。
	if err := DismissFinding(chat); err != nil {
		t.Fatal(err)
	}
	rep2, err := m.RunProjectAudit(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep2.Items) != 1 || rep2.Items[0].Status != "fixed" || rep2.Items[0].Title != "OpenSSH 过旧" {
		t.Fatalf("resolved risk must auto-flip to fixed: %+v", rep2.Items)
	}
	// 人工 accepted 同样跨复扫存活（另一条风险）。
	keep := seedFinding(t, &Finding{Title: "接受的风险", Severity: "warning", Source: "audit",
		Detail: "project:上线项目X 套餐阶段：日志异常"})
	rep3, err := m.RunProjectAudit(p)
	if err != nil {
		t.Fatal(err)
	}
	var keepSig string
	for _, it := range rep3.Items {
		if it.FindingID == keep {
			keepSig = it.Signature
		}
	}
	if keepSig == "" {
		t.Fatalf("new anchored risk missing from rescan: %+v", rep3.Items)
	}
	if err := SetAuditItemStatus(p.ID, keepSig, "accepted"); err != nil {
		t.Fatal(err)
	}
	rep4, err := m.RunProjectAudit(p)
	if err != nil {
		t.Fatal(err)
	}
	carried := false
	for _, it := range rep4.Items {
		if it.FindingID == keep && it.Status == "accepted" {
			carried = true
		}
	}
	if !carried {
		t.Fatalf("accepted must carry over the rescan: %+v", rep4.Items)
	}
	_, _, green, err := AuditProjectStatus(p.ID)
	if err != nil || !green {
		t.Fatalf("fixed+accepted 全绿: %v", err)
	}
}
