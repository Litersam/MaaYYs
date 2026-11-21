package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"maa-yys-agent/registry"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func normalizeSubcommand(arg string) string {
	return strings.TrimLeft(strings.ToLower(strings.TrimSpace(arg)), "-")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: maayys-cli <catalog|check-resources|run> (or --catalog / --check-resources)")
		os.Exit(2)
	}
	repoRoot := resolveRepoRoot()
	switch normalizeSubcommand(os.Args[1]) {
	case "catalog":
		if err := runCatalog(repoRoot, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "catalog error: %v\n", err)
			os.Exit(1)
		}
	case "check-resources":
		if err := runCheckResources(repoRoot, os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "check-resources error: %v\n", err)
			os.Exit(1)
		}
	case "run":
		jsonlOut := os.Stdout
		os.Stdout = os.Stderr
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		log.Logger = log.Output(os.Stderr)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := runStepsFromStdin(ctx, repoRoot, os.Stdin, jsonlOut)
		stop()
		os.Exit(code)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runCatalog(repoRoot string, out io.Writer) error {
	list, _, err := loadTaskDefinitions(repoRoot)
	if err != nil {
		return err
	}
	items := make([]CatalogTask, len(list))
	for i, t := range list {
		items[i] = t.CatalogTask
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(CatalogOutput{OK: true, Tasks: items})
}

func runCheckResources(repoRoot string, paths []string) error {
	if len(paths) == 0 {
		paths = defaultResourcePaths(repoRoot)
	}
	if err := initMaaFramework(repoRoot); err != nil {
		return err
	}
	defer func() { _ = maa.Release() }()
	res, err := loadAndRegisterResource(paths)
	if err != nil {
		return err
	}
	defer res.Destroy()
	for _, n := range []string{
		"账号登录", "启动游戏", "关闭阴阳师", "契灵结契-开始", "组队刷本-开局", "开始逢魔-检查时间",
		"好友寄养", "结界挂卡", "结界收菜", "自动结界", "寮突", "通用加成-归位", "装备御魂",
	} {
		if _, err := res.GetNodeJSON(n); err != nil {
			return fmt.Errorf("missing node %q: %w", n, err)
		}
	}
	data, _ := json.Marshal(map[string]any{"ok": true, "loaded": res.Loaded(), "resource_paths": paths})
	fmt.Println(string(data))
	return nil
}

func defaultResourcePaths(repoRoot string) []string {
	return []string{filepath.Join(repoRoot, "resource_pack", "base"), filepath.Join(repoRoot, "resource_pack", "official")}
}

func initMaaFramework(repoRoot string) error {
	if maa.IsInited() {
		return nil
	}
	logDir := filepath.Join(repoRoot, "debug")
	_ = os.MkdirAll(logDir, 0o755)
	if err := maa.Init(maa.WithLibDir(resolveMaaLibDir(repoRoot)), maa.WithLogDir(logDir), maa.WithStdoutLevel(maa.LoggingLevelOff)); err != nil {
		return err
	}
	_ = maa.ConfigInitOption(repoRoot, "{}")
	return nil
}

func loadAndRegisterResource(paths []string) (*maa.Resource, error) {
	res, err := maa.NewResource()
	if err != nil {
		return nil, err
	}
	if err := registry.RegisterResource(res); err != nil {
		res.Destroy()
		return nil, err
	}
	for _, p := range paths {
		if !res.PostBundle(p).Wait().Success() {
			res.Destroy()
			return nil, fmt.Errorf("加载资源包失败: %s", p)
		}
	}
	return res, nil
}

func resolveRepoRoot() string {
	if env := strings.TrimSpace(os.Getenv("MAAYYS_ROOT")); env != "" {
		return env
	}
	if exe, err := os.Executable(); err == nil {
		for _, cand := range []string{filepath.Dir(filepath.Dir(exe)), filepath.Dir(exe)} {
			if dirExists(filepath.Join(cand, "tasks")) && dirExists(filepath.Join(cand, "resource_pack")) {
				return cand
			}
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			if dirExists(filepath.Join(dir, "tasks")) && dirExists(filepath.Join(dir, "resource_pack")) {
				return dir
			}
			if next := filepath.Dir(dir); next == dir {
				break
			}
		}
	}
	return "."
}

func resolveMaaLibDir(repoRoot string) string {
	if override := strings.TrimSpace(os.Getenv("MAAFW_LIB_DIR")); override != "" {
		return override
	}
	return filepath.Join(repoRoot, "maafw")
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
