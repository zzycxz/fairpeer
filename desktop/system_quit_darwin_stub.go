//go:build darwin && !arm64

package main

// Stub implementations for darwin/amd64 builds where cgo is not available.
// They provide no‑op functions so the linker can resolve the symbols.

func installSystemQuitHook() {
    // No operation on amd64 macOS builds.
}

//export fairpeerMarkSystemQuit
func fairpeerMarkSystemQuit() {
    // No operation on amd64 macOS builds.
}
