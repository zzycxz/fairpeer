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
	buf     []byte // accumulated output, front-trimmed when over the ring cap
	dropped int    // bytes trimmed from the front of buf (absolute-offset bookkeeping)
	cursor  int    // absolute offset the reader has consumed up to
	exited  bool
}

type execSessionTool struct{}

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
}

func (execSessionTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p execSessionArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	switch p.Action {
	case "spawn":
		return execSessionSpawn(ctx, p)
	case "write":
		return execSessionWrite(p)
	case "read":
		return execSessionRead(p)
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
	sessMu.Lock()
	// Reclaim the oldest EXITED session at capacity — a live session is never
	// reclaimed (its output may be unread).
	if len(sessions) >= maxExecSessions {
		for id, s := range sessions {
			s.mu.Lock()
			exited := s.exited
			s.mu.Unlock()
			if exited {
				delete(sessions, id)
				break
			}
		}
	}
	if len(sessions) >= maxExecSessions {
		sessMu.Unlock()
		return "", fmt.Errorf("session cap (%d) reached — kill or fully read a session first", maxExecSessions)
	}
	sessCounter++
	id := fmt.Sprintf("es%d", sessCounter)
	sessMu.Unlock()

	shell, flag := "sh", "-c"
	if runtime.GOOS == "windows" {
		shell, flag = "cmd", "/c"
	}
	cmd := exec.Command(shell, flag, command)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	stderr, err2 := cmd.StderrPipe()
	if err != nil || err2 != nil {
		return "", fmt.Errorf("output pipes: %v/%v", err, err2)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}

	s := &execSession{cmd: cmd, stdin: stdin}
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
	go drain(stdout)
	go drain(stderr)
	go func() {
		_ = cmd.Wait()
		s.mu.Lock()
		s.exited = true
		s.mu.Unlock()
	}()
	return fmt.Sprintf("session %s spawned (pid %d). write = 喂 stdin，read = 取增量输出，kill = 结束。", id, cmd.Process.Pid), nil
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
	if !strings.HasSuffix(input, "\n") {
		// Windows pipe readers (findstr/more) treat a bare LF as an incomplete
		// line — CRLF is the line terminator there.
		if runtime.GOOS == "windows" {
			input += "\r\n"
		} else {
			input += "\n"
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited {
		return "", fmt.Errorf("session %s has exited", p.SessionID)
	}
	if _, err := s.stdin.Write([]byte(input)); err != nil {
		return "", fmt.Errorf("write to %s stdin: %w", p.SessionID, err)
	}
	return fmt.Sprintf("sent %d bytes to %s stdin", len(input), p.SessionID), nil
}

func execSessionRead(p execSessionArgs) (string, error) {
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
			return out, nil
		}
		exited := s.exited
		s.mu.Unlock()
		if exited {
			return fmt.Sprintf("session %s has exited (no further output).", p.SessionID), nil
		}
		if time.Now().After(deadline) {
			return "(no new output yet)", nil
		}
		time.Sleep(50 * time.Millisecond)
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
