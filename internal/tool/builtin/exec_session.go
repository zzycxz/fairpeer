package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/sandbox"
	"github.com/zzycxz/fairpeer/internal/tool"
)

func init() { tool.RegisterBuiltin(execSessionTool{}) }

const (
	sessionRingCap    = 64 << 10 // 64KB per session output buffer
	maxExecSessions   = 8
	defaultReadWaitMs = 500
	maxReadWaitMs     = 30000
)

// exec_session.go — persistent interactive shell sessions (G5,
// CODEX_GAP_AUDIT；对标 codex unified_exec 的管道形态)。bash 工具是一条命令
// 跑到底收全量输出；本工具补"需要喂 stdin 的长期会话"：python/node REPL、
// ssh 会话、带交互提示的安装器。管道（非 PTY）实现——全屏 TUI 程序不可用，
// 但 REPL 与逐行协议足够；输出存丢弃式环形缓冲，read 游标取增量。

type execSession struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdinMu sync.Mutex // serialises writes WITHOUT holding mu (a blocked write must not wedge read/kill)
	buf     []byte     // accumulated output, front-trimmed when over the ring cap
	dropped int        // bytes trimmed from the front of buf (absolute-offset bookkeeping)
	cursor  int        // absolute offset the reader has consumed up to
	exited  bool
	cancel  context.CancelFunc
	seq     int // spawn order, for deterministic oldest-exited reclaim
}

type execSessionTool struct {
	// workDir, when non-empty, is the directory sessions spawn in (workspace
	// binding, same as bash). Empty = process cwd.
	workDir string
	// sb, when non-zero, wraps the spawned shell in the OS sandbox (same as
	// bash) — exec_session is exec-class and must not be a jail escape hatch.
	sb sandbox.Spec
}

func (execSessionTool) Name() string { return "exec_session" }

func (execSessionTool) Description() string {
	return "Run a persistent interactive shell session and feed its stdin across calls — for REPLs (python/node), long ssh sessions, or installers that ask questions. Actions: spawn (start `command` in a session, returns session_id), write (send `input` to its stdin), read (get output produced since the last read, waiting up to `timeout_ms`), kill (terminate). This is a PIPE session: full-screen TUI programs (vim/top) are not supported — use bash for one-shot commands. Sessions cap at 8; the oldest EXITED session is reclaimed first."
}

func (execSessionTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "action":{"type":"string","enum":["spawn","write","read","kill"],"description":"spawn = start the command in a new session; write = send input to the session's stdin; read = get new output since the last read; kill = terminate the session"},
  "session_id":{"type":"string","description":"session id from spawn (write/read/kill)"},
  "command":{"type":"string","description":"the command line to run (spawn only); runs via the system shell"},
  "input":{"type":"string","description":"text to write to stdin (write); a newline is appended unless input already ends with one"},
  "timeout_ms":{"type":"integer","description":"read: how long to wait for NEW output before returning what's buffered (default 500, max 30000)","minimum":0,"maximum":30000}
},
"required":["action"]
}`)
}

func (execSessionTool) ReadOnly() bool { return false }

type execSessionArgs struct {
	Action    string `json:"action"`
	SessionID string `json:"session_id"`
	Command   string `json:"command"`
	Input     string `json:"input"`
	TimeoutMs int    `json:"timeout_ms"`
	workDir   string // injected from the tool binding, not from args
	sb        sandbox.Spec
}

func (t execSessionTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p execSessionArgs
	p.workDir = t.workDir
	p.sb = t.sb
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	switch p.Action {
	case "spawn":
		return execSessionSpawn(ctx, p)
	case "write":
		return execSessionWrite(p)
	case "read":
		return execSessionRead(ctx, p)
	case "kill":
		return execSessionKill(p)
	default:
		return "", fmt.Errorf("unknown action %q (want spawn|write|read|kill)", p.Action)
	}
}

var (
	sessMu      sync.Mutex
	sessions    = map[string]*execSession{}
	sessCounter = 0
)

func execSessionSpawn(ctx context.Context, p execSessionArgs) (string, error) {
	command := strings.TrimSpace(p.Command)
	if command == "" {
		return "", fmt.Errorf("spawn requires command")
	}
	// Reserve the id/slot in ONE critical section (concurrent spawns must not
	// both pass the cap check); a placeholder session is inserted immediately so
	// the slot is truly reserved, and the real execSession replaces it after
	// Start. Reclaim pick: the LOWEST sequence among EXITED sessions — map
	// iteration is random, so the minimum makes "oldest reclaimed" true.
	sessMu.Lock()
	if len(sessions) >= maxExecSessions {
		reclaimID, oldest := "", -1
		for id, s := range sessions {
			s.mu.Lock()
			exited, seq := s.exited, s.seq
			s.mu.Unlock()
			if exited && (oldest == -1 || seq < oldest) {
				oldest, reclaimID = seq, id
			}
		}
		if reclaimID != "" {
			delete(sessions, reclaimID)
		}
	}
	if len(sessions) >= maxExecSessions {
		sessMu.Unlock()
		return "", fmt.Errorf("session cap (%d) reached — kill or fully read a session first", maxExecSessions)
	}
	sessCounter++
	id := fmt.Sprintf("es%d", sessCounter)
	// exited=true makes the placeholder inert: write/read/kill on this id fail
	// fast instead of dereferencing the nil cmd/stdin before Start replaces it.
	sessions[id] = &execSession{seq: sessCounter, exited: true}
	sessMu.Unlock()

	// OS sandbox wrap — same discipline as bash: an enforce-mode deployment
	// must not be escapable by swapping bash for exec_session. The sandbox
	// package resolves the shell and wraps argv.
	sh := sandbox.ResolveShell()
	argv, _ := sandbox.Command(p.sb, sh, command)
	// Session-scoped context: setKillTree installs cmd.Cancel, which the exec
	// package only honours for CommandContext-created commands; kill() fires it.
	sctx, scancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(sctx, argv[0], argv[1:]...)
	cmd.Dir = p.workDir
	// 会话与 bash 同纪律：Windows 隐藏控制台窗 + 进程树终止（taskkill /T），
	// POSIX 自立进程组（组杀）。必须在 Start 之前设置。
	setKillTree(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		scancel()
		sessMu.Lock()
		delete(sessions, id)
		sessMu.Unlock()
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	stderr, err2 := cmd.StderrPipe()
	if err != nil || err2 != nil {
		scancel()
		sessMu.Lock()
		delete(sessions, id)
		sessMu.Unlock()
		return "", fmt.Errorf("output pipes: %v/%v", err, err2)
	}
	if err := cmd.Start(); err != nil {
		scancel()
		sessMu.Lock()
		delete(sessions, id)
		sessMu.Unlock()
		return "", fmt.Errorf("start: %w", err)
	}

	s := &execSession{cmd: cmd, stdin: stdin, seq: sessCounter, cancel: scancel}
	sessMu.Lock()
	sessions[id] = s
	sessMu.Unlock()

	// Drain both pipes into the session buffer (front-trimmed at the ring cap).
	drain := func(r interface{ Read([]byte) (int, error) }) {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				s.mu.Lock()
				s.buf = append(s.buf, buf[:n]...)
				if cut := len(s.buf) - sessionRingCap; cut > 0 {
					s.buf = s.buf[cut:]
					s.dropped += cut
				}
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}
	var drains sync.WaitGroup
	drains.Add(2)
	go func() { defer drains.Done(); drain(stdout) }()
	go func() { defer drains.Done(); drain(stderr) }()
	// os/exec contract: Wait must not run while pipe reads are in flight —
	// join the drains first, or a fast-exiting command's tail output is lost.
	go func() {
		drains.Wait()
		s.mu.Lock()
		s.exited = true
		s.mu.Unlock()
	}()
	return fmt.Sprintf("session %s spawned (pid %d); write stdin, read for incremental output, kill to end.", id, cmd.Process.Pid), nil
}

func execSessionWrite(p execSessionArgs) (string, error) {
	s, err := sessionByID(p.SessionID)
	if err != nil {
		return "", err
	}
	if p.Input == "" {
		return "", fmt.Errorf("write requires input")
	}
	input := p.Input
	// Line terminators: Windows pipe readers (findstr/more) need CRLF; a bare
	// LF inside a multi-line input is also normalised so every line terminates
	// properly. Trailing "\r" (input already ended CRLF) is not doubled.
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(input, "\n") {
			input += "\r\n"
		} else if !strings.HasSuffix(input, "\r\n") {
			input = strings.ReplaceAll(strings.TrimSuffix(input, "\n"), "\n", "\r\n") + "\r\n"
		}
	} else if !strings.HasSuffix(input, "\n") {
		// POSIX regression guard (review P4-A): a line-fed REPL needs the LF.
		input += "\n"
	}
	// Write under stdinMu, NOT s.mu: a child that stops consuming stdin blocks
	// this write indefinitely — holding s.mu here would wedge read and kill
	// (and thus the whole session) behind it.
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	s.mu.Lock()
	if s.exited {
		s.mu.Unlock()
		return "", fmt.Errorf("session %s has exited", p.SessionID)
	}
	s.mu.Unlock()
	if _, err := s.stdin.Write([]byte(input)); err != nil {
		return "", fmt.Errorf("write to %s stdin: %w", p.SessionID, err)
	}
	return fmt.Sprintf("sent %d bytes to %s stdin", len(input), p.SessionID), nil
}

func execSessionRead(ctx context.Context, p execSessionArgs) (string, error) {
	s, err := sessionByID(p.SessionID)
	if err != nil {
		return "", err
	}
	wait := time.Duration(p.TimeoutMs) * time.Millisecond
	if wait <= 0 {
		wait = defaultReadWaitMs
	}
	if wait > maxReadWaitMs {
		wait = maxReadWaitMs
	}
	deadline := time.Now().Add(wait)
	for {
		s.mu.Lock()
		rel := s.cursor - s.dropped
		if rel < 0 {
			rel = 0 // the referenced bytes were front-trimmed
		}
		if rel < len(s.buf) {
			out := string(s.buf[rel:])
			s.cursor = s.dropped + len(s.buf)
			s.mu.Unlock()
			if s.dropped > 0 && rel == 0 {
				out = fmt.Sprintf("[... %d bytes of older output trimmed ...]\n", s.dropped) + out
			}
			return out, nil
		}
		exited := s.exited
		s.mu.Unlock()
		if exited {
			return fmt.Sprintf("session %s has exited (no further output).", p.SessionID), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return "(no new output yet)", nil
		}
	}
}

func execSessionKill(p execSessionArgs) (string, error) {
	s, err := sessionByID(p.SessionID)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if !exited {
		// 先关 stdin（子进程得到 EOF），再整树终止——cmd /c 的孙子进程
		// （python/ssh）否则会抱着继承的管道句柄存活成孤儿。
		_ = s.stdin.Close()
		if s.cmd.Cancel != nil {
			_ = s.cmd.Cancel() // setKillTree 装的整树终止（Windows taskkill /T；POSIX 组杀）
		}
		_ = s.cmd.Process.Kill()
	}
	sessMu.Lock()
	delete(sessions, p.SessionID)
	sessMu.Unlock()
	return fmt.Sprintf("session %s killed and removed.", p.SessionID), nil
}

func sessionByID(id string) (*execSession, error) {
	sessMu.Lock()
	defer sessMu.Unlock()
	s, ok := sessions[id]
	if !ok {
		return nil, fmt.Errorf("no session %q — spawn one first", id)
	}
	return s, nil
}
