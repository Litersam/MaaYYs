package main

import (
	"reflect"
	"testing"
)

func TestAwakeningTaskOptions(t *testing.T) {
	_, tasks, err := loadTaskDefinitions(testRepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	task, ok := tasks["觉醒"]
	if !ok || task.Category != "standalone" {
		t.Fatal("standalone awakening task is missing")
	}
	defaults := resolveTaskOverride(task, nil)
	initParams := func(override map[string]any) any {
		t.Helper()
		node, ok := override["组队管控-初始化"].(map[string]any)
		if !ok {
			t.Fatal("party initialization is missing")
		}
		return node["custom_action_param"]
	}
	if got, want := initParams(defaults), (map[string]any{"role": "solo", "flow": "awake", "limit_count": 10}); !reflect.DeepEqual(got, want) {
		t.Fatalf("default party params = %#v, want %#v", got, want)
	}
	for _, node := range []string{"组队管控-准备", "组队管控-开启加成", "组队管控-拨动加成"} {
		if _, exists := defaults[node]; exists {
			t.Fatalf("default options override %s", node)
		}
	}

	member := resolveTaskOverride(task, map[string]any{
		"party_role": "member", "captain_names": "队长.+, 队长二", "teammate_names": "不适用",
		"limit_count": 2,
	})
	if got, want := initParams(member), (map[string]any{"role": "member", "flow": "awake", "limit_count": 2, "captain_names": "队长.+, 队长二"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("member party params = %#v, want %#v", got, want)
	}

	captain := resolveTaskOverride(task, map[string]any{
		"party_role": "captain", "teammate_names": "队友一,队友二", "limit_count": 3,
		"awakening_element": "water", "dungeon_layer": 6,
		"switch_soul": true, "group_name": "觉醒", "team_name": "水麒麟", "drop_awake": true,
	})
	if got, want := initParams(captain), (map[string]any{"role": "captain", "flow": "awake", "limit_count": 3, "teammate_names": "队友一,队友二"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("captain party params = %#v, want %#v", got, want)
	}
	for node, want := range map[string]map[string]any{
		"觉醒-选层入口":   {"next": []any{"觉醒-水麒麟", "[JumpBack]导航-觉醒材料"}},
		"觉醒-目标层标签":  {"expected": []any{"^(陆|六)层?$"}},
		"觉醒-当前层标题":  {"expected": []any{"^(陆|六)层$"}},
		"觉醒-切换御魂":   {"custom_action_param": map[string]any{"group_name": "觉醒", "team_name": "水麒麟"}},
		"组队管控-拨动加成": {"custom_action_param": map[string]any{"drop_awake": true}},
	} {
		if got := captain[node]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s override = %#v, want %#v", node, got, want)
		}
	}
}
