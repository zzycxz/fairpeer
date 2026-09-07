package netdev

import (
	"testing"
)

// SCENARIO_SPEC S5：项目审计实体 CRUD + 风险清单复扫合并/放行判定。
func TestAuditProjectLifecycle(t *testing.T) {
	auditProjOverr = t.TempDir()
	t.Cleanup(func() { auditProjOverr = "" })

	p := &AuditProject{Name: "上线项目A", Devices: []string{"sw1"}, Checklist: []string{"baseline", "logs"}}
	if err := SaveAuditProject(p); err != nil {
		t.Fatal(err)
	}
	if p.ID == "" {
		t.Fatal("id not assigned")
	}
	got, err := ListAuditProjects()
	if err != nil || len(got) != 1 || got[0].Name != "上线项目A" {
		t.Fatalf("list = %v err=%v", got, err)
	}

	// 手造一份报告（open 风险）→ 状态未绿
	rep := &AuditReport{ProjectID: p.ID, At: "20260905-120000", Items: []AuditItem{
		{Signature: "sw1|Telnet 管理服务开启|baseline:summary", Title: "Telnet 管理服务开启", Device: "sw1", Severity: "warning", Status: "open", FirstSeen: "20260905-120000"},
	}}
	if err := saveAuditReportForTest(rep); err != nil {
		t.Fatal(err)
	}
	_, _, green, err := AuditProjectStatus(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if green {
		t.Fatal("open item must not be green")
	}

	// 人工 accepted → 放行绿
	if err := SetAuditItemStatus(p.ID, "sw1|Telnet 管理服务开启|baseline:summary", "accepted"); err != nil {
		t.Fatal(err)
	}
	_, _, green, err = AuditProjectStatus(p.ID)
	if err != nil || !green {
		t.Fatalf("after accept green=%v err=%v", green, err)
	}

	if err := DeleteAuditProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := ListAuditProjects(); len(got) != 0 {
		t.Fatalf("delete left %d", len(got))
	}
}
