//go:build darwin && cgo

package main

/*
#cgo darwin LDFLAGS: -framework Cocoa
void installfairpeerSystemQuitHook(void);
*/
import "C"

import "sync"

var installSystemQuitHookOnce sync.Once

func installSystemQuitHook() {
	installSystemQuitHookOnce.Do(func() {
		C.installfairpeerSystemQuitHook()
	})
}

//export fairpeerMarkSystemQuit
func fairpeerMarkSystemQuit() {
	markSystemQuitRequested()
}
