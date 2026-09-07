//go:build windows

package builtin

import (
	"encoding/binary"
	"strconv"
	"testing"
	"unsafe"
)

// TestScreenToolsRoster guards the WINDOWS desktop-automation tool set: the
// four cross-platform action tools PLUS the Windows-native perception tools
// (screenshot, get_ui_tree, screen_perceive). This file is //go:build windows,
// so it only runs on Windows. The cross-platform base set (screen_click /
// screen_type / screen_scroll / screen_key) is covered separately and runs on
// every platform — see TestBaseScreenToolsRoster in screen_tools_test.go.
func TestScreenToolsRoster(t *testing.T) {
	tools := ScreenTools()
	// Windows exposes the full 7-tool set (base actions + Windows perception).
	want := map[string]bool{
		"screenshot": true, "screen_click": true, "screen_type": true,
		"screen_scroll": true, "get_ui_tree": true, "screen_perceive": true,
		"screen_key": true,
	}
	seen := make(map[string]bool, len(tools))
	for _, tl := range tools {
		name := tl.Name()
		seen[name] = true
		if !want[name] {
			t.Errorf("unexpected screen tool %q", name)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("missing screen tool %q", name)
		}
	}
}

// TestScreenToolsReadOnlyClassification locks the read-only flags so a flip
// doesn't silently change batching behavior. screenshot + get_ui_tree are
// read-only (safe to parallelize); click/type/scroll mutate the desktop.
func TestScreenToolsReadOnlyClassification(t *testing.T) {
	readOnly := map[string]bool{"screenshot": true, "get_ui_tree": true, "screen_perceive": true}
	for _, tl := range ScreenTools() {
		got := tl.ReadOnly()
		if want := readOnly[tl.Name()]; got != want {
			t.Errorf("%s ReadOnly() = %v, want %v", tl.Name(), got, want)
		}
	}
}

// TestAbsInt was moved to screen_tools_test.go (the helper is now
// platform-agnostic, so the test runs on every platform there).

// TestBuildInputRecordWireLayout asserts the INPUT record's wire layout against
// the native Win32 contract. The typed Go structs (mouseInput/keyboardInput)
// pack {type; union fields} back to back, but on 64-bit targets the native INPUT
// places the union at byte 8 (ULONG_PTR alignment). An earlier version copied
// the struct verbatim, shifting every field SendInput reads 4 bytes early —
// clicks/keys/scroll silently did nothing. This test drives the exact
// buffer-building path sendInput uses so a regression cannot hide behind the
// syscall boundary (we never synthesize real input in tests).
func TestBuildInputRecordWireLayout(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skipf("wire offsets differ on %d-bit; layout derivation is exercised via winInputShape", strconv.IntSize)
	}
	if inputUnionOffset != 8 {
		t.Fatalf("inputUnionOffset = %d, want 8 (DWORD type + 4 bytes padding on x64)", inputUnionOffset)
	}
	if inputRecordSize != 40 {
		t.Fatalf("inputRecordSize = %d, want 40", inputRecordSize)
	}

	// Keyboard: a known vk/scan/flags record must land each field at its
	// KEYBDINPUT offset inside the union (wVk@0, wScan@2, dwFlags@4, time@8).
	ki := keyboardInput{Type: inputKeyboard, Vk: 0x41, Scan: 0x1E, Flags: keyeventfKeyUp, Time: 777}
	in := buildInputRecord(inputKeyboard, unsafe.Pointer(&ki), int(unsafe.Sizeof(ki)))
	if len(in) != inputRecordSize {
		t.Fatalf("record len = %d, want %d", len(in), inputRecordSize)
	}
	if got := binary.LittleEndian.Uint32(in[0:4]); got != inputKeyboard {
		t.Fatalf("type = %#x, want %#x", got, inputKeyboard)
	}
	if got := binary.LittleEndian.Uint16(in[8:10]); got != 0x41 {
		t.Errorf("wVk at union+0 = %#x, want 0x41 (the old verbatim copy put DX/vk at byte 4)", got)
	}
	if got := binary.LittleEndian.Uint16(in[10:12]); got != 0x1E {
		t.Errorf("wScan at union+2 = %#x, want 0x1E", got)
	}
	if got := binary.LittleEndian.Uint32(in[12:16]); got != keyeventfKeyUp {
		t.Errorf("dwFlags at union+4 = %#x, want %#x", got, keyeventfKeyUp)
	}
	if got := binary.LittleEndian.Uint32(in[16:20]); got != 777 {
		t.Errorf("time at union+8 = %d, want 777", got)
	}

	// Mouse: dx/dy/mouseData/flags/time at their MOUSEINPUT offsets
	// (dx@0, dy@4, mouseData@8, dwFlags@12, time@16 inside the union).
	mi := mouseInput{
		Type:      inputMouse,
		DX:        111,
		DY:        222,
		MouseData: 120,
		Flags:     mouseeventfWheel | mouseeventfLeftDown,
		Time:      555,
	}
	in = buildInputRecord(inputMouse, unsafe.Pointer(&mi), int(unsafe.Sizeof(mi)))
	if got := binary.LittleEndian.Uint32(in[0:4]); got != inputMouse {
		t.Fatalf("type = %#x, want %#x", got, inputMouse)
	}
	if got := binary.LittleEndian.Uint32(in[8:12]); got != 111 {
		t.Errorf("dx at union+0 = %d, want 111", got)
	}
	if got := binary.LittleEndian.Uint32(in[12:16]); got != 222 {
		t.Errorf("dy at union+4 = %d, want 222", got)
	}
	if got := binary.LittleEndian.Uint32(in[16:20]); got != 120 {
		t.Errorf("mouseData at union+8 = %d, want 120", got)
	}
	if got := binary.LittleEndian.Uint32(in[20:24]); got != mouseeventfWheel|mouseeventfLeftDown {
		t.Errorf("dwFlags at union+12 = %#x, want %#x", got, mouseeventfWheel|mouseeventfLeftDown)
	}
	if got := binary.LittleEndian.Uint32(in[24:28]); got != 555 {
		t.Errorf("time at union+16 = %d, want 555", got)
	}
}
