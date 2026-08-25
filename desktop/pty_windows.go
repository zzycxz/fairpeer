//go:build windows

// pty_windows.go — upgrade spec 3-4: ConPTY (Windows pseudoconsole) wrapper.
// Provides a persistent terminal session the frontend can write stdin to and
// read stdout from, with resize support — the backend for TerminalPanel v2.
//
// The ConPTY API (Windows 10 1809+) creates a pseudoconsole object with a
// pair of pipes: one for input (we write, ConPTY reads) and one for output
// (ConPTY writes, we read). A child process is spawned attached to the
// pseudoconsole, so it behaves as if connected to a real terminal — ANSI
// escape sequences, cursor addressing, colors, interactive programs (vim,
// ssh, node REPL) all work.
package main

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	procCreatePseudoConsole      = kernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole      = kernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole       = kernel32.NewProc("ClosePseudoConsole")
	procInitializeProcThreadAttr = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttr     = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttr     = kernel32.NewProc("DeleteProcThreadAttributeList")
)

const (
	// PSEUDOCONSOLE_INHERIT_CURSOR makes the new ConPTY inherit the cursor
	// position from the calling terminal — not needed for our headless use.
	pseudoconsoleInheritCursor = 0x1
	// procThreadAttributePseudoConsole is the attribute list key for attaching
	// a process to a pseudoconsole (not in the public headers).
	procThreadAttributePseudoConsole = 22
)

// ptySession is one live pseudoconsole + its attached process.
type ptySession struct {
	mu         sync.Mutex
	handle     windows.Handle // HPCON
	proc       windows.Handle // process handle
	inWrite    windows.Handle // our end of the input pipe (we write here)
	outRead    windows.Handle // our end of the output pipe (we read here)
	closed     bool
	cols, rows int
}

// newPTY creates a pseudoconsole, spawns cmd.exe (or the given raw command
// line — e.g. "wsl.exe -d Ubuntu --cd /home/me/proj" for a remote tab's
// bridged terminal) attached to it, and starts a background goroutine that
// pumps output to the caller-provided channel.
func newPTY(cols, rows int, commandLine string) (*ptySession, error) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 30
	}

	// Create the input pipe (we write, ConPTY reads).
	var inRead, inWrite windows.Handle
	err := windows.CreatePipe(&inRead, &inWrite, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("pty input pipe: %w", err)
	}
	// Create the output pipe (ConPTY writes, we read).
	var outRead, outWrite windows.Handle
	err = windows.CreatePipe(&outRead, &outWrite, nil, 0)
	if err != nil {
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		return nil, fmt.Errorf("pty output pipe: %w", err)
	}

	// Create the pseudoconsole.
	dim := uint32(cols) | (uint32(rows) << 16) // COORD {X, Y}
	var hpc windows.Handle
	r1, _, err := procCreatePseudoConsole.Call(
		uintptr(dim),
		uintptr(inRead),
		uintptr(outWrite),
		0,
		uintptr(unsafe.Pointer(&hpc)),
	)
	if r1 != 0 { // S_OK == 0
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		windows.CloseHandle(outWrite)
		return nil, fmt.Errorf("CreatePseudoConsole: %w", err)
	}

	// The ConPTY owns inRead and outWrite now; close our copies so the pipes
	// tear down properly when the ConPTY closes.
	windows.CloseHandle(inRead)
	windows.CloseHandle(outWrite)

	// Build the attribute list with the pseudoconsole handle, using the
	// x/sys helper types that manage the raw buffer.
	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		procClosePseudoConsole.Call(uintptr(hpc))
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		return nil, fmt.Errorf("attribute list: %w", err)
	}
	defer attrList.Delete()
	if err := attrList.Update(procThreadAttributePseudoConsole,
		unsafe.Pointer(&hpc), unsafe.Sizeof(hpc)); err != nil {
		procClosePseudoConsole.Call(uintptr(hpc))
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		return nil, fmt.Errorf("attribute update: %w", err)
	}

	// Spawn the shell attached to the pseudoconsole via StartupInfoEx. An
	// empty commandLine keeps the historical plain cmd.exe; a custom line
	// (remote tabs) is resolved through the shell-less CreateProcess command
	// line, so the executable must be findable on PATH (wsl.exe always is).
	siEx := &windows.StartupInfoEx{}
	siEx.Cb = uint32(unsafe.Sizeof(*siEx))
	siEx.ProcThreadAttributeList = attrList.List()
	pi := &windows.ProcessInformation{}
	var cmdPath *uint16
	cmdLine := strings.TrimSpace(commandLine)
	if cmdLine == "" {
		cmdLine = `cmd.exe`
	} else {
		// lpApplicationName must stay nil for a mixed command line.
		cmdPath = nil
	}
	cmdArgs, _ := windows.UTF16PtrFromString(cmdLine)
	err = windows.CreateProcess(
		cmdPath, cmdArgs, nil, nil, false,
		windows.EXTENDED_STARTUPINFO_PRESENT,
		nil, nil, &siEx.StartupInfo, pi,
	)
	if err != nil {
		procClosePseudoConsole.Call(uintptr(hpc))
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		return nil, fmt.Errorf("CreateProcess: %w", err)
	}

	// Close the thread handle; we only track the process.
	windows.CloseHandle(pi.Thread)

	s := &ptySession{
		handle:  hpc,
		proc:    pi.Process,
		inWrite: inWrite,
		outRead: outRead,
		cols:    cols,
		rows:    rows,
	}
	return s, nil
}

// write sends bytes to the pseudoconsole's stdin.
func (s *ptySession) write(b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("pty closed")
	}
	var n uint32
	err := windows.WriteFile(s.inWrite, b, &n, nil)
	if err != nil {
		return fmt.Errorf("pty write: %w", err)
	}
	return nil
}

// read reads up to len(b) bytes from the pseudoconsole's stdout.
func (s *ptySession) read(b []byte) (int, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return 0, fmt.Errorf("pty closed")
	}
	var n uint32
	err := windows.ReadFile(s.outRead, b, &n, nil)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// resize adjusts the pseudoconsole dimensions.
func (s *ptySession) resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("pty closed")
	}
	dim := uint32(cols) | (uint32(rows) << 16)
	r1, _, err := procResizePseudoConsole.Call(uintptr(s.handle), uintptr(dim))
	if r1 != 0 {
		return fmt.Errorf("ResizePseudoConsole: %w", err)
	}
	s.cols, s.rows = cols, rows
	return nil
}

// close tears down the process, pseudoconsole, and pipes.
func (s *ptySession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	windows.TerminateProcess(s.proc, 1)
	windows.WaitForSingleObject(s.proc, 2000) // 2s grace
	windows.CloseHandle(s.proc)
	procClosePseudoConsole.Call(uintptr(s.handle))
	windows.CloseHandle(s.inWrite)
	windows.CloseHandle(s.outRead)
}

// alive reports whether the child process is still running.
func (s *ptySession) alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	var ec uint32
	if windows.GetExitCodeProcess(s.proc, &ec) != nil {
		return false
	}
	return ec == 259 // STILL_ACTIVE
}

// ptyManager owns the live PTY sessions (one per terminal tab).
type ptyManager struct {
	mu       sync.Mutex
	next     int
	sessions map[int]*ptySession
}

var ptys = &ptyManager{sessions: map[int]*ptySession{}}

func (m *ptyManager) create(cols, rows int) (int, error) {
	return m.createCmd(cols, rows, "")
}

func (m *ptyManager) createCmd(cols, rows int, commandLine string) (int, error) {
	s, err := newPTY(cols, rows, commandLine)
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
	if !ok || s.closed {
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

// --- Wails bindings (upgrade spec 3-4) ---

// PTYCreate spawns a new pseudoconsole running cmd.exe and returns its id.
func (a *App) PTYCreate(cols, rows int) (int, error) {
	return ptys.create(cols, rows)
}

// PTYCreateForTab spawns the terminal a tab's workspace calls for: cmd.exe
// locally, or `wsl.exe -d <distro> --cd <root>` for a remote (WSL) tab, so the
// integrated terminal lands inside the same environment the agent works in
// without any remote PTY protocol.
func (a *App) PTYCreateForTab(tabID string, cols, rows int) (int, error) {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	a.mu.RUnlock()
	if tab == nil || tab.Remote == nil {
		return ptys.create(cols, rows)
	}
	ref := *tab.Remote
	var parts []string
	switch ref.Kind {
	case "wsl":
		parts = []string{"wsl.exe", "-d", ref.Target}
		if u := strings.TrimSpace(ref.User); u != "" {
			parts = append(parts, "-u", u)
		}
		if root := strings.TrimSpace(tab.WorkspaceRoot); root != "" {
			parts = append(parts, "--cd", root)
		}
	case "docker":
		parts = []string{"docker", "exec", "-it", ref.Target, "sh"}
		if root := strings.TrimSpace(tab.WorkspaceRoot); root != "" {
			parts = append(parts, "-c", "cd "+root+" && exec sh")
		}
	case "ssh":
		host, port := splitSSHTarget(ref.Target)
		parts = []string{"ssh", "-o", "BatchMode=yes"}
		if u := strings.TrimSpace(ref.User); u != "" {
			parts = append(parts, u+"@"+host)
		} else {
			parts = append(parts, host)
		}
		if port != "" {
			parts = append(parts, "-p", port)
		}
		if root := strings.TrimSpace(tab.WorkspaceRoot); root != "" {
			parts = append(parts, "cd "+root+" && exec $SHELL -l")
		} else {
			parts = append(parts, "$SHELL -l")
		}
	default:
		return ptys.create(cols, rows)
	}
	return ptys.createCmd(cols, rows, strings.Join(parts, " "))
}

// PTYWrite sends bytes to the pseudoconsole's stdin.
func (a *App) PTYWrite(id int, input string) error {
	s, err := ptys.get(id)
	if err != nil {
		return err
	}
	return s.write([]byte(input))
}

// PTYRead reads pending output from the pseudoconsole's stdout. Returns an
// empty string when there is nothing to read (the frontend polls). The
// second return is false when the session has exited.
func (a *App) PTYRead(id int) (string, bool, error) {
	s, err := ptys.get(id)
	if err != nil {
		return "", false, err
	}
	buf := make([]byte, 4096)
	n, err := s.read(buf)
	if err != nil {
		if s.alive() {
			return "", true, nil // read timeout/EOF but process alive
		}
		return string(buf[:n]), false, nil
	}
	return string(buf[:n]), s.alive(), nil
}

// PTYResize adjusts the pseudoconsole dimensions.
func (a *App) PTYResize(id, cols, rows int) error {
	s, err := ptys.get(id)
	if err != nil {
		return err
	}
	return s.resize(cols, rows)
}

// PTYKill terminates the pseudoconsole and its child process.
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
