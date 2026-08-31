package main

// browser_console_app.go — Wails bindings for 运维 dock 的「浏览器」
// console tab: manual browser-control primitives over the kernel's console
// session slot, the recording trace → SKILL.md generator (provider-backed,
// with a naive deterministic fallback), and the skill save/list/read +
// trial-run surface the panel's editor drives.
//
// Design guards (plan: 受控版):
//   - generation is one structured provider call; failures degrade to the
//     naive conversion, never to an error wall
//   - saving validates frontmatter + name, refuses same-name overwrites
//     unless the panel explicitly passes overwrite (after asking the user)
//   - trial run emits per-step events ("browser:trial") and stops at the
//     first failure — the panel renders progress

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/zzycxz/fairpeer/internal/boot"
	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/provider"
	"github.com/zzycxz/fairpeer/internal/tool/builtin"
)

// --- console primitives -----------------------------------------------------------

func (a *App) BrowserConsoleOpen(cdpURL string, url string) (builtin.ConsoleState, error) {
	return builtin.ConsoleOpen(cdpURL, url)
}

func (a *App) BrowserConsoleState() (builtin.ConsoleState, error) {
	return builtin.ConsoleStateOf()
}

func (a *App) BrowserConsoleClose() error {
	return builtin.ConsoleClose()
}

func (a *App) BrowserConsoleNavigate(url string) (string, error) {
	return builtin.ConsoleNavigate(url)
}

func (a *App) BrowserConsoleElements() ([]builtin.ConsoleElement, error) {
	return builtin.ConsoleElements()
}

func (a *App) BrowserConsoleClick(target string) (string, error) {
	return builtin.ConsoleClick(target)
}

func (a *App) BrowserConsoleType(target string, text string) (string, error) {
	return builtin.ConsoleType(target, text)
}

func (a *App) BrowserConsoleKey(key string) error {
	return builtin.ConsoleKey(key)
}

func (a *App) BrowserConsoleScroll(direction string, amount int) (string, error) {
	return builtin.ConsoleScroll(direction, amount)
}

func (a *App) BrowserConsoleSelectOption(target string, value string) (string, error) {
	return builtin.ConsoleSelectOption(target, value)
}

func (a *App) BrowserConsoleUploadFile(target string, files []string) (string, error) {
	return builtin.ConsoleUploadFile(target, files)
}

func (a *App) BrowserConsoleWait(condition string, timeoutSec int) (string, error) {
	return builtin.ConsoleWait(condition, timeoutSec)
}

func (a *App) BrowserConsoleExtract(selector string) (string, error) {
	return builtin.ConsoleExtract(selector)
}

func (a *App) BrowserConsoleScreenshot() (string, error) {
	return builtin.ConsoleScreenshot()
}

func (a *App) BrowserConsoleEvaluate(expression string) (string, error) {
	return builtin.ConsoleEvaluate(expression)
}

func (a *App) BrowserConsoleRecordStart() error {
	return builtin.ConsoleRecordStart()
}

// BrowserConsoleRecordStop returns the raw trace. Deterministic filtering
// happens next via BrowserConsoleFilterTrace (pure, also exposed so the
// frontend can re-filter after manual tweaks).
func (a *App) BrowserConsoleRecordStop() ([]builtin.ConsoleRecordEvent, error) {
	return builtin.ConsoleRecordStop(), nil
}

type BrowserConsoleTraceFilter struct {
	Kept    []builtin.ConsoleRecordEvent `json:"kept"`
	Dropped []builtin.ConsoleRecordEvent `json:"dropped"`
}

func (a *App) BrowserConsoleFilterTrace(events []builtin.ConsoleRecordEvent) (BrowserConsoleTraceFilter, error) {
	kept, dropped := builtin.FilterRecordEvents(events)
	return BrowserConsoleTraceFilter{Kept: kept, Dropped: dropped}, nil
}

// initBrowserConsoleForwarders wires the kernel's record-event sink to the
// Wails channel ("browser:record"). Called once from startup.
func (a *App) initBrowserConsoleForwarders() {
	builtin.SetBrowserRecordSink(func(rec builtin.ConsoleRecordEvent) {
		if a.ctx == nil {
			return
		}
		wailsruntime.EventsEmit(a.ctx, "browser:record", rec)
	})
}

// --- skill generation ----------------------------------------------------------------

// BrowserSkillDraft is the AI (or fallback) comprehension result: a complete
// SKILL.md text plus metadata about how it was produced.
type BrowserSkillDraft struct {
	Name     string `json:"name"`
	Content  string `json:"content"`
	Fallback bool   `json:"fallback"` // true → naive deterministic conversion
	Detail   string `json:"detail,omitempty"`
}

// generateSkillSystemPrompt is the comprehension contract (borrowing Hermes'
// reflection-prompt shape + generality rules, specialized to browser traces).
const generateSkillSystemPrompt = `你是浏览器操作技能生成器。输入是用户在浏览器中手动操作的事件轨迹（已过滤噪音）。
把轨迹归纳成一份可复用的 SKILL.md 技能文件。严格输出 JSON：{"name": "<技能名>", "content": "<SKILL.md 全文>"}，不要输出其它内容。

SKILL.md 必须遵守：
1. YAML frontmatter 含 name（小写字母数字与连字符）、description（一句话，≤60字，说明何时用）、runAs: subagent、allowed-tools（从 browser_open, browser_navigate, browser_click, browser_type, browser_wait, browser_extract, browser_screenshot, browser_scroll, browser_select_option 中按需选择）。
2. 正文四个段落，标题固定：
   ## 何时使用 —— 触发条件
   ## 步骤 —— 一张 markdown 表格，列：# | 操作 | 目标 | 值。操作只限 navigate/click/type/key/wait/extract/screenshot/scroll/select。目标用 CSS 选择器（轨迹里有）。
   ## 注意事项 —— 轨迹中的坑（失败的尝试、需要等待的页面、易错点）
   ## 验证 —— 怎么确认执行成功（基于轨迹最后的 extract/页面结果）
3. 通用性：把会变的值（用户名、日期、单号）写成 {{参数名}} 并在「何时使用」段落列出参数说明；绝不写入密码——密码字段写成 {{密码}} 占位并在注意事项注明运行时询问；不写死绝对路径。
4. 步骤要覆盖轨迹的有效操作，但可按意图合并、删除明显冗余；点击后页面加载慢的位置补 wait networkidle 步骤。
5. 全文不超过 120 行。`

// BrowserConsoleGenerateSkill runs the provider-backed comprehension over a
// (usually filtered) trace. nameHint seeds the skill name when the model
// doesn't provide one or generation falls back.
func (a *App) BrowserConsoleGenerateSkill(nameHint string, events []builtin.ConsoleRecordEvent) (BrowserSkillDraft, error) {
	if len(events) == 0 {
		return BrowserSkillDraft{}, fmt.Errorf("轨迹为空——先录制一些操作")
	}
	trace, err := json.Marshal(events)
	if err != nil {
		return BrowserSkillDraft{}, fmt.Errorf("marshal trace: %w", err)
	}
	user := fmt.Sprintf("技能名提示：%s\n\n轨迹事件（JSON，type: click/input/change/submit/navigate；selector 为 CSS；name 为元素可见名；password=true 表示密码框，值未记录）：\n%s",
		nameHint, string(trace))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	raw, gerr := a.consoleProviderChat(ctx, generateSkillSystemPrompt, user)
	if gerr == nil {
		if draft, perr := parseSkillDraft(raw); perr == nil {
			draft.Content = normalizeSkillContent(draft.Content)
			if skillContentParsable(draft.Content) {
				if draft.Name == "" {
					draft.Name = sanitizeSkillName(nameHint)
				}
				return draft, nil
			}
			// Parsed JSON but the markdown body is malformed (fences the
			// model wouldn't drop, missing frontmatter) — fall through to
			// the deterministic conversion instead of handing the editor an
			// unparseable draft (its guardrail would block the structured
			// mode; verified live on the 2026-08-29 E2E).
			gerr = fmt.Errorf("生成的技能格式不合规")
		} else {
			gerr = perr
		}
	}
	// Degraded path: deterministic conversion. Never an error wall — the
	// editor can fix whatever the naive form gets wrong.
	detail := ""
	if gerr != nil {
		detail = "AI 理解不可用（" + gerr.Error() + "），已退化为朴素转换"
	}
	return naiveSkillDraft(nameHint, events, detail), nil
}

// normalizeSkillContent strips the wrappers models tend to add around the
// SKILL.md body (```markdown fences, leading prose) so the content starts
// at its frontmatter.
func normalizeSkillContent(content string) string {
	text := strings.TrimSpace(content)
	// Outer ```markdown fence.
	if fence := regexp.MustCompile("(?s)^```[a-zA-Z]*\\s*(.*?)\\s*```$").FindStringSubmatch(text); fence != nil {
		text = strings.TrimSpace(fence[1])
	}
	// Leading prose before the first --- frontmatter line: drop it.
	if idx := strings.Index(text, "\n---"); idx > 0 && !strings.HasPrefix(text, "---") {
		if head := text[:idx]; !strings.Contains(head, "name:") {
			text = strings.TrimSpace(text[idx+1:])
		}
	}
	return text
}

// skillContentParsable is the floor the editor's parser needs: a frontmatter
// block starting the content with a name field.
func skillContentParsable(content string) bool {
	if !strings.HasPrefix(content, "---") {
		return false
	}
	rest := content[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return false
	}
	return strings.Contains(rest[:end], "name:")
}

// parseSkillDraft unwraps the model's JSON (tolerating ```json fences).
func parseSkillDraft(raw string) (BrowserSkillDraft, error) {
	text := strings.TrimSpace(raw)
	if fence := regexp.MustCompile("(?s)^```[a-zA-Z]*\\s*(.*?)\\s*```$").FindStringSubmatch(text); fence != nil {
		text = fence[1]
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return BrowserSkillDraft{}, fmt.Errorf("model output is not JSON")
	}
	var draft BrowserSkillDraft
	if err := json.Unmarshal([]byte(text[start:end+1]), &draft); err != nil {
		return BrowserSkillDraft{}, err
	}
	if strings.TrimSpace(draft.Content) == "" {
		return BrowserSkillDraft{}, fmt.Errorf("draft content is empty")
	}
	return draft, nil
}

// naiveSkillDraft converts a trace mechanically: one step per event, values
// verbatim except password fields. This is the floor quality — human review
// in the editor is expected either way.
func naiveSkillDraft(nameHint string, events []builtin.ConsoleRecordEvent, detail string) BrowserSkillDraft {
	// The name must never be empty — an empty frontmatter name trips both the
	// editor's parser guardrail and the save validation (verified live on the
	// 2026-08-29 E2E: a Chinese-only hint sanitizes to "" here).
	name := sanitizeSkillName(nameHint)
	if name == "" {
		name = "browser-skill"
	}
	title := strings.TrimSpace(nameHint)
	if title == "" {
		title = name
	}
	var b strings.Builder
	b.WriteString("---\nname: ")
	b.WriteString(name)
	b.WriteString("\ndescription: 浏览器操作技能（录制朴素转换，请人工完善描述）。\nrunAs: subagent\nallowed-tools: browser_open, browser_navigate, browser_click, browser_type, browser_wait, browser_extract, browser_screenshot\n---\n\n")
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n## 何时使用\n\n（待补充：说明这个操作流程的适用场景与参数）\n\n## 步骤\n\n| # | 操作 | 目标 | 值 |\n|---|------|------|------|\n")
	n := 0
	for _, ev := range events {
		var op, target, value string
		switch ev.Type {
		case "navigate":
			op, target, value = "navigate", ev.URL, ""
		case "click":
			op, target = "click", ev.Selector
			if ev.Password {
				continue // password-field clicks carry no step
			}
		case "input":
			op, target = "type", ev.Selector
			if ev.Password {
				value = "{{密码}}"
			} else {
				value = ev.Value
			}
		case "change":
			op, target, value = "select", ev.Selector, ev.Value
		case "submit":
			op, target = "key", ev.Selector
			value = "enter"
		default:
			continue
		}
		n++
		fmt.Fprintf(&b, "| %d | %s | `%s` | %s |\n", n, op, target, value)
	}
	b.WriteString("\n## 注意事项\n\n- 本技能由录制朴素转换生成，请人工检查步骤与目标选择器\n- 密码字段已用 {{密码}} 占位，运行时询问\n\n## 验证\n\n（待补充：如何确认执行成功）\n")
	return BrowserSkillDraft{Name: name, Content: b.String(), Fallback: true, Detail: detail}
}

// consoleProviderChat makes one structured text-model call via the user's
// provider config. Resolution mirrors browser_auto's chain, plus one more
// level the static chain misses (verified live on the 2026-08-29 E2E: every
// static knob was empty while the chat itself ran on the active tab's model):
// the ACTIVE TAB's current model — what the user actually sees answering.
func (a *App) consoleProviderChat(ctx context.Context, system, user string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	ref := strings.TrimSpace(cfg.Cowork.BrowserUseModel)
	if ref == "" {
		ref = strings.TrimSpace(cfg.Cowork.VLMModel)
	}
	if ref == "" {
		a.mu.RLock()
		if tab := a.activeTabLocked(); tab != nil {
			ref = strings.TrimSpace(tab.model)
		}
		a.mu.RUnlock()
	}
	if ref == "" {
		ref = strings.TrimSpace(cfg.DefaultModel)
	}
	if ref == "" {
		// Same fallback the chat executor uses for an empty ref: the first
		// configured provider with a key. Persisted tabs don't store their
		// model (E2E 2026-08-29), so "active tab model" alone can be empty on
		// a restored session while the chat itself still runs.
		ref, _, _ = cfg.ResolveModelWithFallback("")
	}
	if ref == "" {
		return "", fmt.Errorf("未配置模型（设置中选择默认模型后重试）")
	}
	entry, ok := cfg.ResolveModel(ref)
	if !ok {
		return "", fmt.Errorf("模型 %q 不是已配置的 provider", ref)
	}
	prov, err := boot.NewProviderWithProxy(entry, cfg.NetworkProxySpec(), false)
	if err != nil {
		return "", fmt.Errorf("build provider %q: %w", ref, err)
	}
	ch, err := prov.Stream(ctx, provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: system},
			{Role: provider.RoleUser, Content: user},
		},
		MaxTokens: 4096,
	})
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			return "", chunk.Err
		}
		if chunk.Type == provider.ChunkText {
			sb.WriteString(chunk.Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("模型返回为空")
	}
	return sb.String(), nil
}

// --- skill persistence ----------------------------------------------------------------

// BrowserConsoleSkill is one entry of the panel's generated-skills list.
type BrowserConsoleSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Browser     bool   `json:"browser"` // allowed-tools contains browser_*
}

func userSkillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".fairpeer", "skills"), nil
}

var skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-_]*$`)

// sanitizeSkillName keeps the skill dir nameable: lowercase, letters/digits/
// dash/underscore, capped length. Chinese hints fall back to the default.
func sanitizeSkillName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r == ' ', r == '_', r == '-', r == '.':
			return '-'
		case unicode.Is(unicode.Han, r):
			return -1 // strip CJK rather than transliterate badly
		default:
			return -1
		}
	}, n)
	for strings.Contains(n, "--") {
		n = strings.ReplaceAll(n, "--", "-")
	}
	n = strings.Trim(n, "-")
	if len(n) > 48 {
		n = n[:48]
	}
	return n
}

// BrowserConsoleSaveSkill validates and writes the SKILL.md. Same-name
// refusal unless overwrite (the panel asks the user first).
func (a *App) BrowserConsoleSaveSkill(content string, overwrite bool) (string, error) {
	fm, body, err := splitSkillFrontmatter(content)
	if err != nil {
		return "", err
	}
	if !strings.Contains(fm, "name:") {
		return "", fmt.Errorf("frontmatter 缺少 name 字段")
	}
	if !strings.Contains(fm, "description:") {
		return "", fmt.Errorf("frontmatter 缺少 description 字段")
	}
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("技能正文为空")
	}
	name := sanitizeSkillName(frontmatterValue(fm, "name"))
	if !skillNameRe.MatchString(name) {
		return "", fmt.Errorf("技能名 %q 无效（小写字母/数字/连字符）", name)
	}
	if len([]rune(frontmatterValue(fm, "description"))) > 200 {
		return "", fmt.Errorf("description 过长（>200 字符）")
	}
	root, err := userSkillsDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, name)
	target := filepath.Join(dir, "SKILL.md")
	if _, statErr := os.Stat(target); statErr == nil && !overwrite {
		return "", fmt.Errorf("已存在同名技能 %q——保存将覆盖它", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return "", err
	}
	return target, nil
}

// BrowserConsoleListSkills scans the user skills dir (name/description from
// frontmatter; Browser marks browser-capable skills the editor can reopen).
func (a *App) BrowserConsoleListSkills() ([]BrowserConsoleSkill, error) {
	root, err := userSkillsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []BrowserConsoleSkill{}, nil
		}
		return nil, err
	}
	out := []BrowserConsoleSkill{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(root, e.Name(), "SKILL.md"))
		if rerr != nil {
			continue
		}
		fm, _, serr := splitSkillFrontmatter(string(raw))
		if serr != nil {
			continue
		}
		out = append(out, BrowserConsoleSkill{
			Name:        e.Name(),
			Description: frontmatterValue(fm, "description"),
			Browser:     strings.Contains(fm, "browser_"),
		})
	}
	return out, nil
}

// BrowserConsoleReadSkill loads one SKILL.md for the editor.
func (a *App) BrowserConsoleReadSkill(name string) (string, error) {
	clean := sanitizeSkillName(name)
	if !skillNameRe.MatchString(clean) {
		return "", fmt.Errorf("技能名 %q 无效", name)
	}
	root, err := userSkillsDir()
	if err != nil {
		return "", err
	}
	raw, rerr := os.ReadFile(filepath.Join(root, clean, "SKILL.md"))
	if rerr != nil {
		return "", fmt.Errorf("读取技能 %q: %w", clean, rerr)
	}
	return string(raw), nil
}

func splitSkillFrontmatter(content string) (fm string, body string, err error) {
	text := strings.TrimSpace(content)
	if !strings.HasPrefix(text, "---") {
		return "", "", fmt.Errorf("缺少 YAML frontmatter（文件需以 --- 开头）")
	}
	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", fmt.Errorf("frontmatter 未闭合（缺少结束的 ---）")
	}
	return strings.TrimSpace(rest[:end]), strings.TrimSpace(rest[end+4:]), nil
}

func frontmatterValue(fm, key string) string {
	for _, line := range strings.Split(fm, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+":") {
			return strings.TrimSpace(strings.Trim(strings.TrimPrefix(trimmed, key+":"), `"'`))
		}
	}
	return ""
}

// --- trial run -----------------------------------------------------------------------

// BrowserConsoleStep is one editor step, values already parameter-substituted
// by the frontend. Mirrors the step vocabulary the generator emits.
type BrowserConsoleStep struct {
	Type       string   `json:"type"`
	Target     string   `json:"target,omitempty"`
	URL        string   `json:"url,omitempty"`
	Text       string   `json:"text,omitempty"`
	Value      string   `json:"value,omitempty"`
	Direction  string   `json:"direction,omitempty"`
	Amount     int      `json:"amount,omitempty"`
	Condition  string   `json:"condition,omitempty"`
	TimeoutSec int      `json:"timeout_sec,omitempty"`
	Files      []string `json:"files,omitempty"`
	Expression string   `json:"expression,omitempty"`
	Label      string   `json:"label,omitempty"`
}

// BrowserConsoleTrialStatus is one per-step progress event ("browser:trial";
// index -1 marks the run's terminal done/failed).
type BrowserConsoleTrialStatus struct {
	Index  int    `json:"index"`
	Status string `json:"status"` // running|done|failed
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// BrowserConsoleTrialRun executes steps sequentially against the console
// session (opening one when closed), emitting progress and stopping at the
// first failure. Parameter substitution happened frontend-side.
func (a *App) BrowserConsoleTrialRun(steps []BrowserConsoleStep) error {
	if len(steps) == 0 {
		return fmt.Errorf("没有可执行的步骤")
	}
	if state, err := builtin.ConsoleStateOf(); err != nil || !state.Open {
		if _, oerr := builtin.ConsoleOpen("", ""); oerr != nil {
			return fmt.Errorf("打开浏览器会话: %w", oerr)
		}
	}
	emit := func(st BrowserConsoleTrialStatus) {
		if a.ctx != nil {
			wailsruntime.EventsEmit(a.ctx, "browser:trial", st)
		}
	}
	run := func(s BrowserConsoleStep) (string, error) {
		switch s.Type {
		case "navigate":
			return builtin.ConsoleNavigate(s.URL)
		case "click":
			return builtin.ConsoleClick(s.Target)
		case "type":
			return builtin.ConsoleType(s.Target, s.Text)
		case "key":
			return "", builtin.ConsoleKey(s.Value)
		case "scroll":
			return builtin.ConsoleScroll(s.Direction, s.Amount)
		case "select":
			return builtin.ConsoleSelectOption(s.Target, s.Value)
		case "upload":
			return builtin.ConsoleUploadFile(s.Target, s.Files)
		case "wait":
			return builtin.ConsoleWait(s.Condition, s.TimeoutSec)
		case "extract":
			return builtin.ConsoleExtract(s.Target)
		case "screenshot":
			return builtin.ConsoleScreenshot()
		case "evaluate":
			return builtin.ConsoleEvaluate(s.Expression)
		default:
			return "", fmt.Errorf("未知步骤类型 %q", s.Type)
		}
	}
	// Each primitive bounds its own execution window (browserActionTimeout
	// via the kernel), so the loop needs no outer timeout — a slow step
	// simply takes its time and the next one follows.
	for i, s := range steps {
		emit(BrowserConsoleTrialStatus{Index: i, Status: "running"})
		out, err := run(s)
		if err != nil {
			emit(BrowserConsoleTrialStatus{Index: i, Status: "failed", Error: err.Error()})
			emit(BrowserConsoleTrialStatus{Index: -1, Status: "failed", Error: err.Error()})
			return nil // step failure is a result, not a binding error
		}
		if len(out) > 2000 {
			out = out[:2000] + "…"
		}
		emit(BrowserConsoleTrialStatus{Index: i, Status: "done", Output: out})
	}
	emit(BrowserConsoleTrialStatus{Index: -1, Status: "done"})
	return nil
}
