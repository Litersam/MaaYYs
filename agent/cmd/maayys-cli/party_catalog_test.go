package main

import (
	"reflect"
	"slices"
	"testing"

	"maa-yys-agent/internal/runstate"
)

func TestPartyCatalogPreservesManualAndSelectsManagedRole(t *testing.T) {
	_, tasks, err := loadTaskDefinitions(testRepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	const party = "组队刷本"
	if p := resolveTaskOverride(tasks[party], map[string]any{"组队刷本-组队身份": "房客", "挑战次数": 7}); p["组队刷本-开局"] != nil ||
		!reflect.DeepEqual(p["组队刷本-队友"], map[string]any{"enabled": true}) {
		t.Fatalf("legacy room options no longer resolve: %v", p)
	}
	p := resolveTaskOverride(tasks[party], map[string]any{
		"party_role": "captain", "teammate_names": "甲,乙", "limit_count": 3, "dungeon": "soul_12",
	})
	if !reflect.DeepEqual(p["组队刷本-开局"], map[string]any{"next": []any{"组队管控-开局"}}) ||
		!reflect.DeepEqual(p["组队管控-初始化"], map[string]any{"custom_action_param": map[string]any{
			"flow": "party", "role": "captain", "teammate_names": "甲,乙", "limit_count": 3, "dungeon": "soul_12",
		}}) {
		t.Fatalf("managed route or typed params do not resolve: %v", p)
	}
	for _, name := range []string{party, "觉醒", "同心之兰"} {
		task := tasks[name]
		if !slices.Contains(task.Options, "party_role") || slices.Contains(task.Options, name+"-party_role") ||
			!slices.Contains(task.RawTask.Option, name+"-party_role") {
			t.Errorf("%s should expose one CLI role alias without an MXU option-name collision", name)
		}
	}
}

func TestBatch3RequiresExplicitOutcome(t *testing.T) {
	_, tasks, err := loadTaskDefinitions(testRepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"组队刷本", "觉醒", "同心之兰", "契灵结契"} {
		for _, call := range []string{name, "standalone/" + name, tasks[name].Entry} {
			if err := validateStepCompletion(outcomeTaskName(tasks, call), runstate.Outcome{Status: runstate.Completed}, nil); err == nil {
				t.Errorf("%s accepted a framework success without a verified terminal", call)
			}
		}
	}
}
