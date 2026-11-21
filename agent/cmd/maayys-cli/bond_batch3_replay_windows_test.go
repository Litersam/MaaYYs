//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func newFastBondReplay(t *testing.T, root string, res *maa.Resource, tasker *maa.Tasker) guildBatch2Replay {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "resource_pack", "base", "pipeline", "战斗", "同心之兰.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pipeline map[string]json.RawMessage
	if err := json.Unmarshal(data, &pipeline); err != nil {
		t.Fatal(err)
	}
	f := newGuildBatch2Replay(t, res, tasker)
	for name := range pipeline {
		f.shorten(name)
		f.node(name)["pre_delay"], f.node(name)["post_delay"] = 0, 0
	}
	return f
}

func TestNativeBatch3BondCleanSkip(t *testing.T) {
	withNativeBattleFixture(t, func(root string, res *maa.Resource, tasker *maa.Tasker) {
		for _, tc := range []struct {
			name, marker, reason string
			clean                bool
			status               runstate.Status
		}{
			{"known cooldown", "同心之兰-冷却文字", "bond_cooldown", true, runstate.Skipped},
			{"both rewards already claimed", "同心之兰-今日已领文字", "bond_rewards_already_claimed", true, runstate.Skipped},
			{"cooldown but no return", "同心之兰-冷却文字", "bond_courtyard_unconfirmed", false, runstate.Failed},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newFastBondReplay(t, root, res, tasker)
				inFriends := true
				f.node("同心之兰")["custom_action_param"] = map[string]any{"role": "captain", "teammate_names": "测试队员"}
				f.recognize("同心之兰-好友页", func() bool { return inFriends })
				f.recognize("同心之兰-搭档好友名", func() bool { return inFriends })
				f.recognize("同心之兰-庭院", func() bool { return !inFriends && tc.clean })
				f.recognize("同心之兰-关闭图标", func() bool { return inFriends })
				f.recognize("同心之兰-非面板", func() bool { return true })
				f.recognize("同心之兰-关闭羁绊弹窗", func() bool { return false })
				f.recognize("同心之兰-关闭礼袋面板", func() bool { return false })
				f.recognize(tc.marker, func() bool { return inFriends })
				f.act("同心之兰-退出好友", func() { inFriends = false })
				f.run("同心之兰", tc.status, tc.reason)
				if inFriends {
					t.Fatal("did not attempt to leave Friends")
				}
			})
		}
	})
}

type bondReplayGrowth func() (int, int)

func (f bondReplayGrowth) Run(*maa.Context, *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	current, target := f()
	if target == 0 {
		return nil, false
	}
	return &maa.CustomRecognitionResult{
		Box: maa.Rect{1090, 146, 30, 18}, Detail: fmt.Sprintf(`{"Current":%d,"Target":%d}`, current, target),
	}, true
}

func TestNativeBatch3BondTwoRounds(t *testing.T) {
	withNativeBattleFixture(t, func(root string, res *maa.Resource, tasker *maa.Tasker) {
		for _, tc := range []struct {
			name, role, issue, reason string
			status                    runstate.Status
			batches, claims, limit    int
		}{
			{"captain sends and leads both rounds", "captain", "", "bond_growth_reward_claimed", runstate.Completed, 2, 2, 10},
			{"member accepts and returns the gift", "member", "", "bond_growth_reward_claimed", runstate.Completed, 2, 2, 10},
			{"resume partial growth", "captain", "resume", "bond_growth_reward_claimed", runstate.Completed, 2, 2, 10},
			{"native return without party receipt", "captain", "no receipt", "bond_party_unconfirmed", runstate.Failed, 1, 0, 10},
			{"native party action failed", "captain", "party failed", "bond_party_unconfirmed", runstate.Failed, 1, 0, 10},
			{"cap reached but growth too low", "captain", "low growth", "bond_growth_not_reached", runstate.Failed, 1, 0, 4},
			{"no reward popup", "captain", "no popup", "bond_reward_unconfirmed", runstate.Failed, 1, 0, 10},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newFastBondReplay(t, root, res, tasker)
				page := "friends"
				score, target, batches, claims, sent, returned, fought := 0, 0, 0, 0, 0, 0, 0
				if tc.issue != "" {
					score, target = 7, 10
				}
				if tc.issue == "low growth" {
					score = 0
				}
				claimedFirst := false
				f.node("同心之兰")["custom_action_param"] = map[string]any{
					"role": tc.role, "teammate_names": "测试搭档", "captain_names": "测试搭档", "limit_count": tc.limit,
				}
				f.recognize("同心之兰-好友页", func() bool { return page == "friends" || page == "panel" })
				f.recognize("同心之兰-搭档好友名", func() bool { return page == "friends" || page == "panel" })
				f.recognize("同心之兰-庭院", func() bool { return page == "courtyard" })
				f.recognize("同心之兰-面板标题", func() bool { return page == "panel" })
				f.recognize("同心之兰-非面板", func() bool { return page != "panel" })
				f.recognize("同心之兰-关闭图标", func() bool { return page == "friends" || page == "panel" })
				f.recognize("同心之兰-冷却文字", func() bool { return false })
				f.recognize("同心之兰-今日已领文字", func() bool { return false })
				f.recognize("同心之兰-一档满成长", func() bool { return score == 10 && target == 10 })
				f.recognize("同心之兰-二档满成长", func() bool { return score == 20 && target == 20 })
				f.recognize("同心之兰-奖励弹窗", func() bool { return page == "reward" })
				f.recognize("同心之兰-赠送价格", func() bool { return target == 0 && page == "panel" })
				f.recognize("同心之兰-回赠价格", func() bool { return claimedFirst && page == "panel" })
				f.recognize("同心之兰-赠送前额度", func() bool { return (target == 0 || claimedFirst) && page == "panel" })
				f.recognize("同心之兰-明确拒绝赠送", func() bool { return false })
				f.recognize("同心之兰-确认赠送", func() bool { return false })
				f.recognize("同心之兰-展开绑定邀请", func() bool { return false })
				f.recognize("同心之兰-接绑定邀请", func() bool {
					return page == "courtyard" && (tc.role == "captain" && claimedFirst && target == 10 || tc.role == "member" && target == 0)
				})
				f.act("同心之兰-接绑定邀请", func() {
					if target == 0 {
						target, sent = 10, sent+1
					} else {
						target, returned = 20, returned+1
					}
				})
				f.act("同心之兰-首次赠送", func() { target, sent = 10, sent+1 })
				f.act("同心之兰-回赠", func() { target, returned = 20, returned+1 })
				f.act("同心之兰-打开礼袋", func() { page = "panel" })
				f.act("同心之兰-再次打开礼袋", func() { page = "panel" })
				f.recognize("同心之兰-关闭羁绊弹窗", func() bool { return false })
				f.act("同心之兰-关闭礼袋面板", func() { page = "friends" })
				f.act("同心之兰-退出好友", func() { page = "courtyard" })
				f.recognize("同心之兰-开好友", func() bool { return page == "courtyard" })
				f.act("同心之兰-开好友", func() { page = "friends" })
				for _, node := range []string{"同心之兰-领一档", "同心之兰-领二档"} {
					f.act(node, func() {
						if tc.issue != "no popup" {
							page = "reward"
						}
					})
				}
				f.act("同心之兰-一档入账", func() { page, claims, claimedFirst = "friends", claims+1, true })
				f.act("同心之兰-二档入账", func() { page, claims = "friends", claims+1 })
				growthName := t.Name() + "/growth"
				if err := res.RegisterCustomRecognition(growthName, bondReplayGrowth(func() (int, int) {
					if score >= target {
						return 0, 0
					}
					return score, target
				})); err != nil {
					t.Fatal(err)
				}
				f.node("同心之兰-记本轮")["custom_recognition"] = growthName

				// Leave BondControl and the real continuation untouched. Mock
				// the party internals so the fixture can inspect each delegated
				// batch without simulating the dungeon's battle screenshots.
				const entry, init = "组队管控-开局", "组队管控-初始化"
				f.override[entry] = map[string]any{"recognition": "DirectHit", "action": "DoNothing", "next": []string{init}, "on_error": []string{}}
				action := t.Name() + "/party"
				if err := res.RegisterCustomAction(action, offlineAction(func(_ *maa.Context, arg *maa.CustomActionArg) bool {
					batches++
					var p struct {
						Role, Flow    string
						Count         int    `json:"limit_count"`
						TeammateNames string `json:"teammate_names"`
						CaptainNames  string `json:"captain_names"`
					}
					if json.Unmarshal([]byte(arg.CustomActionParam), &p) != nil || p.Role != tc.role || p.Flow != "bond" ||
						p.Count != min(tc.limit, target-score) || p.TeammateNames+p.CaptainNames != "测试搭档" ||
						(p.TeammateNames != "") != (tc.role == "captain") {
						t.Errorf("unexpected managed party params: %s (growth %d/%d)", arg.CustomActionParam, score, target)
						return false
					}
					if tc.issue == "party failed" {
						return false
					}
					score += p.Count
					fought += p.Count
					return true
				})); err != nil {
					t.Fatal(err)
				}
				next := []string{"同心之兰-组队后"}
				if tc.issue == "no receipt" {
					next = []string{}
				}
				f.override[init] = map[string]any{"recognition": "DirectHit", "action": "Custom", "custom_action": action, "next": next, "on_error": []string{}}
				f.run("同心之兰", tc.status, tc.reason)
				if batches != tc.batches || claims != tc.claims || (returned == 1) != (tc.status == runstate.Completed) {
					t.Errorf("batches=%d claims=%d return exchanges=%d; want %d batches and %d claims", batches, claims, returned, tc.batches, tc.claims)
				}
				switch tc.issue {
				case "":
					if sent != 1 || fought != 20 {
						t.Errorf("fresh run sent=%d, battles=%d; want 1 exchange and 10+10 battles", sent, fought)
					}
				case "resume":
					if sent != 0 || fought != 13 {
						t.Errorf("resumed run sent=%d, battles=%d; want no duplicate gift and 3+10 battles", sent, fought)
					}
				case "low growth":
					if fought != tc.limit {
						t.Errorf("aggregate battles %d; want cap %d without retry", fought, tc.limit)
					}
				}
			})
		}
	})
}
