package switch_account

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type MuMuWindowInfo struct {
	Index      int
	Name       string
	PID        uint32
	MainHWND   uintptr
	RenderHWND uintptr
	AdbAddress string
	Started    bool
}

type rawMuMuInfo struct {
	Index            any    `json:"index"`
	Name             string `json:"name"`
	IsProcessStarted bool   `json:"is_process_started"`
	PID              uint32 `json:"pid"`
	MainWnd          any    `json:"main_wnd"`
	RenderWnd        any    `json:"render_wnd"`
	AdbHostIP        string `json:"adb_host_ip"`
	AdbPort          int    `json:"adb_port"`
	ErrorCode        int    `json:"error_code"`
}

func ResolveMuMuManagerPath(adbPath string) string {
	if trimmed := strings.TrimSpace(adbPath); trimmed != "" {
		return filepath.Join(filepath.Dir(trimmed), "MuMuManager.exe")
	}
	return `D:\tools\MuMuPlayer\nx_main\MuMuManager.exe`
}

func ParseMuMuInfoJSON(raw []byte, targetIndex int, expectedAdbAddr string) (*MuMuWindowInfo, error) {
	trimmed := strings.TrimSpace(string(raw))
	var objMap map[string]json.RawMessage
	if trimmed == "" || json.Unmarshal([]byte(trimmed), &objMap) != nil {
		return nil, fmt.Errorf("invalid MuMuManager JSON for index %d", targetIndex)
	}
	payload := []byte(trimmed)
	if sub, ok := objMap[strconv.Itoa(targetIndex)]; ok && len(sub) > 0 && sub[0] == '{' {
		payload = sub
	}
	var item rawMuMuInfo
	if err := json.Unmarshal(payload, &item); err != nil || item.ErrorCode != 0 {
		return nil, fmt.Errorf("failed to decode MuMu #%d info (code=%d)", targetIndex, item.ErrorCode)
	}
	if idx, err := parseAnyInt(item.Index); err != nil || idx != targetIndex {
		return nil, fmt.Errorf("MuMu index mismatch: got %v, want %d", item.Index, targetIndex)
	}
	var addr string
	if item.AdbHostIP != "" && item.AdbPort > 0 {
		addr = fmt.Sprintf("%s:%d", item.AdbHostIP, item.AdbPort)
	}
	if exp := strings.TrimSpace(expectedAdbAddr); exp != "" && addr != "" && !strings.EqualFold(addr, exp) {
		return nil, fmt.Errorf("MuMu #%d address %s mismatches %s", targetIndex, addr, exp)
	}
	mainHWND, _ := parseAnyUintptr(item.MainWnd)
	renderHWND, _ := parseAnyUintptr(item.RenderWnd)
	return &MuMuWindowInfo{
		Index: targetIndex, Name: strings.TrimSpace(item.Name), PID: item.PID,
		MainHWND: mainHWND, RenderHWND: renderHWND, AdbAddress: addr, Started: item.IsProcessStarted,
	}, nil
}

func FindMuMuIndexByAddress(raw []byte, expectedAdbAddr string) (int, error) {
	exp := strings.TrimSpace(expectedAdbAddr)
	var objMap map[string]json.RawMessage
	if exp == "" || json.Unmarshal(raw, &objMap) != nil {
		return -1, fmt.Errorf("invalid MuMu all-info output")
	}
	candidates := []json.RawMessage{raw}
	for _, sub := range objMap {
		if len(sub) > 0 && sub[0] == '{' {
			candidates = append(candidates, sub)
		}
	}
	for _, c := range candidates {
		var item rawMuMuInfo
		if json.Unmarshal(c, &item) == nil && item.IsProcessStarted && item.AdbHostIP != "" && item.AdbPort > 0 {
			if strings.EqualFold(fmt.Sprintf("%s:%d", item.AdbHostIP, item.AdbPort), exp) {
				if idx, err := parseAnyInt(item.Index); err == nil {
					return idx, nil
				}
			}
		}
	}
	return -1, fmt.Errorf("no running MuMu instance matched %s", exp)
}

func parseAnyInt(v any) (int, error) {
	switch val := v.(type) {
	case float64:
		return int(val), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(val))
	default:
		return 0, fmt.Errorf("invalid int %v", v)
	}
}

func parseAnyUintptr(v any) (uintptr, bool) {
	switch val := v.(type) {
	case float64:
		return uintptr(uint64(max(0, int64(val)))), val > 0
	case string:
		u, err := strconv.ParseUint(strings.TrimSpace(val), 0, 64)
		return uintptr(u), err == nil && u > 0
	default:
		return 0, false
	}
}
