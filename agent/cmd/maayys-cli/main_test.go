package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func testRepoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return abs
}

func TestCatalogLoadsTasksIncludingLogin(t *testing.T) {
	root := testRepoRoot(t)
	var buf bytes.Buffer
	if err := runCatalog(root, &buf); err != nil {
		t.Fatalf("runCatalog failed: %v", err)
	}

	var out CatalogOutput
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal catalog output: %v", err)
	}
	if !out.OK || len(out.Tasks) < 10 {
		t.Fatalf("unexpected catalog output: ok=%v count=%d", out.OK, len(out.Tasks))
	}

	want := map[string]string{
		"账号登录":   "账号登录",
		"打开游戏":   "启动游戏",
		"关闭游戏":   "关闭阴阳师",
		"日常奖励领取": "日常-开始执行日常任务",
	}
	got := make(map[string]string, len(out.Tasks))
	for _, item := range out.Tasks {
		got[item.Name] = item.Entry
	}
	for name, entry := range want {
		if got[name] != entry {
			t.Errorf("task %q entry = %q, want %q", name, got[name], entry)
		}
	}
}

func TestJSONLWriterFormatAndSecretRedaction(t *testing.T) {
	var buf bytes.Buffer
	w := &jsonlWriter{out: &buf}
	w.addSecret("my$ecretP@ss")

	w.Log("info", "login test with my$ecretP@ss")
	w.Step("step-login", "running", "正在切换账号 my$ecretP@ss")
	w.Step("step-login", "complete", "已进入庭院")
	w.Result(true, false, "", false)

	raw := buf.String()
	if strings.Contains(raw, "my$ecretP@ss") {
		t.Fatalf("secret leaked in JSONL output: %s", raw)
	}
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 JSONL lines, got %d: %s", len(lines), raw)
	}
	if !strings.Contains(lines[3], `"type":"result"`) || !strings.Contains(lines[3], `"error":null`) {
		t.Fatalf("unexpected result JSONL line: %s", lines[3])
	}
}

func TestOfflineResourceLoadingWithBaseAndOfficial(t *testing.T) {
	root := testRepoRoot(t)
	libDir := resolveMaaLibDir(root)
	if _, err := os.Stat(filepath.Join(libDir, "MaaFramework.dll")); err != nil {
		t.Skipf("MaaFramework runtime not present at %s: %v", libDir, err)
	}

	if err := initMaaFramework(root); err != nil {
		t.Fatalf("initMaaFramework failed: %v", err)
	}
	defer func() { _ = maa.Release() }()

	res, err := loadAndRegisterResource([]string{
		filepath.Join(root, "resource_pack", "base"),
		filepath.Join(root, "resource_pack", "official"),
	})
	if err != nil {
		t.Fatalf("loadAndRegisterResource failed: %v", err)
	}
	defer res.Destroy()

	if !res.Loaded() {
		t.Fatal("expected resource.Loaded() == true")
	}

	loginNode, err := res.GetNode("账号登录")
	if err != nil || loginNode == nil {
		t.Fatalf("GetNode(账号登录) failed: %v", err)
	}

	startNode, err := res.GetNode("启动游戏")
	if err != nil || startNode == nil || startNode.Action == nil {
		t.Fatalf("GetNode(启动游戏) failed: %v", err)
	}
	sp, ok := startNode.Action.Param.(*maa.StartAppParam)
	if !ok || sp.Package != "com.netease.onmyoji.wyzymnqsd_cps" {
		t.Fatalf("unexpected official StartApp package: %#v", startNode.Action.Param)
	}
}
