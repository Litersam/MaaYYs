package demon_encounter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"
)

func TestDefaultNoPremiumPurchaseAndZeroCountWinsRouting(t *testing.T) {
	read := func(path string, target any) {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join("..", "..", "..")
	var pipeline map[string]struct {
		Enabled      *bool    `json:"enabled"`
		Next         []string `json:"next"`
		CustomAction string   `json:"custom_action"`
		Action       string   `json:"action"`
		AllOf        []any    `json:"all_of"`
	}
	read(filepath.Join(root, "resource_pack", "base", "pipeline", "战斗", "自动逢魔.json"), &pipeline)
	if n := pipeline["逢魔-购买50勾玉宝箱蓝票"]; n.Enabled == nil || *n.Enabled {
		t.Fatal("unguarded premium purchase in base pipeline")
	}
	first := pipeline["开始现世逢魔"].Next
	if len(first) == 0 || first[0] != "逢魔-今日挑战已用完" {
		t.Fatal("skip must run before any event discovery")
	}
	skip := pipeline[first[0]]
	if skip.CustomAction != "DemonEncounterSkipped" || len(skip.AllOf) < 2 {
		t.Fatal("skip must report its status under a confirmed map and count")
	}
	pattern := regexp.MustCompile(skip.AllOf[1].(map[string]any)["expected"].(string))
	if !pattern.MatchString("0/1") || pattern.MatchString("1/1") || pattern.MatchString("0/4") {
		t.Fatal("boss challenge and lantern counters must not be conflated")
	}
	var task struct {
		Option map[string]struct {
			DefaultCase string `json:"default_case"`
			Cases       []struct {
				Name     string `json:"name"`
				Override map[string]struct {
					Enabled bool `json:"enabled"`
				} `json:"pipeline_override"`
			} `json:"cases"`
		} `json:"option"`
	}
	read(filepath.Join(root, "tasks", "standalone", "逢魔.json"), &task)
	if task.Option["box_buy_amulet"].DefaultCase != "No" {
		t.Fatal("premium purchase must be opt-in in task config")
	}
	const open, buy = "逢魔-打开宝箱灯笼", "逢魔-购买50勾玉宝箱蓝票"
	if !slices.Contains(pipeline["识别是否用完现世逢魔次数"].Next, "逢魔-进入首领前地图稳定") ||
		!slices.Contains(pipeline["逢魔-进入首领前地图稳定"].Next, "自动逢魔9") ||
		!slices.Contains(pipeline["自动逢魔9"].Next, "[JumpBack]"+open) {
		t.Fatal("depleted lantern discovery must reach the opt-in chest path from the actual map")
	}
	lantern := pipeline[open]
	if lantern.Enabled == nil || *lantern.Enabled || lantern.Action != "Click" ||
		!slices.Contains(lantern.AllOf, any("逢魔-宝箱灯笼图标")) ||
		!slices.Contains(lantern.Next, buy) {
		t.Fatal("default must not open chest; opt-in must recognize a chest and open its order")
	}
	confirm := pipeline[buy]
	for _, evidence := range []string{"逢魔-宝箱蓝票图标", "逢魔-宝箱勾玉图标", "逢魔-宝箱五十勾玉"} {
		if !slices.Contains(confirm.AllOf, any(evidence)) {
			t.Fatalf("chest confirmation lacks %s", evidence)
		}
	}
	for _, option := range task.Option["box_buy_amulet"].Cases {
		want := option.Name == "Yes"
		for _, node := range []string{open, buy} {
			override, found := option.Override[node]
			if !found || override.Enabled != want {
				t.Fatalf("%s: %s enabled = %v", option.Name, node, override.Enabled)
			}
		}
	}
}

func TestEncounterScheduleMissAndErrorsAreDistinct(t *testing.T) {
	const normal = `{"start":"17:00","end":"23:00"}`
	for _, tc := range []struct {
		name, config string
		hour, minute int
		open, bad    bool
	}{
		{"not yet open", normal, 16, 59, false, false},
		{"opening minute", normal, 17, 0, true, false},
		{"closing minute", normal, 23, 0, false, false},
		{"overnight", `{"start":"22:00","end":"02:00"}`, 0, 59, true, false},
		{"malformed JSON", `{`, 17, 30, false, true},
		{"invalid clock", `{"start":"25:00","end":"23:00"}`, 1, 0, false, true},
		{"invalid mode", `{"start":"17:00","end":"23:00","mode":"out"}`, 18, 0, false, true},
		{"missing window", `{}`, 17, 30, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 11, 21, tc.hour, tc.minute, 0, 0, time.Local)
			got, err := encounterOpen(tc.config, now)
			if got != tc.open || (err != nil) != tc.bad {
				t.Fatalf("open = %v, err = %v, want %v / error=%v", got, err, tc.open, tc.bad)
			}
		})
	}
}
