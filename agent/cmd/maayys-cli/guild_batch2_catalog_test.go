package main

import "testing"

func TestBatch2GuildTaskOptions(t *testing.T) {
	_, tasks, err := loadTaskDefinitions(testRepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if banquet, ok := lookupTask(tasks, "standalone/寮宴会"); !ok || banquet.Entry != "寮宴会" {
		t.Fatalf("standalone 寮宴会 is not discoverable: %+v", banquet)
	}
	kirin, ok := lookupTask(tasks, "standalone/自动麒麟")
	if !ok {
		t.Fatal("standalone 自动麒麟 is not discoverable")
	}

	assertField := func(override map[string]any, node, field string, want any) {
		t.Helper()
		fields, _ := override[node].(map[string]any)
		if got := fields[field]; got != want {
			t.Fatalf("%s.%s = %v, want %v", node, field, got, want)
		}
	}
	assertPreset := func(override map[string]any, node, group, team string) {
		t.Helper()
		no, _ := override[node].(map[string]any)
		params, _ := no["custom_action_param"].(map[string]any)
		if params["group_name"] != group || params["team_name"] != team {
			t.Fatalf("%s picks %v, want %q/%q", node, params, group, team)
		}
	}

	oldPlan := resolveTaskOverride(kirin, map[string]any{
		"麒麟阵容预设": map[string]any{"group_name": "常驻分组", "team_name": "麒麟队"},
	})
	assertField(oldPlan, "麒麟-选择换御魂", "enabled", true)
	assertField(oldPlan, "麒麟-需要出战预设", "enabled", false)
	assertPreset(oldPlan, "麒麟6", "常驻分组", "麒麟队")

	lineupOnly := resolveTaskOverride(kirin, map[string]any{
		"麒麟是否切换御魂":          "No",
		"麒麟是否切换出战阵容":        "Yes",
		"battle_group_name": "备战分组",
		"battle_team_name":  "备战队",
	})
	assertField(lineupOnly, "麒麟-选择换御魂", "enabled", false)
	assertField(lineupOnly, "麒麟-选择不换御魂", "enabled", true)
	assertField(lineupOnly, "麒麟-需要出战预设", "enabled", true)
	assertField(lineupOnly, "麒麟-不用出战预设", "enabled", false)
	assertField(lineupOnly, "麒麟-直接开战", "enabled", false)
	assertPreset(lineupOnly, "麒麟-切换出战阵容", "备战分组", "备战队")
}
