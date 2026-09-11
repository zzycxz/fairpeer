//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

// pty_unix.go — unix pseudo-terminal backend for the integrated terminal,
// the counterpart of the ConPTY implementation on Windows (upgrade spec 3-4).
// A real kernel pty (via creack/pty) is allocated per session and the shell
// is spawned attached to it, so ANSI escapes, cursor addressing, colors and
// interactive programs (vim, ssh, node REPL) behave exactly as on a real
// terminal. The Wails binding surface is identical across platforms.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// ptySession is one live pty master + its attached child process.
type ptySession struct {
	mu         sync.Mutex
	tty        *os.File // master end: we read child output / write stdin here
	cmd        *exec.Cmd
	closed     bool
	reaped     bool // child exited AND was reaped (zombie check for alive())
	cols, rows int
}

// defaultShell resolves the interactive shell: $SHELL first, then the
// standard fallbacks.
func defaultShell() (string, error) {
	if s := strings.TrimSpace(os.Getenv("SHELL")); s != "" {
		if _, err := os.Stat(s); err == nil {
			return s, nil
		}
	}
	for _, c := range []string{"/bin/bash", "/bin/zsh", "/bin/sh"} {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("no shell found (set $SHELL)")
}

// newPTY spawns the shell (or the given argv for remote tabs) attached to a
// fresh pty of the requested size.
func newPTY(cols, rows int, name string, args []string) (*ptySession, error) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 30
	}
	if name == "" {
		shell, err := defaultShell()
		if err != nil {
			return nil, err
		}
		name, args = shell, nil
	}
	cmd := exec.Command(name, args...)
	// A pty child must look like it runs on a real terminal: colors,
	// alt-screen and readline all key off TERM. getenv returns the FIRST
	// match, so an inherited TERM must be stripped before appending ours.
	env := os.Environ()
	termSet := false
	for i, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			env[i] = "TERM=xterm-256color"
			termSet = true
			break
		}
	}
	if !termSet {
		env = append(env, "TERM=xterm-256color")
	}
	cmd.Env = env
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, fmt.Errorf("pty start %s: %w", name, err)
	}
	return &ptySession{tty: tty, cmd: cmd, cols: cols, rows: rows}, nil
}

// write sends bytes to the child's stdin. The tty handle is captured under
// the lock but the write runs WITHOUT it: a child that stops reading stdin
// would otherwise pin s.mu and wedge PTYKill/close (and every other call for
// this session) forever. Concurrent Write/Close on an *os.File is safe; a
// write after close errors harmlessly.
func (s *ptySession) write(b []byte) error {
	s.mu.Lock()
	tty := s.tty
	closed := s.closed
	s.mu.Unlock()
	if closed || tty == nil {
		return fmt.Errorf("pty closed")
	}
	if _, err := tty.Write(b); err != nil {
		return fmt.Errorf("pty write: %w", err)
	}
	return nil
}

// read reads up to len(b) bytes of child output from the pty master.
func (s *ptySession) read(b []byte) (int, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return 0, fmt.Errorf("pty closed")
	}
	return s.tty.Read(b) // EIO (linux) / <nil> (bsd) once the child exits
}

// reapIfExited reaps the child once it has exited and reports whether it is
// gone. alive()'s Signal(0) probe returns true for an UNREAPED zombie, so
// PTYRead's error path must reap before deciding — otherwise a dead shell
// reports alive forever and the terminal never shows "exited".
func (s *ptySession) reapIfExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.reaped {
		return s.reaped
	}
	done := make(chan struct{})
	go func() {
		_ = s.cmd.Wait() // no kill here — only reap an already-exited child
		close(done)
	}()
	select {
	case <-done:
		s.reaped = true
	case <-time.After(200 * time.Millisecond):
		// Wait didn't return promptly ⇒ the process is likely still alive
		// (a normal live shell blocks Wait). Leave reaped=false.
	}
	return s.reaped
}

// resize adjusts the pty dimensions (and delivers SIGWINCH to the child).
func (s *ptySession) resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("pty closed")
	}
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("pty resize: cols/rows must be positive (got %dx%d)", cols, rows)
	}
	if cols > 1000 || rows > 1000 {
		cols, rows = 1000, 1000
	}
	if err := pty.Setsize(s.tty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		return fmt.Errorf("pty resize: %w", err)
	}
	s.cols, s.rows = cols, rows
	return nil
}

// close terminates the child, reaps it, and releases the pty.
func (s *ptySession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait() // reap so the child never lingers as a zombie
	}
	_ = s.tty.Close()
}

// isClosed reads the closed flag under the session's own mutex — get() holds
// ptyManager.mu, not this lock, so a concurrent close() must not race it.
func (s *ptySession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// alive reports whether the child process is still running.
func (s *ptySession) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.cmd.Process == nil {
		return false
	}
	return s.cmd.Process.Signal(syscall.Signal(0)) == nil
}

// ptyManager owns the live PTY sessions (one per terminal tab).
type ptyManager struct {
	mu       sync.Mutex
	next     int
	sessions map[int]*ptySession
}

var ptys = &ptyManager{sessions: map[int]*ptySession{}}

func (m *ptyManager) create(cols, rows int) (int, error) {
	return m.createArgv(cols, rows, "", nil)
}

func (m *ptyManager) createArgv(cols, rows int, name string, args []string) (int, error) {
	s, err := newPTY(cols, rows, name, args)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	id := m.next
	m.sessions[id] = s
	return id, nil
}

func (m *ptyManager) get(id int) (*ptySession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || s.isClosed() {
		return nil, fmt.Errorf("pty %d not found or closed", id)
	}
	return s, nil
}

func (m *ptyManager) kill(id int) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if ok {
		s.close()
	}
}

// --- Wails bindings (same surface as the ConPTY implementation) ---

// PTYCreate spawns a new pty running the user's login shell and returns its id.
func (a *App) PTYCreate(cols, rows int) (int, error) {
	return ptys.create(cols, rows)
}

// PTYCreateForTab spawns the terminal a tab's workspace calls for: the local
// login shell, or a docker exec / ssh session into the tab's remote — the
// same environments the agent works in. (WSL remotes are Windows-only, like
// the rest of the wsl transport.)
func (a *App) PTYCreateForTab(tabID string, cols, rows int) (int, error) {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	a.mu.RUnlock()
	if tab == nil || tab.Remote == nil {
		return ptys.create(cols, rows)
	}
	ref := *tab.Remote
	root := tab.WorkspaceRoot // copied under the RLock: mutated elsewhere
	var name string
	var args []string
	switch ref.Kind {
	case "docker":
		name, args = "docker", []string{"exec", "-it", ref.Target, "sh"}
		if root := strings.TrimSpace(root); root != "" {
			args = append(args, "-c", "cd "+shquotePath(root)+" && exec sh")
		}
	case "ssh":
		host, port := splitSSHTarget(ref.Target)
		args = []string{"ssh", "-o", "BatchMode=yes"}
		if port != "" {
			args = append(args, "-p", port)
		}
		if u := strings.TrimSpace(ref.User); u != "" {
			args = append(args, u+"@"+host)
		} else {
			args = append(args, host)
		}
		if root := strings.TrimSpace(root); root != "" {
			args = append(args, "cd "+shquotePath(root)+" && exec $SHELL -l")
		} else {
			args = append(args, "exec $SHELL -l")
		}
		name = "ssh"
	default:
		return ptys.create(cols, rows)
	}
	return ptys.createArgv(cols, rows, name, args)
}

// PTYWrite sends bytes to the pty's stdin.
func (a *App) PTYWrite(id int, input string) error {
	s, err := ptys.get(id)
	if err != nil {
		return err
	}
	return s.write([]byte(input))
}

// PTYRead reads pending output from the pty. Returns an empty string when
// there is nothing to read (the frontend polls). The second return is false
// when the session has exited.
func (a *App) PTYRead(id int) (string, bool, error) {
	s, err := ptys.get(id)
	if err != nil {
		return "", false, err
	}
	buf := make([]byte, 4096)
	n, err := s.read(buf)
	if err != nil {
		// Read errors on the master mean the child exited (linux EIO) or the
		// line dropped. alive() alone lies for an unreaped zombie — reap once
		// and re-check before reporting "still running".
		if !s.reapIfExited() && s.alive() {
			return "", true, nil // read hiccup but process alive
		}
		return string(buf[:n]), false, nil
	}
	return string(buf[:n]), s.alive(), nil
}

// PTYResize adjusts the pty dimensions.
func (a *App) PTYResize(id, cols, rows int) error {
	s, err := ptys.get(id)
	if err != nil {
		return err
	}
	return s.resize(cols, rows)
}

// PTYKill terminates the pty and its child process.
func (a *App) PTYKill(id int) {
	ptys.kill(id)
}

// PTYAlive reports whether the child process is still running.
func (a *App) PTYAlive(id int) bool {
	s, err := ptys.get(id)
	if err != nil {
		return false
	}
	return s.alive()
}
