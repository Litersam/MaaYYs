package switch_account

import (
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeParamsAndRedaction(t *testing.T) {
	p, err := NormalizeParams(Params{
		Email:    "  User@163.com ",
		Password: "super-secret-password",
		Platform: " Android ",
		Server:   " 全平台互通 ",
	})
	if err != nil {
		t.Fatalf("NormalizeParams failed: %v", err)
	}
	if p.Email != "User@163.com" || p.Platform != "android" || p.Server != "全平台互通" {
		t.Fatalf("unexpected normalized params: %+v", p)
	}

	if strings.Contains(fmt.Sprintf("%s / %#v", p, p), "super-secret-password") {
		t.Fatal("Params String/GoString leaked password")
	}

	redacted := RedactSecrets("failed with super-secret-password on login", "super-secret-password")
	if strings.Contains(redacted, "super-secret-password") {
		t.Fatalf("RedactSecrets leaked secret: %s", redacted)
	}

	if _, err := NormalizeParams(Params{Email: "u@163.com", Platform: "pc", Server: "s"}); err == nil {
		t.Fatal("expected invalid platform error")
	}
	if _, err := NormalizeParams(Params{Email: "", Platform: "ios", Server: "s"}); err == nil {
		t.Fatal("expected missing email error")
	}
	if _, err := NormalizeParams(Params{Email: "u@163.com", Platform: "ios", Server: ""}); err == nil {
		t.Fatal("expected missing server error")
	}
}

func TestMatchEmailAndServer(t *testing.T) {
	if !MatchEmail("user_01＠163.com ", "user_01@163.com") {
		t.Fatal("expected fullwidth @ email to match")
	}
	if MatchEmail("user_01@163.com", "user_02@163.com") {
		t.Fatal("distinct emails must not match")
	}
	if MatchEmail("user_01", "user_01@163.com") {
		t.Fatal("prefix-only email must not match")
	}

	if !MatchServer("【全平台互通】>", "全平台互通") {
		t.Fatal("expected decorated server text to match")
	}
	if MatchServer("携手同心", "全平台互通") {
		t.Fatal("distinct servers must not match")
	}
}

func TestParseMuMuInfoJSON(t *testing.T) {
	flat := []byte(`{"index":"4","name":"yys-A-1","is_process_started":true,"pid":4321,"main_wnd":"0x102030","render_wnd":"0x203040","adb_host_ip":"127.0.0.1","adb_port":16512,"error_code":0}`)
	info, err := ParseMuMuInfoJSON(flat, 4, "127.0.0.1:16512")
	if err != nil {
		t.Fatalf("ParseMuMuInfoJSON flat failed: %v", err)
	}
	if info.MainHWND != 0x102030 || info.RenderHWND != 0x203040 || info.PID != 4321 || !info.Started {
		t.Fatalf("unexpected MuMuWindowInfo: %+v", info)
	}

	if _, err := ParseMuMuInfoJSON(flat, 4, "127.0.0.1:16384"); err == nil {
		t.Fatal("expected address mismatch error")
	}

	nested := []byte(`{"4":{"index":4,"name":"yys-A-1","is_process_started":true,"adb_host_ip":"127.0.0.1","adb_port":16512}}`)
	idx, err := FindMuMuIndexByAddress(nested, "127.0.0.1:16512")
	if err != nil || idx != 4 {
		t.Fatalf("FindMuMuIndexByAddress got (%d, %v), want 4", idx, err)
	}
}
