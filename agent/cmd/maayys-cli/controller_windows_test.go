//go:build windows

package main

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/MaaXYZ/maa-framework-go/v4/controller/win32"
)

func newOfflineController(t *testing.T) *maa.Controller {
	t.Helper()
	// MaaFramework v5.14 does not support the v4 Go binding's custom
	// controller callback ABI. Use a hidden test-owned window, never a window
	// found by enumeration, and disable all input methods.
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	user32 := syscall.NewLazyDLL("user32.dll")
	class, _ := syscall.UTF16PtrFromString("Static")
	hwnd, _, errno := user32.NewProc("CreateWindowExW").Call(
		0, uintptr(unsafe.Pointer(class)), 0, 0x80000000,
		0, 0, 1280, 720, 0, 0, 0, 0,
	)
	runtime.KeepAlive(class)
	if hwnd == 0 {
		t.Fatalf("create synthetic window: %v", errno)
	}
	t.Cleanup(func() { _, _, _ = user32.NewProc("DestroyWindow").Call(hwnd) })
	ctrl, err := maa.NewWin32Controller(unsafe.Pointer(hwnd), win32.ScreencapGDI, win32.InputNone, win32.InputNone)
	if err != nil {
		t.Fatalf("create synthetic Win32 controller: %v", err)
	}
	return ctrl
}
