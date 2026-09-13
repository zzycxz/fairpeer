package netdev

// runbooktpl.go — 割接 runbook 模板库（F1a，0.2.5 批②；MODEL_DEPLOY_SPEC
// D-1②、GAPS 台账 F1/F1a）。提案侧模板（template.go）管"多设备同型变更"，
// 本文件管"多步骤编排"：模板 = 步骤序列（只读检查步 / 语义门 / 变更段 /
// 决策点）+ {{var}} 变量，save → render（dry-run，逐条分类标注）→ apply
// （渲染产物 = run 定义草稿 + 每个变更段一份 draft 提案）。
//
// 安全语义（零新增写路径）：
//   - render 无任何副作用；
//   - apply 只产生 draft 提案 + 未启动的 run 定义——CutoverStart 既有校验
//     天然兜底：提案必须先被人批准才允许 start（割接执行批准过的变更，
//     从不代批）；
//   - 变量值走 varValueRe 白名单（附录 B-10，与提案模板同款）；渲染产物
//     仍逐条过分类器（read 步）或进提案（write 段），模板不绕过任何闸。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/fileutil"
	"github.com/zzycxz/fairpeer/internal/netdev/driver"
)

// RunbookTplStep is one template step. Exactly one of {command (read check),
// proposal_cmds (change segment), decision_point} carries the step body;
// gate_* optionally attaches a post-step semantic gate.
type RunbookTplStep struct {
	Label string `json:"label"`
	// Device: literal inventory name or {{var}} (e.g. {{core_sw}}).
	Device  string `json:"device,omitempty"`
	Command string `json:"command,omitempty"` // sealed read command, {{vars}} allowed
	// Gate: post-step verification (rendered into CutoverStep.Gate).
	GateCmd     string `json:"gate_cmd,omitempty"`
	GateExpect  string `json:"gate_expect,omitempty"`
	GateSustain int    `json:"gate_sustain_sec,omitempty"` // default 30
	GateTimeout int    `json:"gate_timeout_sec,omitempty"` // default 2×sustain+90
	// ProposalCmds is the change segment: rendered into a DRAFT proposal the
	// human must approve before the run can start. ProposalIntent describes
	// what & why (proposal draft intent).
	ProposalIntent string   `json:"proposal_intent,omitempty"`
	ProposalCmds   []string `json:"proposal_cmds,omitempty"`
	// DecisionPoint / Impact / EstSec map straight onto CutoverStep.
	DecisionPoint bool   `json:"decision_point,omitempty"`
	Impact        string `json:"impact,omitempty"`
	EstSec        int    `json:"est_sec,omitempty"`
}

// RunbookTemplate is one reusable 割接编排. Scenario tags the intended family
// ("model-deploy", "network-cutover", "acceptance"…) — advisory metadata for
// the template picker and project-type defaults（批⑤ G-P2 联动点）.
type RunbookTemplate struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Scenario  string           `json:"scenario,omitempty"`
	Vars      []string         `json:"vars,omitempty"`
	WindowMin int              `json:"window_min,omitempty"` // 割接窗口（总倒计时分钟；0=apply 时必传）
	Steps     []RunbookTplStep `json:"steps"`
	Notes     string           `json:"notes,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
}

// RunbookTplPreviewStep is one rendered step with dry-run verdicts.
type RunbookTplPreviewStep struct {
	Label       string   `json:"label"`
	Device      string   `json:"device,omitempty"`
	Command     string   `json:"command,omitempty"`
	Class       string   `json:"class,omitempty"`         // rendered read command's classifier verdict
	ProposalCmd []string `json:"proposal_cmds,omitempty"` // rendered change segment
	ProposalCl  []string `json:"proposal_classes,omitempty"`
	Decision    bool     `json:"decision_point,omitempty"`
	Notes       []string `json:"notes,omitempty"` // 未解析设备/未知分类等提示
}

// RunbookTplPreview is the dry-run output: everything rendered, nothing saved.
type RunbookTplPreview struct {
	Steps []RunbookTplPreviewStep `json:"steps"`
	Run   *CutoverRun             `json:"run"` // assembled (not started) run definition
	Notes []string                `json:"notes,omitempty"`
}

var runbookTplDirOverride string

func runbookTplsDir() string {
	if runbookTplDirOverride != "" {
		return runbookTplDirOverride
	}
	return filepath.Join(netdevStateDir(), "runbook_templates")
}

var (
	runbookTplMu  sync.Mutex
	runbookTplSeq int
)

func newRunbookTplID() string {
	runbookTplMu.Lock()
	defer runbookTplMu.Unlock()
	runbookTplSeq++
	return fmt.Sprintf("RB%s-%d", time.Now().Format("20060102"), runbookTplSeq)
}

// SaveRunbookTemplate persists t (create or update; ID assigned on create).
func SaveRunbookTemplate(t *RunbookTemplate) error {
	if err := validateRunbookTpl(t); err != nil {
		return err
	}
	if t.ID == "" {
		t.ID = newRunbookTplID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	StateEventSnap(StateEventTplSave, t.ID, StateActorUser, filepath.Join(runbookTplsDir(), t.ID+".json"))
	if err := os.MkdirAll(runbookTplsDir(), 0o700); err != nil {
		return err
	}
	runbookTplMu.Lock()
	defer runbookTplMu.Unlock()
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(filepath.Join(runbookTplsDir(), t.ID+".json"), b, 0o600)
}

func validateRunbookTpl(t *RunbookTemplate) error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("runbook template: name is required")
	}
	if len(t.Steps) == 0 {
		return fmt.Errorf("runbook template %q: no steps", t.Name)
	}
	nameRe := regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	for _, v := range t.Vars {
		if !nameRe.MatchString(v) {
			return fmt.Errorf("runbook template %q: variable name %q must be [A-Za-z0-9_]", t.Name, v)
		}
	}
	for i := range t.Steps {
		s := &t.Steps[i]
		body := 0
		for _, ok := range []bool{s.Command != "", len(s.ProposalCmds) > 0, s.DecisionPoint} {
			if ok {
				body++
			}
		}
		if body != 1 {
			return fmt.Errorf("runbook template %q step %q: exactly one of command / proposal_cmds / decision_point", t.Name, s.Label)
		}
		if s.Command != "" && s.Device == "" {
			return fmt.Errorf("runbook template %q step %q: command step needs device", t.Name, s.Label)
		}
		if (s.GateCmd != "" || s.GateExpect != "") && (s.GateCmd == "" || s.GateExpect == "") {
			return fmt.Errorf("runbook template %q step %q: gate needs command AND expect", t.Name, s.Label)
		}
	}
	return nil
}

// BuiltinRunbookIDPrefix marks seed templates that ship with the product
// (Get falls through to them, Delete refuses them)。
const BuiltinRunbookIDPrefix = "RBB-"

// builtinRunbookSkel is the 批② 空模板骨架：一个能渲染、能走完
// preview→apply→approve→start 全链的最小部署编排示例——机制的活教材。
func builtinRunbookSkel() RunbookTemplate {
	return RunbookTemplate{
		ID:        BuiltinRunbookIDPrefix + "skel-vllm-standalone",
		Name:      "示例：vLLM 单机部署骨架",
		Scenario:  "model-deploy",
		Vars:      []string{"gpu_host", "svc_port"},
		WindowMin: 120,
		Notes:     "内置骨架示例（复制后改）：前置只读→变更段（提案）→语义门→决策点。新场景建议先跑通一次再『另存为模板』。",
		Steps: []RunbookTplStep{
			{Label: "前置检查", Device: "{{gpu_host}}", Command: "nvidia-smi"},
			{Label: "变更：拉起服务", Device: "{{gpu_host}}", ProposalIntent: "启动推理服务",
				ProposalCmds: []string{"systemctl start vllm-{{svc_port}}"}},
			{Label: "语义门", Device: "{{gpu_host}}", Command: "systemctl is-active vllm-{{svc_port}}",
				GateCmd: "systemctl is-active vllm-{{svc_port}}", GateExpect: "active"},
			{Label: "决策点", DecisionPoint: true, Impact: "放流量前人工确认基线压测"},
		},
	}
}

// builtinRunbookLibrary is the F1b 内容库（批③a 模型侧三变体：单机/多机/
// 蓝绿升级）。内容纪律（MODEL_DEPLOY_SPEC §二/§三）：①只读步命令全部在
// linux 读表白名单或 curlReadOverride 语法内（门命令同样——门走密封执行，
// Unknown 会被拒，fail-closed）；②变更段全部走提案（draft，人批后 start）；
// ③权重对账只核 config.json/index 清单（全量 safetensors 哈希按 F2 走登记
// manifest，值班手册簇 4）；④多机变体的主机间协同命令（ray join 等）拆成
// 每台一份提案——绝不出现 ssh 串链（那是分类器绕过原语）。
func builtinRunbookLibrary() []RunbookTemplate {
	lib := []RunbookTemplate{builtinRunbookSkel()}
	lib = append(lib,
		RunbookTemplate{
			ID:        BuiltinRunbookIDPrefix + "deploy-vllm-standalone",
			Name:      "部署：vLLM 单机（systemd）",
			Scenario:  "model-deploy",
			Vars:      []string{"gpu_host", "model_path", "served_name", "svc_port", "tp_size", "gpu_mem_util"},
			WindowMin: 180,
			Notes: "蓝本 MODEL_DEPLOY_SPEC §2.1 八步走查的单机 systemd 形态。变更段含写 unit 与启动参数" +
				"（TP={{tp_size}}/mem-util={{gpu_mem_util}}）——建议先跑 NetDevProfileCheck 过机型/模型档案校验。",
			Steps: []RunbookTplStep{
				{Label: "前置：驱动与可见卡", Device: "{{gpu_host}}", Command: "nvidia-smi", EstSec: 30},
				{Label: "前置：显存水位", Device: "{{gpu_host}}",
					Command: "nvidia-smi --query-gpu=memory.used,memory.total --format=csv", EstSec: 30},
				{Label: "前置：残留进程（簇 2）", Device: "{{gpu_host}}",
					Command: "nvidia-smi --query-compute-apps=pid,process_name,used_memory --format=csv", EstSec: 30},
				{Label: "前置：/dev/shm（簇 3）", Device: "{{gpu_host}}", Command: "df -h /dev/shm", EstSec: 20},
				{Label: "前置：权重目录", Device: "{{gpu_host}}", Command: "ls -l {{model_path}}", EstSec: 20},
				{Label: "权重对账（F2 校验段）", Device: "{{gpu_host}}",
					Command: "sha256sum {{model_path}}/config.json {{model_path}}/model.safetensors.index.json", EstSec: 60},
				{Label: "变更：写 unit 并启动", Device: "{{gpu_host}}",
					ProposalIntent: "部署 {{served_name}}（TP={{tp_size}}，mem-util={{gpu_mem_util}}，端口 {{svc_port}}）",
					ProposalCmds: []string{
						"tee /etc/systemd/system/vllm-{{svc_port}}.service",
						"systemctl daemon-reload",
						"systemctl enable --now vllm-{{svc_port}}.service",
					}, EstSec: 600},
				{Label: "门：服务存活", Device: "{{gpu_host}}", Command: "systemctl is-active vllm-{{svc_port}}",
					GateCmd: "systemctl is-active vllm-{{svc_port}}", GateExpect: "active", GateSustain: 15, EstSec: 60},
				{Label: "门：HTTP 探活", Device: "{{gpu_host}}",
					GateCmd: "curl -I http://127.0.0.1:{{svc_port}}/health", GateExpect: "200", GateSustain: 30, GateTimeout: 1800, EstSec: 300},
				{Label: "决策点：放流量", DecisionPoint: true, Impact: "确认模型名={{served_name}} 可列出、基线压测通过后放流量；回退=stop 新服务"},
			},
		},
		RunbookTemplate{
			ID:        BuiltinRunbookIDPrefix + "deploy-vllm-multinode",
			Name:      "部署：vLLM 多机（head-worker）",
			Scenario:  "model-deploy",
			Vars:      []string{"head_host", "worker_host", "model_path", "svc_port", "tp_size", "nnodes"},
			WindowMin: 240,
			Notes: "多机形态：主机间协同（ray join）拆成每台一份提案，先后序由割接步骤序保证——绝不 ssh 串链。" +
				"互联前置用 ibstat（E9 读表）；NCCL hang 按值班手册簇 3。",
			Steps: []RunbookTplStep{
				{Label: "前置：head 卡可见", Device: "{{head_host}}", Command: "nvidia-smi", EstSec: 30},
				{Label: "前置：worker 卡可见", Device: "{{worker_host}}", Command: "nvidia-smi", EstSec: 30},
				{Label: "前置：互联口状态", Device: "{{head_host}}", Command: "ibstat", EstSec: 30},
				{Label: "前置：双机权重目录", Device: "{{head_host}}", Command: "ls -l {{model_path}}", EstSec: 20},
				{Label: "变更：head 起 Ray 头", Device: "{{head_host}}",
					ProposalIntent: "Ray head 启动 + vLLM 服务端（nnodes={{nnodes}}，TP={{tp_size}}）",
					ProposalCmds:   []string{"ray start --head", "systemctl start vllm-{{svc_port}}"}, EstSec: 600},
				{Label: "变更：worker 加入", Device: "{{worker_host}}",
					ProposalIntent: "worker 加入 Ray 集群（指向 {{head_host}}）",
					ProposalCmds:   []string{"ray start --address={{head_host}}:6379"}, EstSec: 300},
				{Label: "门：head 服务存活", Device: "{{head_host}}",
					GateCmd: "systemctl is-active vllm-{{svc_port}}", GateExpect: "active", GateSustain: 30, EstSec: 120},
				{Label: "门：head 端口监听", Device: "{{head_host}}",
					GateCmd: "ss -ltn", GateExpect: ":{{svc_port}}", GateSustain: 30, EstSec: 120},
				{Label: "决策点：全集群就绪后放流量", DecisionPoint: true,
					Impact: "确认 nnodes={{nnodes}} 全部在线、worker 无掉卡；回退=双机 stop + ray stop"},
			},
		},
		RunbookTemplate{
			ID:        BuiltinRunbookIDPrefix + "upgrade-vllm-bluegreen",
			Name:      "升级：vLLM 蓝绿（双端口并行）",
			Scenario:  "model-upgrade",
			Vars:      []string{"gpu_host", "svc_old", "svc_new", "port_old", "port_new"},
			WindowMin: 120,
			Notes: "蓝绿升级：新版在新端口拉起→门验证→决策点切流→旧版下线→决策点关回退窗。" +
				"TP 启动时固定——改 TP 不走本模板（那是 TP 重排=蓝绿换队，批③b 变体）；流量编排=双版本+流量百分比原语。",
			Steps: []RunbookTplStep{
				{Label: "前置：旧版健康", Device: "{{gpu_host}}",
					Command: "systemctl is-active {{svc_old}}", EstSec: 20},
				{Label: "前置：旧版探活", Device: "{{gpu_host}}",
					Command: "curl -I http://127.0.0.1:{{port_old}}/health", EstSec: 20},
				{Label: "变更：新版拉起（新端口，不动旧版）", Device: "{{gpu_host}}",
					ProposalIntent: "拉起 {{svc_new}}（端口 {{port_new}}），{{svc_old}} 保持在线",
					ProposalCmds:   []string{"systemctl enable --now {{svc_new}}.service"}, EstSec: 900},
				{Label: "门：新版 HTTP 探活", Device: "{{gpu_host}}",
					GateCmd: "curl -I http://127.0.0.1:{{port_new}}/health", GateExpect: "200", GateSustain: 60, GateTimeout: 1800, EstSec: 600},
				{Label: "决策点：切流确认", DecisionPoint: true,
					Impact: "新旧版本 TTFT/输出质量对比通过后切流；此刻回退=stop 新版（零影响）"},
				{Label: "变更：旧版下线", Device: "{{gpu_host}}",
					ProposalIntent: "停用 {{svc_old}}（流量已在 {{port_new}}）",
					ProposalCmds:   []string{"systemctl disable --now {{svc_old}}.service"}, EstSec: 120},
				{Label: "门：新版持续探活", Device: "{{gpu_host}}",
					GateCmd: "curl -I http://127.0.0.1:{{port_new}}/health", GateExpect: "200", GateSustain: 60, EstSec: 120},
				{Label: "决策点：关闭回退窗口", DecisionPoint: true,
					Impact: "观察期过后关闭；此刻起回退需重新部署旧版本"},
			},
		},
	)
	return lib
}

// GetRunbookTemplate loads one template; built-in seeds fall through when no
// user file shadows them.
func GetRunbookTemplate(id string) (*RunbookTemplate, error) {
	if !validStoreID(id) {
		return nil, fmt.Errorf("runbook template %s: invalid id", id)
	}
	b, err := os.ReadFile(filepath.Join(runbookTplsDir(), id+".json"))
	if err == nil {
		var t RunbookTemplate
		if err := json.Unmarshal(b, &t); err != nil {
			return nil, err
		}
		return &t, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	if strings.HasPrefix(id, BuiltinRunbookIDPrefix) {
		for _, t := range builtinRunbookLibrary() {
			if t.ID == id {
				return &t, nil
			}
		}
	}
	return nil, os.ErrNotExist
}

// ListRunbookTemplates returns all templates (user files + built-in seeds),
// newest first.
func ListRunbookTemplates() ([]RunbookTemplate, error) {
	entries, err := os.ReadDir(runbookTplsDir())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	out := []RunbookTemplate{}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		t, err := GetRunbookTemplate(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue // 坏文件跳过——模板库是便利层，一张坏卡不拖垮列表
		}
		seen[t.ID] = true
		out = append(out, *t)
	}
	for _, b := range builtinRunbookLibrary() {
		if !seen[b.ID] {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// DeleteRunbookTemplate removes one template.
func DeleteRunbookTemplate(id string) error {
	if !validStoreID(id) {
		return fmt.Errorf("runbook template %s: invalid id", id)
	}
	if strings.HasPrefix(id, BuiltinRunbookIDPrefix) {
		return fmt.Errorf("runbook template %s: 内置模板不可删除——复制一份后改副本", id)
	}
	StateEventSnap(StateEventTplDelete, id, StateActorUser, filepath.Join(runbookTplsDir(), id+".json"))
	runbookTplMu.Lock()
	defer runbookTplMu.Unlock()
	return os.Remove(filepath.Join(runbookTplsDir(), id+".json"))
}

// renderTplString substitutes {{var}} placeholders. Declared vars must have
// whitelisted values; leftover placeholders (undeclared) are an error —
// silent half-rendered commands are how classifier bypasses are born.
func renderTplString(s string, values map[string]string) (string, error) {
	var firstErr error
	out := templatePlaceholderRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-2]
		v, ok := values[name]
		if !ok && firstErr == nil {
			firstErr = fmt.Errorf("variable {{%s}} has no value", name)
			return m
		}
		return v
	})
	return out, firstErr
}

// (values validation shared with proposal templates' varValueRe whitelist.)
func validateRunbookVarValues(t *RunbookTemplate, values map[string]string) error {
	for _, name := range t.Vars {
		v, ok := values[name]
		if !ok || strings.TrimSpace(v) == "" {
			return fmt.Errorf("variable %q: value required", name)
		}
		if !varValueRe.MatchString(v) {
			return fmt.Errorf("variable %q: value contains characters outside the whitelist", name)
		}
	}
	return nil
}

// renderRunbookSteps does the shared substitution for preview & apply.
func renderRunbookSteps(t *RunbookTemplate, values map[string]string) ([]RunbookTplStep, error) {
	if err := validateRunbookVarValues(t, values); err != nil {
		return nil, err
	}
	out := make([]RunbookTplStep, len(t.Steps))
	for i := range t.Steps {
		s := t.Steps[i]
		var err error
		for _, dst := range []*string{&s.Label, &s.Device, &s.Command, &s.GateCmd, &s.GateExpect, &s.ProposalIntent, &s.Impact} {
			if *dst, err = renderTplString(*dst, values); err != nil {
				return nil, fmt.Errorf("step %q: %v", s.Label, err)
			}
		}
		for j := range s.ProposalCmds {
			if s.ProposalCmds[j], err = renderTplString(s.ProposalCmds[j], values); err != nil {
				return nil, fmt.Errorf("step %q: %v", s.Label, err)
			}
		}
		out[i] = s
	}
	return out, nil
}

// classifyRendered is the preview-side verdict for one rendered command on one
// rendered device. Unresolvable device (var'd device not in inventory) yields
// class "?" with a note — the classifier still has the final say at run time.
func (m *Manager) classifyRendered(dev, cmd string) (string, []string) {
	var notes []string
	if dev == "" {
		return "?", []string{"设备未解析（变量缺失或清单外）"}
	}
	d, ok := m.cfg.NetDevDeviceByName(dev)
	if !ok {
		return "?", []string{fmt.Sprintf("设备 %q 不在清单——渲染结果按未知分类展示", dev)}
	}
	drv, ok := m.driverFor(d)
	if !ok {
		return "?", []string{fmt.Sprintf("设备 %q 无驱动", dev)}
	}
	class := drv.Classify(cmd)
	if class == driver.Unknown {
		if _, allow := logPathReadOverride(d, drv, cmd); allow {
			class = driver.Read
		} else if c, allow := curlReadOverride(drv, cmd); allow {
			class = c
		}
	}
	return class.String(), notes
}

// PreviewRunbookTemplate renders the template (dry-run): substitutions +
// per-command classifier verdicts + the assembled-but-NOT-started run
// definition. Zero side effects.
func (m *Manager) PreviewRunbookTemplate(id string, values map[string]string) (*RunbookTplPreview, error) {
	t, err := GetRunbookTemplate(id)
	if err != nil {
		return nil, err
	}
	steps, err := renderRunbookSteps(t, values)
	if err != nil {
		return nil, err
	}
	p := &RunbookTplPreview{Steps: []RunbookTplPreviewStep{}}
	run := &CutoverRun{Name: t.Name, Steps: []CutoverStep{}}
	if t.WindowMin > 0 {
		run.Deadline = time.Now().Add(time.Duration(t.WindowMin) * time.Minute)
	}
	for i := range steps {
		s := steps[i]
		ps := RunbookTplPreviewStep{Label: s.Label}
		cs := CutoverStep{Label: s.Label, Impact: s.Impact, EstSec: s.EstSec, DecisionPoint: s.DecisionPoint}
		switch {
		case s.Command != "":
			ps.Device, ps.Command = s.Device, s.Command
			ps.Class, ps.Notes = m.classifyRendered(s.Device, s.Command)
			cs.Device, cs.Command = s.Device, s.Command
		case len(s.ProposalCmds) > 0:
			ps.ProposalCmd = s.ProposalCmds
			for _, c := range s.ProposalCmds {
				cls, notes := m.classifyRendered(s.Device, c)
				ps.ProposalCl = append(ps.ProposalCl, cls)
				ps.Notes = append(ps.Notes, notes...)
			}
			// apply 阶段才落 draft 提案；预览只标注。
			ps.Notes = append(ps.Notes, "变更段：apply 时生成为 draft 提案，人批后方可启动")
		case s.DecisionPoint:
			ps.Decision = true
		}
		if s.GateCmd != "" {
			cs.Gate = &CutoverGate{Device: s.Device, Command: s.GateCmd, Expect: s.GateExpect, SustainSec: s.GateSustain, TimeoutSec: s.GateTimeout}
			gc, gn := m.classifyRendered(s.Device, s.GateCmd)
			ps.Notes = append(ps.Notes, gn...)
			ps.Notes = append(ps.Notes, fmt.Sprintf("门命令分类 %s", gc))
		}
		p.Steps = append(p.Steps, ps)
		run.Steps = append(run.Steps, cs)
	}
	p.Run = run
	return p, nil
}

// RunbookApplyResult carries the rendered, NOT-started run plus the draft
// proposals its change segments became. The human approves the proposals,
// then starts the run through the normal path — the template never approves.
type RunbookApplyResult struct {
	Run       *CutoverRun `json:"run"`
	Proposals []*Proposal `json:"proposals"`
	Notes     []string    `json:"notes,omitempty"`
}

// ApplyRunbookTemplate renders and materializes: one draft proposal per
// change-segment step, a run definition referencing them, nothing running.
func (m *Manager) ApplyRunbookTemplate(id string, values map[string]string, runName string) (*RunbookApplyResult, error) {
	t, err := GetRunbookTemplate(id)
	if err != nil {
		return nil, err
	}
	steps, err := renderRunbookSteps(t, values)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(runName) == "" {
		runName = t.Name
	}
	res := &RunbookApplyResult{Proposals: []*Proposal{}, Notes: []string{}}
	run := &CutoverRun{Name: runName}
	if t.WindowMin > 0 {
		run.Deadline = time.Now().Add(time.Duration(t.WindowMin) * time.Minute)
	}
	for i := range steps {
		s := steps[i]
		cs := CutoverStep{Label: s.Label, Impact: s.Impact, EstSec: s.EstSec, DecisionPoint: s.DecisionPoint}
		switch {
		case s.Command != "":
			cs.Device, cs.Command = s.Device, s.Command
		case len(s.ProposalCmds) > 0:
			intent := strings.TrimSpace(s.ProposalIntent)
			if intent == "" {
				intent = fmt.Sprintf("%s / %s", t.Name, s.Label)
			}
			p := &Proposal{Intent: fmt.Sprintf("[runbook %s] %s", t.Name, intent), Status: ProposalDraft}
			if s.Device == "" {
				return nil, fmt.Errorf("步骤 %q：变更段缺设备——提案步必须落清单设备", s.Label)
			}
			p.Steps = append(p.Steps, ProposalStep{Device: s.Device, Type: "cli", Commands: s.ProposalCmds})
			if err := SaveProposal(p); err != nil {
				return nil, fmt.Errorf("步骤 %q：提案草稿落库失败: %v", s.Label, err)
			}
			cs.ProposalID = p.ID
			res.Proposals = append(res.Proposals, p)
		case s.DecisionPoint:
			// body 本身就是决策点，无命令。
		}
		if s.GateCmd != "" {
			cs.Gate = &CutoverGate{Device: s.Device, Command: s.GateCmd, Expect: s.GateExpect, SustainSec: s.GateSustain, TimeoutSec: s.GateTimeout}
		}
		run.Steps = append(run.Steps, cs)
	}
	res.Run = run
	StateEventSnap(StateEventTplApply, t.ID, StateActorUser, fmt.Sprintf("proposals=%d steps=%d", len(res.Proposals), len(run.Steps)))
	return res, nil
}

// ExtractRunbookTemplate is the F1c 出口②: a run that actually happened gets
// distilled back into a reusable template — 现场经验沉淀为资产. Proposal steps
// are dereferenced (their cli commands become the template's change segment);
// decision points and gates carry over verbatim. Terminal runs only — a run
// still in flight hasn't earned generalization yet.
func ExtractRunbookTemplate(run *CutoverRun, name string) (*RunbookTemplate, error) {
	if run == nil || run.ID == "" {
		return nil, fmt.Errorf("runbook extract: source run required")
	}
	switch run.Status {
	case CutoverDone, CutoverFailed, CutoverAborted:
	default:
		return nil, fmt.Errorf("runbook extract %s: status %q is not terminal — 等跑完再沉淀", run.ID, run.Status)
	}
	t := &RunbookTemplate{Name: strings.TrimSpace(name), Notes: fmt.Sprintf("抽取自 run %s（状态 %s）", run.ID, run.Status)}
	t.Steps = make([]RunbookTplStep, 0, len(run.Steps))
	var extractNotes []string
	for i := range run.Steps {
		s := run.Steps[i]
		ts := RunbookTplStep{
			Label: s.Label, Impact: s.Impact, EstSec: s.EstSec, DecisionPoint: s.DecisionPoint,
		}
		switch {
		case s.ProposalID != "":
			p, err := GetProposal(s.ProposalID)
			if err != nil {
				extractNotes = append(extractNotes, fmt.Sprintf("步骤 %q：提案 %s 读取失败（已删？）——该步以占位保留", s.Label, s.ProposalID))
				ts.ProposalIntent = "(提案不可读，需人工补写)"
				t.Steps = append(t.Steps, ts)
				continue
			}
			ts.ProposalIntent = p.Intent
			for _, ps := range p.Steps {
				if ps.Device != "" && ts.Device == "" {
					ts.Device = ps.Device
				}
				if ps.Type == "" || ps.Type == "cli" {
					ts.ProposalCmds = append(ts.ProposalCmds, ps.Commands...)
				} else {
					extractNotes = append(extractNotes, fmt.Sprintf("步骤 %q：提案含结构化载荷（%s），无法模板化——apply 时需人工补写", s.Label, ps.Type))
				}
			}
		case s.Device != "" && s.Command != "":
			ts.Device, ts.Command = s.Device, s.Command
			if s.Gate != nil {
				ts.GateCmd, ts.GateExpect = s.Gate.Command, s.Gate.Expect
				ts.GateSustain, ts.GateTimeout = s.Gate.SustainSec, s.Gate.TimeoutSec
			}
		case s.DecisionPoint:
			// 决策点本体无命令——原样带走。
		default:
			continue
		}
		t.Steps = append(t.Steps, ts)
	}
	if len(t.Steps) == 0 {
		return nil, fmt.Errorf("runbook extract %s: nothing extractable", run.ID)
	}
	t.Notes += "；" + strings.Join(extractNotes, "；")
	if err := validateRunbookTpl(t); err != nil {
		return nil, err
	}
	return t, nil
}
