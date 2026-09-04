# 元素提取方案全面改造 (2026-09-03)

## 背景

深信服安全感知平台（Sangfor SIP）基于 ExtJS (Sencha) 框架，fairpeer 运维界面的「元素」页卡无法准确提取页面上的交互元素。

### 问题现象

用户在运维界面右侧栏的浏览器「元素」页卡中，无法看到以下关键元素：

```html
<!-- 时间范围选择器 — 完全缺失 -->
<input id="timeRangeField-3482-inputEl" type="text" name="time_range"
  value="2026-09-02 00:00:00 - 2026-09-03 23:59:59" readonly
  class="x-form-field x-form-text x-trigger-noedit">
```

### 根因分析

对 `C:\Users\13852\Desktop\1.txt`（完整页面 HTML）的审查发现四层问题：

1. **ExtJS 动态 ID**：`timeRangeField-3482-inputEl` 中的 `3482` 每次渲染变化，`ext-gen*`、`widget-*-\d+` 同理
2. **框架 CSS 类共享**：`x-form-field x-form-text x-trigger-noedit` 被数百个元素共用，无法唯一标识
3. **AX 树盲区**：ExtJS 深层 div 嵌套包装导致 Chrome 无障碍树漏掉被包装的 input
4. **CSS-in-JS 哈希类名**：`css-*`、`emotion-*`、`jss-*`、`styled-*`、`sc-*` 跨渲染失效

### 审查范围

8 层场景全覆盖审查：

| 层 | 场景 | 典型框架 |
|----|------|----------|
| 1 | ExtJS/Dojo 动态 ID | Sencha, Dojo, jQuery-UI |
| 2 | CSS-in-JS 哈希类名 | emotion, styled-components, JSS |
| 3 | Shadow DOM | Web Components, Lit, Stencil |
| 4 | 动态 role/aria 属性 | ARIA 不完整的组件库 |
| 5 | Vue/React 组件库 div 按钮 | Naive-UI, MUI, Element-Plus |
| 6 | iframe 嵌套 | 微前端, 嵌入式页面 |
| 7 | 虚拟滚动 | 大数据表格 |
| 8 | 框架特殊输入 | readonly 选择器, contenteditable |

---

## 改造总览

涉及 **6 个文件**，覆盖元素发现、选择器生成、内容提取、录制器四条链路。

---

## 一、选择器梯子（通用方案）

所有选择器生成共用同一套 8-9 级梯子，按优先级降序：

| 级别 | 策略 | 示例 | 说明 |
|------|------|------|------|
| 1 | 稳定 ID | `#loginForm` | 过滤动态 ID |
| 2 | `name` 属性 | `input[name="time_range"]` | 表单字段最稳定锚点，**不要求唯一** |
| 3 | data-testid / data-ref | `[data-testid="submit"]` | 测试框架 |
| 4 | aria-label | `[aria-label="搜索"]` | 无障碍标签 |
| 5 | ExtJS 稳定前缀 | `[id^="timeRangeField"][type="text"]` | 去数字后缀 |
| 6 | 唯一 class | `input.x-form-field` | 跳过 CSS-in-JS 哈希类名 |
| 7 | `text=` 锚点 | `text=查询` | 可见文字，**不用 name 属性** |
| 8 | 祖先作用域 | `#stableParent input[type="text"]` | 稳定祖先 + tag（仅 axcss） |
| 9 | 结构路径 | `div:nth-of-type(3) > input:nth-of-type(1)` | 兜底 |

### 动态 ID 过滤器 (`isDynId`)

三处使用（domscan、axcss、recorder），逻辑完全一致：

```javascript
function isDynId(id) {
  if (!id) return false;
  if (/^ext-gen\d+$/.test(id)) return true;           // ext-gen3482
  if (/^widget-[a-z]+-\d+/i.test(id)) return true;    // widget-datefield-123
  if (/-(\d{3,})(?:-|$)/.test(id)                      // -3482- 后缀
      && !/^[a-z]+-[a-z]/i.test(id)) return true;      // 排除 kebab-case
  return false;
}
```

### CSS-in-JS 哈希类名过滤器

```javascript
var HASH_CLASS_RE = /^(css|emotion|jss|styled|sc)-[a-z0-9]{4,}$/i;
```

---

## 二、两条互补发现路径 + 去重

### 路径 1 — AX 树（无障碍树）

- Chrome `accessibility.GetFullAXTree()` 发现元素（已原生穿透 Shadow DOM）
- 按 `accessible name` 匹配 DOM 元素 → 选择器梯子
- 产出：`{Ref: "e5", Role: "textbox", Name: "搜索", CSS: "input[name=query_string]"}`

### 路径 2 — DOM 扫描（启发式）

- `querySelectorAll` 宽泛候选 → 过滤 → 选择器梯子
- 补充 AX 树盲区：无 role 的 div 按钮、被 ExtJS 包装漏掉的 input
- 产出：`{Ref: "input[name=query_string]", Role: "input", Name: "搜索"}`

### 去重合并

两条路径可能发现同一元素，合并策略避免重复：

```
AX 树结果集 (有 ref + 可能有 CSS)
         ↓ 计算 CSS
    axCSS = {所有已有 CSS 选择器}
         ↓
DOM 扫描结果集 (有 CSS 选择器)
         ↓ 逐条比对
    ┌─ CSS ∈ axCSS → 丢弃（重复）
    ├─ CSS ∉ axCSS 但 AX 有同名无 CSS 条目 → 合并 CSS 到 AX 条目
    └─ 全新元素 → 追加
```

---

## 三、修改的文件清单

### 1. `browserconsole_domscan.go` — DOM 级可点击候选扫描器

**作用**：补充 AX 树盲区——Vue/React 组件库渲染的 div/span 按钮对 AX 树不可见。

**改动**：
- **不再跳过表单元素**：旧代码 `if (tag === 'a' || tag === 'button' || tag === 'input' || tag === 'select' || tag === 'textarea') continue` 跳过了所有表单标签，ExtJS 包装的 input 因此被漏掉。新代码只跳过 `a` 和 `button`
- **不再跳过带按钮类的 `<a>`/`<button>`**：ExtJS 按钮用 `<a class="x-btn">` 渲染，旧代码无条件跳过所有 `<a>`。新代码保留带 `btn`/`button` 类或 `onclick`/`tabindex` 的元素
- **`isDynId()` 动态 ID 过滤**：选择器梯子的每一步都过滤动态 ID
- **`HASH_CLASS_RE` 哈希类名过滤**：跳过 CSS-in-JS 生成的哈希类名
- **`nameOf()` 修正**：不返回 `name` 属性（`name` 只用于 CSS 选择器，`text=` 锚点只用用户可见文字：aria-label / placeholder / title / innerText）
- **`selectorFor()` 完整 8 级梯子**：稳定 ID → name → data-testid → aria-label → ExtJS 前缀 → 唯一 class → text= → 结构路径
- **`name` 不要求唯一**：`input[name="time_range"]` 即使匹配多个元素也直接返回（`querySelector` 取第一个，语义上正确）
- **表单元素绕过 hint+pointer 门控**：`isForm = true` 时直接进入选择器生成，不要求 `onclick` 或 `cursor: pointer`
- **Shadow DOM 穿透**：`collect()` 递归遍历 `shadowRoot`
- **`name+type`/`name+readonly` 组合**：先试 `input[name="time_range"][type="text"]` 提升唯一性

**关键代码变更**：

```javascript
// ===== 旧：跳过所有 a/button/input/select/textarea =====
if (tag === 'a' || tag === 'button' || tag === 'input' || tag === 'select' || tag === 'textarea') continue;

// ===== 新：只跳过标准 a/button，保留表单和 ExtJS 按钮 =====
if (tag === 'a' || tag === 'button') continue;  // 仅跳过 a/button
// 后续改为：
if ((tag === 'a' || tag === 'button')
  && !/btn|button/i.test(el.getAttribute('class') || '')
  && !el.hasAttribute('onclick') && !el.hasAttribute('tabindex')) continue;
```

```javascript
// ===== 旧：name 属性用于 text= 锚点 =====
function nameOf(el) {
  if (tag === 'input' || tag === 'select' || tag === 'textarea') {
    var nm = el.getAttribute('name');
    if (nm) return nm;  // ← "query_string" 不是可见文字！
  }
  ...
}

// ===== 新：name 属性只用于 CSS 选择器 =====
function nameOf(el) {
  return (el.getAttribute('aria-label') || el.getAttribute('alt')
    || el.getAttribute('placeholder') || el.getAttribute('title')
    || (el.innerText || '')).trim().replace(/\s+/g, ' ').slice(0, 80);
}
```

```javascript
// ===== 旧：name 要求唯一 =====
var byName = el.tagName.toLowerCase() + '[name="' + nm + '"]';
if (unique(byName)) return byName;  // 不唯一就跳过

// ===== 新：先试 name+type 组合，不唯一也直接用 =====
if (nm) {
  var byNameType = tag + '[name="' + nm + '"]';
  var tp = el.getAttribute('type');
  if (tp) byNameType += '[type="' + tp + '"]';
  if (unique(byNameType)) return byNameType;
  if (el.hasAttribute('readonly')) {
    var byNameRo = tag + '[name="' + nm + '"][readonly]';
    if (unique(byNameRo)) return byNameRo;
  }
  var byName = tag + '[name="' + nm + '"]';
  if (unique(byName)) return byName;
  return byName;  // 不唯一也用（querySelector 取第一个）
}
```

---

### 2. `browserconsole_axcss.go` — AX 行 CSS 计算器

**作用**：为 AX 树发现的元素计算稳定 CSS 选择器，使录制的技能可跨会话复用。

**改动**：
- 同样的 `isDynId`、`HASH_CLASS_RE`、`nameOf` 修正
- `selectorFor()` 9 级梯子（比 domscan 多了祖先作用域级）
- `name` 不要求唯一，先试 `name+type`/`name+readonly` 组合
- Shadow DOM 穿透（`collectPool()` 递归 shadow root）
- **空 name 兜底匹配**：当 AX 行 `name` 为空时（ExtJS input 无 aria-label），用 `value` 匹配表单元素

```javascript
// AX 树报 name="" 时，用 value 匹配
if (!hit && row.value && row.role
    && (row.role === 'textbox' || row.role === 'searchbox'
        || row.role === 'combobox' || row.role === 'spinbutton')) {
  for (var m = 0; m < pool.length; m++) {
    var el = pool[m];
    var tg = el.tagName.toLowerCase();
    if (tg !== 'input' && tg !== 'textarea' && tg !== 'select') continue;
    if (isUsed(el)) continue;
    if ((el.value || '').trim() === row.value.trim()) { hit = el; break; }
  }
}
```

---

### 3. `browserconsole.go` — 运维控制台核心（录制器 + 元素列表）

**改动**：

#### 3a. 录制器 `sel()` 函数

注入页面的录制脚本中，`sel()` 负责为用户交互的元素生成选择器。

- 新增 `isDynId()` 过滤动态 ID
- `name` 属性优先于 `data-testid`（表单字段最稳定锚点）
- 新增 ExtJS 稳定前缀回退（`[id^="timeRangeField"][type="text"]`）
- 结构路径中的祖先 ID 也过滤动态 ID

```javascript
const sel = (el) => {
  if (el.id && !isDynId(el.id)) return "#" + CSS.escape(el.id);
  const nm = el.getAttribute("name");
  if (nm) return el.tagName.toLowerCase() + '[name="' + nm + '"]';
  // ... data-testid, aria-label, ExtJS prefix, structural path
};
```

#### 3b. `axRow` 结构体增加 `Value` 字段

将 AX 树的 `value`（input 当前值）传入 CSS 匹配器，用于空 name 时的兜底匹配。

```go
type axRow struct {
  Ref   string `json:"ref"`
  Role  string `json:"role,omitempty"`
  Name  string `json:"name,omitempty"`
  Value string `json:"value,omitempty"`  // 新增
}
```

#### 3c. AX 树与 DOM 扫描去重

```go
axCSS := make(map[string]bool, len(out))
for _, el := range out {
    if el.CSS != "" { axCSS[el.CSS] = true }
}
for _, dom := range domEls {
    if dom.Ref != "" && axCSS[dom.Ref] {
        continue // 重复：AX 已有此选择器
    }
    if dom.Ref != "" {
        merged := false
        for i := range out {
            if out[i].CSS == "" && out[i].Name != "" && out[i].Name == dom.Name {
                out[i].CSS = dom.Ref  // AX 有元素但无 CSS → 合并
                merged = true; break
            }
        }
        if merged { continue }
    }
    out = append(out, dom)  // 全新元素
}
```

---

### 4. `browser.go` — 浏览器工具（表格提取 + readonly 输入）

#### 4a. 表格提取 Shadow DOM 穿透

```javascript
function collectTables(r) {
  var tables = Array.from(r.querySelectorAll('table'));
  r.querySelectorAll('*').forEach(function(el){
    if (el.shadowRoot) tables = tables.concat(collectTables(el.shadowRoot));
  });
  return tables;
}
var tables = collectTables(root);  // 替代原来的 root.querySelectorAll('table')
```

#### 4b. readonly 输入框直接设值

ExtJS 的 readonly input（日期选择器等）使用原型 setter 会报 "Illegal invocation"。修复：readonly 时跳过原型 setter，直接 `.value` + `change` 事件。

```javascript
// typeRefJSBody 中新增 readonly 分支
if (this.hasAttribute && this.hasAttribute('readonly')) {
  this.value = text;
  this.dispatchEvent(new Event('change', {bubbles: true}));
  return JSON.stringify({value: this.value, expectFormatChange: true});
}
// 原有的 React/Vue 原型 setter 逻辑不变
var valueProto = this.tagName === 'TEXTAREA'
  ? window.HTMLTextAreaElement.prototype
  : window.HTMLInputElement.prototype;
var setter = Object.getOwnPropertyDescriptor(valueProto, 'value');
if (setter && setter.set) { setter.set.call(this, text); }
else { this.value = text; }
```

---

### 5. `browsermarkdown.go` — Markdown 内容提取

**改动**：Shadow DOM 穿透，使 Web Components 内部的内容也能被提取为 Markdown。

```javascript
// 根选择器穿透 shadow root
function queryDeep(sel) {
  var found = document.querySelector(sel);
  if (found) return found;
  var all = document.querySelectorAll('*');
  for (var i = 0; i < all.length; i++) {
    var n = all[i];
    if (!found && n.shadowRoot) {
      try { found = n.shadowRoot.querySelector(sel); } catch(e) {}
    }
  }
  return found;
}

// 子节点包含 shadow root 内容
function childNodesDeep(node) {
  var nodes = Array.from(node.childNodes);
  if (node.shadowRoot) nodes = nodes.concat(Array.from(node.shadowRoot.childNodes));
  return nodes;
}
// inline() 和 block() 均从 node.childNodes 改为 childNodesDeep(node)
```

---

### 6. `browsersnapshot.go`（只读审查）

- 确认 `captureAXTree()` 使用 `accessibility.GetFullAXTree()` 已原生穿透 Shadow DOM
- `axInteractiveRoles` 包含 `textarea`（Naive-UI 等用 textarea 构建 input）
- 无需修改

---

## 四、深信服页面元素提取验证

| 元素 | HTML | 选择器 | 梯子级别 | 来源路径 |
|------|------|--------|----------|----------|
| 搜索框 | `<input name="query_string" placeholder="支持关键词...">` | `input[name="query_string"]` | L2 name | DOM 扫描 |
| 查询按钮 | `<a class="x-btn" role="button" id="button-2170">查询</a>` | `text=查询` | L7 text= | DOM 扫描 |
| 导出按钮 | `<a class="x-btn" role="button" id="button-2070">导出</a>` | `text=导出` | L7 text= | DOM 扫描 |
| 时间范围 | `<input name="time_range" readonly value="2026-09-02...">` | `input[name="time_range"]` | L2 name | DOM 扫描 |
| 时间范围输入 | 同上（readonly，需直接设值） | 同上 | — | type 时 readonly 分支 |

---

## 五、编译

```bash
cd fairpeer/desktop
/c/Users/13852/go/bin/wails.exe build
# Built 'fairpeer\desktop\build\bin\fairpeer.exe' in ~60s
# 产物：build\bin\fairpeer.exe (~96MB)
```

---

## 六、调试过程中发现并修复的额外问题

| 问题 | 现象 | 根因 | 修复 |
|------|------|------|------|
| `text=query_string` 失败 | 搜索框选择器报错 | `nameOf` 返回 `name` 属性值，不是可见文字 | `nameOf` 不再返回 `name`，只用于 CSS |
| readonly 输入 "Illegal invocation" | 时间范围框输入报错 | ExtJS readonly input 原型链被修改，native setter 验证失败 | readonly 时跳过原型 setter，直接 `.value` |
| AX/DOM 重复元素 | 同一元素出现两条 | 两条路径独立输出无去重 | `ConsoleElements` 中合并去重 |
| `<a>` 标签按钮消失 | 查询/导出按钮不可见 | `scanDomCandidates` 无条件跳过所有 `<a>` | 保留带 `btn` 类的 `<a>` |
| AX 空 name 无法匹配 | readonly input 无 CSS | AX 树报 `name=""`，匹配器跳过 | value 兜底匹配 |

---

## 七、已知限制（TODO）

- **iframe 内元素**：当前不穿透 iframe，需后续支持
- **虚拟滚动**：视口外被移除的 DOM 元素无法扫描
- **`text=` 锚点歧义**：页面上有多个相同可见文字的元素时，`text=` 匹配第一个
- **`TestConsoleElementsInteractiveOnly` 测试**：`text=` 锚点不是有效 CSS，`querySelectorAll` 会抛 `SyntaxError`，测试需要适配

---

## 八、改动文件索引

| 文件 | 行数变化 | 核心改动 |
|------|----------|----------|
| `internal/tool/builtin/browserconsole_domscan.go` | 重写 `domCandidateJS` | 不跳过表单 + isDynId + nameOf + 选择器梯子 + Shadow DOM |
| `internal/tool/builtin/browserconsole_axcss.go` | 重写 `axRowCSSJS` | 同上 + 空 name value 兜底 + 祖先作用域级 |
| `internal/tool/builtin/browserconsole.go` | 录制器 + 去重 | sel() isDynId + axRow.Value + AX/DOM 去重 |
| `internal/tool/builtin/browser.go` | 表格 + readonly | collectTables Shadow DOM + typeRefJSBody readonly 分支 |
| `internal/tool/builtin/browsermarkdown.go` | Markdown | queryDeep + childNodesDeep Shadow DOM |
| `internal/tool/builtin/browsersnapshot.go` | 无修改 | 审查确认 AX 树已穿透 Shadow DOM |
