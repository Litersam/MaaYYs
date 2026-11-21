//go:build windows

package switch_account

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	wmActivate, wmSetFocus = 0x0006, 0x0007
	wmChar, waActive       = 0x0102, 1
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procIsWindow                 = user32.NewProc("IsWindow")
	procSendMessageW             = user32.NewProc("SendMessageW")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procEnumChildWindows         = user32.NewProc("EnumChildWindows")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

type cachedHWNDs struct {
	mainWnd   uintptr
	renderWnd uintptr
}

var mumuHWNDCache sync.Map

func sendMuMuText(mumuIndex *int, adbPath, adbAddress, text string) error {
	mainWnd, renderWnd, err := resolveMuMuInputWindows(mumuIndex, adbPath, adbAddress)
	if err != nil {
		return err
	}
	if mainWnd != 0 {
		_, _, _ = procSendMessageW.Call(mainWnd, wmActivate, waActive, 0)
	}
	_, _, _ = procSendMessageW.Call(renderWnd, wmSetFocus, 0, 0)
	time.Sleep(150 * time.Millisecond)

	for i, u := range utf16.Encode([]rune(text)) {
		if ret, _, callErr := procPostMessageW.Call(renderWnd, wmChar, uintptr(u), 1); ret == 0 {
			return fmt.Errorf("PostMessageW WM_CHAR failed: %v", callErr)
		}
		if i == 0 {
			time.Sleep(80 * time.Millisecond)
		} else {
			time.Sleep(38 * time.Millisecond)
		}
	}
	return nil
}

func resolveMuMuInputWindows(mumuIndex *int, adbPath, adbAddress string) (uintptr, uintptr, error) {
	cacheKey := ""
	if mumuIndex != nil {
		cacheKey = fmt.Sprintf("idx:%d|%s", *mumuIndex, strings.TrimSpace(adbAddress))
	} else if strings.TrimSpace(adbAddress) != "" {
		cacheKey = "addr:" + strings.TrimSpace(adbAddress)
	}
	if cacheKey != "" {
		if v, ok := mumuHWNDCache.Load(cacheKey); ok {
			c := v.(cachedHWNDs)
			if r := validWnd(c.renderWnd); r != 0 {
				return validWnd(c.mainWnd), r, nil
			}
			mumuHWNDCache.Delete(cacheKey)
		}
	}

	mgr := ResolveMuMuManagerPath(adbPath)
	if _, err := os.Stat(mgr); err != nil {
		return 0, 0, fmt.Errorf("MuMuManager.exe missing: %w", err)
	}
	idx := -1
	if mumuIndex != nil {
		idx = *mumuIndex
	} else if strings.TrimSpace(adbAddress) != "" {
		out, err := runMuMuManager(mgr, "info", "-v", "all")
		if err != nil {
			return 0, 0, err
		}
		if idx, err = FindMuMuIndexByAddress(out, adbAddress); err != nil {
			return 0, 0, err
		}
	}
	if idx < 0 {
		return 0, 0, fmt.Errorf("missing mumu_index for Win32 input")
	}
	out, err := runMuMuManager(mgr, "info", "-v", strconv.Itoa(idx))
	if err != nil {
		return 0, 0, err
	}
	info, err := ParseMuMuInfoJSON(out, idx, adbAddress)
	if err != nil || !info.Started {
		return 0, 0, fmt.Errorf("MuMu #%d unavailable (%v)", idx, err)
	}
	mainWnd, renderWnd := validWnd(info.MainHWND), validWnd(info.RenderHWND)
	if mainWnd == 0 {
		mainWnd = findTopWindow(info.PID, info.Name)
	}
	if renderWnd == 0 && mainWnd != 0 {
		renderWnd = findRenderChild(mainWnd)
	}
	if renderWnd == 0 {
		renderWnd = mainWnd
	}
	if renderWnd == 0 {
		return 0, 0, fmt.Errorf("unable to locate render window for MuMu #%d", idx)
	}
	if cacheKey != "" {
		mumuHWNDCache.Store(cacheKey, cachedHWNDs{mainWnd: mainWnd, renderWnd: renderWnd})
	}
	return mainWnd, renderWnd, nil
}

func runMuMuManager(mgr string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, mgr, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Output()
}

func validWnd(hwnd uintptr) uintptr {
	if hwnd == 0 {
		return 0
	}
	if ret, _, _ := procIsWindow.Call(hwnd); ret != 0 {
		return hwnd
	}
	return 0
}

func winTitle(hwnd uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func findTopWindow(pid uint32, title string) uintptr {
	var found uintptr
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var winPID uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&winPID)))
		t := winTitle(hwnd)
		if (pid > 0 && winPID == pid && t != "") || (title != "" && t == title) {
			found = hwnd
			return 0
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return found
}

func findRenderChild(mainWnd uintptr) uintptr {
	var first, mumu uintptr
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if first == 0 {
			first = hwnd
		}
		if t := winTitle(hwnd); strings.EqualFold(t, "MuMuPlayer") || strings.EqualFold(t, "NemuPlayer") {
			mumu = hwnd
			return 0
		}
		return 1
	})
	procEnumChildWindows.Call(mainWnd, cb, 0)
	if mumu != 0 {
		return mumu
	}
	return first
}
