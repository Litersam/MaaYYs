package demon_encounter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
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
		} `json:"option"`
	}
	read(filepath.Join(root, "tasks", "standalone", "逢魔.json"), &task)
	if task.Option["box_buy_amulet"].DefaultCase != "No" {
		t.Fatal("premium purchase must be opt-in in task config")
	}
}
