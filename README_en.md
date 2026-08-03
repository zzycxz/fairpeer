<p align="center">
  <img src="docs/logo.png" alt="fairpeer" width="640"/>
</p>

<p align="center">
  <strong>English</strong>
  &nbsp;·&nbsp;
  <a href="./README.md">简体中文</a>
  &nbsp;·&nbsp;
  <a href="./docs/GUIDE.md">Guide</a>
  &nbsp;·&nbsp;
  <a href="./docs/SPEC.md">Spec</a>
</p>

<p align="center">
  <a href="https://github.com/zzycxz/fairpeer/releases"><img src="https://img.shields.io/badge/version-v0.5.6-0153e5?style=flat-square" alt="Version 0.5.6"/></a>
  <a href="https://github.com/zzycxz/fairpeer/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/zzycxz/fairpeer/ci.yml?style=flat-square&label=ci&labelColor=161b22&logo=githubactions&logoColor=white" alt="CI"/></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/zzycxz/fairpeer.svg?style=flat-square&color=8b949e&labelColor=161b22" alt="license"/></a>
  <a href="https://github.com/zzycxz/fairpeer/stargazers"><img src="https://img.shields.io/github/stars/zzycxz/fairpeer.svg?style=flat-square&color=dbab09&labelColor=161b22&logo=github&logoColor=white" alt="GitHub stars"/></a>
</p>

<br/>

<h3 align="center">A universal, full-scenario AI coding agent.</h3>
<p align="center">
  Connects to any OpenAI / Anthropic compatible model endpoint (Qwen, GLM, DeepSeek, Kimi, Doubao, GPT, Claude, and 300+ others).<br/>
  A single static Go binary. Zero runtime dependencies. Cross-platform distribution.
</p>

<br/>

## What is fairpeer?

fairpeer is a universal AI coding agent designed to work with any standard model endpoint. Driven by a highly configurable core and Model Context Protocol (MCP) plugins, fairpeer integrates with mainstream LLMs (DeepSeek, Qwen, GLM, Kimi, GPT, Claude, and 300+ others) to provide autonomous, natural-language-driven programming capabilities.

The agent can run in your **terminal** (TUI), as a **native desktop app** (Wails), as an **HTTP/SSE server**, or as a **multi-channel IM bot** (WeCom / Feishu / QQ) — all powered by a single, high-performance, transport-agnostic engine.

> **Open Source Attribution:** This project is derived from [DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix),
> deeply optimized and architecturally expanded for a general-purpose, multi-model workflow and enterprise scenarios.

## Core Features

### Architecture & Ecosystem

- **Universal Provider Architecture** — A unified abstraction that connects to any OpenAI / Anthropic compatible endpoint: thinking mode protocol, reasoning_content round-trip, 11 direct vendors + 7 aggregators (Coding Plan). Fully configuration-driven via `fairpeer.toml`.
- **MCP Plugin Ecosystem** — Full support for Model Context Protocol (MCP). External tools run as subprocesses over stdio / HTTP, providing infinite extensibility.
- **Built-in Web Search** — Integrated Brave → Exa → Linkup three-engine fallback chain search, no external MCP needed.
- **Zero-Friction Distribution** — `CGO_ENABLED=0` single binary. Cross-compiled for 6 major OS/Architecture targets.

### Built-in Intelligent Tools

Native integration of a streamlined IDE-grade toolchain: `bash` · `read_file` (doubles as directory listing) · `write_file` · `edit_file` · `grep` (with timeout) ·
`web_fetch` · `web_search` · `todo_write` · `complete_step` ·
`codegraph_*` (Tree-sitter based project-wide symbol and call-graph semantic search).
Domain capabilities (browser/desktop automation, email, knowledge base, documents, scheduling, expert teams) are packaged as subagent skills, invoked on demand via `run_skill`.

### Exclusive Code Intelligence

- **CodeGraph Engine** — A lightweight, local code graph built on Tree-sitter + SQLite. Zero API cost, background silent indexing, enabling precise method invocation and symbol tracking.
- **Full-Stack LSP Integration** — Deep binding with mainstream language servers for diagnostics, go-to-definition, and cross-references.

### Autonomous Intelligence & Self-Evolution

- **Goal Independent Judge** — Goal completion evaluation by a separate LLM (based on transcript evidence, temperature=0), preventing premature optimistic stops.
- **Max Mode (Best-of-N)** — N parallel candidate reasoning + independent judge selection. Ideal for complex architecture design and hard bugs, significantly improving reasoning quality.
- **Dream / Distill Self-Evolution** — Dream (7-day cycle) auto-consolidates session knowledge into project memory; Distill (30-day cycle) auto-discovers repeated workflows and packages them as reusable Skills.
- **Memory FTS5 Full-Text Search** — SQLite FTS5 + BM25 ranked memory search. Retrieves by relevance instead of injecting all memories, with token cost growing linearly with memory count.
- **Memory Archive Soft Delete** — Deleted memories are moved to `.archive/` directory, traceable and recoverable, never permanently lost.
- **GlobalDir Cross-Project Memory** — User preferences and feedback memories are shared across all projects, preserving accumulated knowledge when switching contexts.

### Safety & Reliability

- **Subject-based Permission Evaluation** — Writer tools (`write_file`/`edit_file`) and irreversible operations (`email_send`/`rag_delete`) have their subject paths glob-matched for approval; deny rules cannot be bypassed.
- **Checkpoint Path Traversal Protection** — `safePath` uses `filepath.IsLocal` to explicitly reject `..`, UNC paths, and other escape vectors.
- **Memory Store Path Protection** — `safeJoin` prevents path traversal attacks via the `remember` tool.
- **Summarizer Timeout Protection** — 90-second timeout prevents LLM stream stalls from permanently blocking compaction.
- **Transient 401 Retry** — Automatic retry on transient gateway authentication failures, reducing spurious session interruptions.
- **Checkpoints & Rewind** — Snapshot-based safety net for code modifications. Supports `/rewind` for instant undo, providing maximum fault tolerance.

### Plan-Driven Mode

- **Plan Mode** — Automatically intercepts high-risk operations. The Agent must submit an "execution plan" and wait for human sign-off before modifying files or executing sensitive shell commands.
- **Evidence-Backed Completion** — Every plan step must cite evidence (verification command, diff, file paths), preventing the agent from claiming completion without actual output.
- **PlanModeFromContext** — Tools can introspect whether they are running under plan mode, conditionally disabling writer-only surfaces.

## Frontend Channels

| Frontend | Command | Description |
|------|------|------|
| **Terminal TUI** | `fairpeer chat` | For geeks: Immersive terminal UI (Charm Bubble Tea) |
| **API Server** | `fairpeer serve` | Open capabilities: Standard HTTP/SSE programmatic interface |
| **Desktop App** | Wails Launcher | UI interaction: Native macOS / Windows / Linux multi-tab experience |
| **Enterprise Bot** | `fairpeer bot start` | Team collaboration: WeCom / Feishu / QQ IM gateway integration |
| **ACP Server** | `fairpeer acp` | Protocol bridge: Agent Control Protocol remote execution layer |

## Install

Current Release: **v0.5.6**

```sh
npm i -g fairpeer                        # Any OS — pulls the prebuilt native binary
brew install zzycxz/fairpeer/fairpeer    # macOS users
```

You can also download prebuilt archives (`darwin|linux|windows × amd64|arm64`) directly from [GitHub Releases](https://github.com/zzycxz/fairpeer/releases).

> **⚠️ macOS Desktop Installation Guide:**
> If you downloaded the `.zip` archive for the macOS desktop app, because this is an open-source project without Apple developer signing, extracting and running the app might trigger an **"App is damaged and can't be opened"** warning.
>
> **Solution:** Open your terminal and run the following command to remove the quarantine attribute (assuming the app is in your Downloads folder):
> ```sh
> xattr -cr ~/Downloads/fairpeer.app
> ```
> After running this, you can double-click and open the app normally.

### Build from source

```sh
make build    # Output to bin/ directory
make cross    # Cross-compile for 6 target platforms to dist/
```
*(Requires Go 1.25+)*

## Quick Start & Configuration

```sh
fairpeer setup                       # Configuration wizard → generates ./fairpeer.toml
export DEEPSEEK_API_KEY=your-key     # Set your model API key (or add to .env)
fairpeer chat                        # Enter the interactive TUI, type /init to generate project context
fairpeer run "implement all TODOs in main.go"
fairpeer run --model deepseek/deepseek-v4-flash "add unit tests"
echo "explain this code block" | fairpeer run
```

## Connecting a Model Provider

fairpeer is not tied to any model platform: a unified Provider abstraction connects to any OpenAI / Anthropic compatible endpoint. Full templates are in [`fairpeer.example.toml`](./fairpeer.example.toml) (11 direct vendors + 7 aggregators).

### Step 1: Obtain an API Key
Using DeepSeek as an example (other vendors follow the same flow): register on the vendor's platform and create an API key.

### Step 2: Set the Environment Variable
```sh
# Linux / macOS
export DEEPSEEK_API_KEY="your-real-api-key"

# Windows (PowerShell)
$env:DEEPSEEK_API_KEY = "your-real-api-key"
```

### Step 3: Configure the Provider (`fairpeer.toml`)
Create or modify `fairpeer.toml` in your project root:

```toml
default_model = "deepseek"

[[providers]]
name        = "deepseek"
kind        = "openai"
base_url    = "https://api.deepseek.com"
api_key_env = "DEEPSEEK_API_KEY"
default     = "deepseek-v4-pro"
fast_model  = "deepseek-v4-flash"
models      = ["deepseek-v4-pro", "deepseek-v4-flash"]
```

Once configured, simply run `fairpeer chat` to experience intelligent programming powered by your model of choice.

> **💡 Pro Tip: Customizing AI Identity & Rules**
> If you want the AI to better understand your team's development standards, you can create or modify `fairpeer.md` in your project root to write down your specific rules and identity declarations. The AI will automatically read and follow these instructions in every conversation.

### Recommended Models

In the `model` field, you can flexibly switch using the `provider/vendor/model-name` format. Recommended direct vendors:

| Model ID | Core Advantage | Best For |
|---------|------|------|
| `deepseek/deepseek-v4-pro` | Strong capability, native multimodal | Core architecture design, complex analysis, primary coding |
| `qwen/qwen3.7-max` | 1M context, thinking support | Long-context analysis, complex architecture design |
| `zhipu/glm-5.2` | Open-source SOTA, 1M context | General coding, cross-module refactoring |
| `deepseek/deepseek-v4-flash` | Extremely fast response, code-specialized | Code snippet completion, quick refactoring, unit tests |

> fairpeer supports 18 vendors and 300+ models. See the full Provider templates in [`fairpeer.example.toml`](./fairpeer.example.toml) (Qwen, Zhipu, Volcengine, MiniMax, Kimi, Anthropic, OpenAI, and more). Switch models seamlessly by changing the `model` field — zero code changes required.

## Documentation Reference

| Document | Contents |
|------|------|
| **[Guide](./docs/GUIDE.md)** | Permissions, sandbox execution, MCP plugins, slash commands, `@` refs, plan mode, background model |
| **[Specification](./docs/SPEC.md)** | Engineering contract: architecture, registry mechanism, data types, and roadmap |
| **[Checkpoints](./docs/CHECKPOINTS.md)** | Snapshot-based safety net for code modifications |
| **[Session Architecture](./docs/SESSION_REFERENCE_ARCHITECTURE.md)** | Session lifecycle management, persistence, and seamless resumption |
| **[Contributing](./CONTRIBUTING.md)** | Developer guide: Adding new tools, Providers, and custom bot channels |
| **[Changelog](./CHANGELOG.md)** | Historical release records and feature iterations |

## Core Architecture

```
Developer → CLI / Desktop / HTTP / Bot / ACP
             ↓
             control.Controller  (Transport-agnostic session driver)
             ↓
             agent.Agent         (ReAct loop core: think stream → tool dispatch → parse → …)
             ↓
             provider.Provider   (Connects to any OpenAI/Anthropic compatible LLM)
             tool.Registry       (Execution sandbox: Native tools + MCP external plugins)
```

The project contains over 40 strictly decoupled internal packages. The dependency graph follows a strict, acyclic, one-way flow:
`cli → {agent, plugin, config} → {tool, provider}`

---

<p align="center">
  <sub>MIT License — see the <a href="./LICENSE">LICENSE</a> file for details.</sub>
</p>
