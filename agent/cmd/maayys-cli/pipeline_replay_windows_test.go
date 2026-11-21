//go:build windows

package main

import (
	"encoding/json"
	"testing"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestNativeGuildScansSamePageBeforeSwiping(t *testing.T) {
	withNativeBattleFixture(t, func(_ string, _ *maa.Resource, tasker *maa.Tasker) {
		const (
			entry      = "寮突18_从上往下打"
			firstStamp = "寮突-识别第一个目标是否已击破"
			firstSlot  = "寮突18_第一个待选目标"
			secondSlot = "寮突18_第二个待选目标"
			selectOne  = "寮突17_copy1"
			selectTwo  = "寮突17_copy2"
			swipe      = "寮突16_copy1"
		)
		for _, tc := range []struct {
			name                              string
			broken, firstAttempt, secondReady bool
		}{
			{name: "broken first slot", broken: true, secondReady: true},
			{name: "first selection did not open attack", firstAttempt: true, secondReady: true},
			{name: "all slots unavailable", broken: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				stop := map[string]any{"recognition": "DirectHit", "action": "StopTask"}
				override := map[string]any{
					entry:      map[string]any{"recognition": "DirectHit", "pre_delay": 0, "post_delay": 0},
					firstStamp: map[string]any{"recognition": "DirectHit", "enabled": tc.broken},
					firstSlot:  map[string]any{"recognition": "DirectHit", "inverse": false, "enabled": tc.firstAttempt, "pre_delay": 0, "post_delay": 0},
					secondSlot: map[string]any{"recognition": "DirectHit", "inverse": false, "enabled": tc.secondReady, "pre_delay": 0, "post_delay": 0},
					selectOne:  map[string]any{"recognition": "DirectHit", "action": "DoNothing", "enabled": tc.firstAttempt, "max_hit": 1, "pre_delay": 0, "post_delay": 0},
					selectTwo:  stop,
					swipe:      stop,
				}
				for _, node := range []string{
					"寮突-识别进攻按钮", "寮突18_第三个待选目标", "寮突18_第四个待选目标",
					"寮突-识别第二个目标是否已击破", "寮突-识别第三个目标是否已击破", "寮突-识别第四个目标是否已击破",
				} {
					override[node] = map[string]any{"enabled": false}
				}
				job := tasker.PostTask(entry, override).Wait()
				detail, err := job.GetDetail()
				hits := completedFixtureNodes(t, detail, err)
				if !job.Success() || (hits[selectOne] == 1) != tc.firstAttempt ||
					(hits[selectTwo] == 1) != tc.secondReady || (hits[swipe] == 1) == tc.secondReady {
					t.Fatalf("state graph selected the wrong destination: job=%v; nodes=%v", job.Status(), hits)
				}
			})
		}
	})
}

func TestNativeGateShrineNavigation(t *testing.T) {
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		const (
			entry  = "阴界之门-在开放时段"
			nav    = "阴界之门-导航神社"
			shrine = "导航-阴阳寮神社-识别是否位于阴阳寮神社界面"
			lobby  = "阴间之门5"
			click  = "阴界之门-点击神社"
		)
		for _, tc := range []struct {
			name             string
			started, canOpen bool
			clicks           int
		}{
			{name: "unchanged shrine button fails", clicks: 2},
			{name: "click enters shrine", canOpen: true, clicks: 1},
			{name: "already at shrine", started: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				prefix := t.Name() + "/"
				opening, visible, confirm, atLobby := prefix+"OpenShrine", prefix+"ShrineVisible", prefix+"ConfirmShrine", prefix+"LobbyVisible"
				inShrine, confirmed, clicks := tc.started, false, 0
				if err := res.RegisterCustomRecognition(visible, replayRecognition(func() bool { return inShrine })); err != nil {
					t.Fatal(err)
				}
				if err := res.RegisterCustomRecognition(atLobby, replayRecognition(func() bool { return confirmed })); err != nil {
					t.Fatal(err)
				}
				if err := res.RegisterCustomAction(confirm, offlineAction(func(*maa.Context, *maa.CustomActionArg) bool {
					confirmed = true
					return true
				})); err != nil {
					t.Fatal(err)
				}
				if err := res.RegisterCustomAction(opening, offlineAction(func(*maa.Context, *maa.CustomActionArg) bool {
					clicks++
					inShrine = tc.canOpen
					return clicks <= 2 // Bound the fixture if a future change removes the production budget.
				})); err != nil {
					t.Fatal(err)
				}
				override := map[string]any{
					entry:           map[string]any{"recognition": "DirectHit", "timeout": 600},
					nav:             map[string]any{"timeout": 500},
					shrine:          map[string]any{"recognition": "Custom", "custom_recognition": visible, "action": "Custom", "custom_action": confirm},
					lobby:           map[string]any{"recognition": "Custom", "custom_recognition": atLobby, "action": "StopTask"},
					click:           map[string]any{"action": "Custom", "custom_action": opening, "post_delay": 0},
					"导航-阴阳寮神社-点击神社": map[string]any{"recognition": "DirectHit", "action": "StopTask"},
					"导航-阴阳寮-点击阴阳寮":  map[string]any{"enabled": false},
					"阴界之门-展开庭院菜单":   map[string]any{"enabled": false},
					"阴界之门-已挑战":      map[string]any{"enabled": false},
					"阴间之门26":        map[string]any{"enabled": false},
					"阴间之门8":         map[string]any{"enabled": false},
				}
				collector, release := runstate.Bind(tasker)
				defer release()
				job := tasker.PostTask(entry, override).Wait()
				detail, err := job.GetDetail()
				hits := completedFixtureNodes(t, detail, err)
				out := collector.Outcome()
				if clicks != tc.clicks {
					t.Fatalf("shrine clicks=%d, want %d; nodes=%v", clicks, tc.clicks, hits)
				}
				if tc.started || tc.canOpen {
					if !job.Success() || hits[lobby] != 1 || out.Reported || hits[shrine] != 1 {
						t.Fatalf("did not reach lobby from the shrine: job=%v nodes=%v outcome=%+v", job.Status(), hits, out)
					}
				} else {
					reason, _ := out.Metrics["battle_reason"].(json.RawMessage)
					if !job.Failure() || hits[lobby] != 0 || hits[shrine] != 0 || !out.Reported ||
						out.Status != runstate.Failed || string(reason) != `"gate_entry_unconfirmed"` {
						t.Fatalf("unconfirmed shrine must fail before lobby: job=%v nodes=%v outcome=%+v", job.Status(), hits, out)
					}
				}
			})
		}
	})
}

func TestNativeRealmHarvestVerifiesStaminaBeforeExperience(t *testing.T) {
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		const (
			entry    = "结界收菜_体力页已确认"
			take     = "结界收菜_取体力"
			reward   = "结界收菜_收下奖励"
			switchXP = "结界收菜_打开经验酒壶"
		)
		for _, tc := range []struct {
			name       string
			zeroAfter  int // -1 keeps the stamina counter positive.
			wantClicks int
			want       runstate.Status
		}{
			{"unchanged counter despite dismissed popup", -1, 2, runstate.Failed},
			{"counter clears after retry", 2, 2, runstate.Completed},
			{"already empty", 0, 0, runstate.Completed},
		} {
			t.Run(tc.name, func(t *testing.T) {
				takes, popup, xp := 0, false, false
				staminaEmpty := func() bool { return tc.zeroAfter >= 0 && takes >= tc.zeroAfter }
				override := map[string]any{
					entry:        map[string]any{"timeout": 500},
					"结界收菜_取出按钮":  map[string]any{"recognition": "DirectHit"},
					"结界收菜_经验标签":  map[string]any{"recognition": "DirectHit"},
					"结界批次_已确认庭院": map[string]any{"recognition": "DirectHit"},
					"结界批次_自家结界":  map[string]any{"enabled": false},
					"结界批次_关食盒":   map[string]any{"enabled": false},
				}
				for node, match := range map[string]func() bool{
					"结界收菜_体力页":  func() bool { return !xp },
					"结界收菜_经验页":  func() bool { return xp },
					"结界收菜_计数为零": func() bool { return xp || staminaEmpty() },
					"结界收菜_计数为正": func() bool { return !xp && !staminaEmpty() },
					reward:      func() bool { return popup },
				} {
					name := t.Name() + "/recognize/" + node
					if err := res.RegisterCustomRecognition(name, replayRecognition(match)); err != nil {
						t.Fatal(err)
					}
					override[node] = replayRecognizedBy(name)
				}
				for node, act := range map[string]offlineAction{
					take: func(*maa.Context, *maa.CustomActionArg) bool {
						takes++
						popup = takes == 1
						return takes <= 2
					},
					reward:   func(*maa.Context, *maa.CustomActionArg) bool { popup = false; return true },
					switchXP: func(*maa.Context, *maa.CustomActionArg) bool { xp = true; return true },
				} {
					name := t.Name() + "/act/" + node
					if err := res.RegisterCustomAction(name, act); err != nil {
						t.Fatal(err)
					}
					opts, ok := override[node].(map[string]any)
					if !ok {
						opts = map[string]any{}
						override[node] = opts
					}
					opts["action"], opts["custom_action"], opts["post_delay"] = "Custom", name, 0
				}
				collector, release := runstate.Bind(tasker)
				defer release()
				job := tasker.PostTask(entry, override).Wait()
				detail, err := job.GetDetail()
				hits := completedFixtureNodes(t, detail, err)
				out := collector.Outcome()
				completed := tc.want == runstate.Completed
				if job.Success() != completed || !out.Reported || out.Status != tc.want || takes != tc.wantClicks ||
					(hits[switchXP] == 1) != completed || (hits[reward] == 1) != (tc.wantClicks > 0) {
					t.Fatalf("want status=%s stamina clicks=%d: job=%v clicks=%d nodes=%v outcome=%+v",
						tc.want, tc.wantClicks, job.Status(), takes, hits, out)
				}
			})
		}
	})
}

type replayRecognition func() bool

func (f replayRecognition) Run(_ *maa.Context, _ *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	if !f() {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: maa.Rect{10, 10, 30, 30}}, true
}

func replayRecognizedBy(name string) map[string]any {
	return map[string]any{"recognition": "Custom", "custom_recognition": name}
}

func completedFixtureNodes(t *testing.T, detail *maa.TaskDetail, err error) map[string]int {
	t.Helper()
	if err != nil || detail == nil {
		t.Fatalf("missing replay task detail: %v", err)
	}
	hits := map[string]int{}
	for _, node := range detail.NodeDetails {
		if node != nil && node.RunCompleted {
			hits[node.Name]++
		}
	}
	return hits
}
