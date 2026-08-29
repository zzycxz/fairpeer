package netdev

import (
	"context"
	"strings"
	"testing"
)

// §4.1 runbook 升级：体检电池作为 Job 引擎 runbook 执行——每步超时/on-fail
// =continue，job 轨迹持久化，报告分段齐全（真 SSH 全链路）。
func TestTriageRunsOnJobEngine(t *testing.T) {
	m := onlineCheckManager(t) // linux device "host1" on a bash-prompt sim
	jobsDirOverride = t.TempDir()
	t.Cleanup(func() { jobsDirOverride = "" })

	rep := m.Triage(context.Background(), "host1")
	if len(rep.Sections) != len(linuxTriageBattery) {
		t.Fatalf("sections = %d, want %d (summary %q)", len(rep.Sections), len(linuxTriageBattery), rep.Summary)
	}
	if !strings.Contains(rep.Summary, "体检") {
		t.Fatalf("summary = %q", rep.Summary)
	}

	// The run left a job trail with the battery's step count.
	jobs, err := ListJobs()
	if err != nil || len(jobs) == 0 {
		t.Fatalf("no job trail: %v (%v)", jobs, err)
	}
	j := jobs[0]
	if !strings.HasPrefix(j.Name, "triage:host1") || j.CreatedBy != "triage" {
		t.Fatalf("job = %s/%s", j.Name, j.CreatedBy)
	}
	if len(j.StepState) != len(linuxTriageBattery) {
		t.Fatalf("job steps = %d", len(j.StepState))
	}
	if j.Status != JobDone {
		t.Fatalf("job status = %s (note %q)", j.Status, j.PauseNote)
	}
	// on-fail=continue：未知命令的步骤失败但电池收全。
	failed, ok := 0, 0
	for _, st := range j.StepState {
		switch st.Status {
		case JobStepOK:
			ok++
		case JobStepFailed:
			failed++
		}
	}
	if ok+failed != len(linuxTriageBattery) {
		t.Fatalf("battery incomplete: ok=%d failed=%d", ok, failed)
	}

	// 报告段与 job 步骤一一对应：who 段有输出（sim 支持）。
	for i, s := range rep.Sections {
		if s.Name != linuxTriageBattery[i].name || s.Command != linuxTriageBattery[i].cmd {
			t.Fatalf("section %d = %+v", i, s)
		}
	}
	whoIdx := -1
	for i, b := range linuxTriageBattery {
		if b.cmd == "who" {
			whoIdx = i
		}
	}
	if whoIdx >= 0 && len(rep.Sections[whoIdx].Lines) == 0 {
		t.Fatalf("who section empty: %+v", rep.Sections[whoIdx])
	}
}
