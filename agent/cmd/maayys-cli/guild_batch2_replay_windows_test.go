//go:build windows

package main

import (
	"encoding/json"
	"testing"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestNativeBatch2BanquetOutcome(t *testing.T) {
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		for _, tc := range []struct {
			name      string
			page      string
			end, home bool
			want      runstate.Status
			reason    string
		}{
			{"confirmed guild before opening", "guild", false, true, runstate.Deferred, "banquet_not_open"},
			{"flag recognized on retry", "guild", true, true, runstate.Completed, "banquet_finished_or_xp_capped"},
			{"direct join first sees 00:00", "join", true, true, runstate.Completed, "banquet_finished_or_xp_capped"},
			{"starts in expired room", "expired", true, true, runstate.Completed, "banquet_finished_or_xp_capped"},
			{"expired room but courtyard not verified", "expired", true, false, runstate.Failed, "banquet_courtyard_unconfirmed"},
			{"served dishes and confirmed end and return", "room", true, true, runstate.Completed, "banquet_finished_or_xp_capped"},
			{"no terminal sign even after eating", "room", false, true, runstate.Failed, "banquet_end_unconfirmed"},
			{"end but no courtyard", "room", true, false, runstate.Failed, "banquet_courtyard_unconfirmed"},
			{"unknown entrance", "unknown", false, false, runstate.Failed, "banquet_entry_unconfirmed"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				page, meals, flagChecks := tc.page, 0, 0
				f := newGuildBatch2Replay(t, res, tasker)
				f.recognize("寮活动-阴阳寮", func() bool { return page == "guild" || page == "join" })
				f.recognize("寮活动-庭院", func() bool { return page == "home" })
				f.recognize("寮活动-返回箭头", func() bool { return true })
				f.recognize("寮宴会-会场标题", func() bool { return page == "room" || page == "expired" })
				f.recognize("寮宴会-有效倒计时", func() bool { return page == "room" && !(tc.end && meals == 3) })
				f.recognize("寮宴会-尚可取餐", func() bool { return page == "room" && meals < 3 })
				f.recognize("寮宴会-结束证据", func() bool { return page == "expired" || (page == "room" && tc.end && meals == 3) })
				f.recognize("寮宴会-集结旗", func() bool {
					flagChecks++
					return tc.end && page == "guild" && flagChecks >= 3
				})
				f.recognize("寮宴会-参加按钮", func() bool { return page == "join" })
				f.act("寮宴会-直接参加", func() { page = "expired" })
				f.act("寮宴会-进入", func() { page = "room" })
				for _, node := range []string{"寮宴会-收下料理奖励", "寮活动-展开庭院菜单", "寮活动-点阴阳寮"} {
					f.recognize(node, func() bool { return false })
				}
				for _, node := range []string{"寮宴会-取餐左桌", "寮宴会-取餐右侧后桌", "寮宴会-取餐右侧前桌"} {
					f.act(node, func() { meals++ })
				}
				f.act("寮活动-退出阴阳寮", func() {
					if tc.home {
						page = "home"
					}
				})
				f.act("寮宴会-退出席位", func() { page = "guild" })
				f.shorten("寮宴会", "寮宴会-未开放", "寮宴会-在席", "寮宴会-完席归庭", "寮宴会-在寮初检", "寮宴会-在寮复检")
				f.node("寮宴会-在寮初检")["post_delay"] = 0
				f.node("寮宴会-在寮复检")["post_delay"] = 0
				f.run("寮宴会", tc.want, tc.reason)
				if (meals == 3) != (tc.page == "room" || (tc.page == "guild" && tc.end)) {
					t.Fatalf("meals = %d, starting page %q", meals, tc.page)
				}
			})
		}
	})
}

func TestNativeBatch2KirinOutcome(t *testing.T) {
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		for _, tc := range []struct {
			name     string
			opponent string
			result   string
			want     runstate.Status
			reason   string
		}{
			{"unopened", "closed", "", runstate.Skipped, "kirin_not_open"},
			{"already challenged", "done", "", runstate.Skipped, "kirin_already_challenged"},
			{"challenge never opens preparation", "available", "stuck", runstate.Failed, "kirin_battle_unconfirmed"},
			{"battle returns without result", "available", "lobby", runstate.Failed, "kirin_battle_unconfirmed"},
			{"damage settlement after preparation", "available", "result", runstate.Completed, "kirin_result_verified"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				page, attempts, preparations := "lobby", 0, 0
				f := newGuildBatch2Replay(t, res, tasker)
				f.recognize("麒麟-狩猎战大厅", func() bool { return page == "lobby" })
				f.recognize("麒麟-已挑战标志", func() bool { return tc.opponent == "done" })
				f.recognize("麒麟-未开放标志", func() bool { return tc.opponent == "closed" })
				f.recognize("麒麟-可挑战", func() bool { return page == "lobby" && tc.opponent == "available" })
				f.recognize("麒麟-备战按钮", func() bool { return page == "prep" })
				f.recognize("麒麟-战中成绩", func() bool { return page == "battle" })
				f.recognize("麒麟-伤害结算", func() bool { return page == "result" })
				for _, node := range []string{
					"麒麟-胜利标志", "麒麟-战败", "麒麟-点击继续",
					"麒麟-点狩猎战", "麒麟-点神社", "寮活动-点阴阳寮", "寮活动-展开庭院菜单",
					"麒麟-神社", "寮活动-阴阳寮",
				} {
					f.recognize(node, func() bool { return false })
				}
				f.recognize("寮活动-返回箭头", func() bool { return true })
				f.recognize("寮活动-庭院", func() bool { return page == "home" })
				f.override["麒麟-选择换御魂"] = map[string]any{"enabled": false}
				f.override["麒麟-选择不换御魂"] = map[string]any{"enabled": true}
				f.act("麒麟-挑战", func() {
					attempts++
					if tc.result != "stuck" {
						page = "prep"
					}
				})
				f.act("麒麟-点准备", func() {
					preparations++
					page = tc.result
				})
				f.act("麒麟-关闭伤害结算", func() { page = "lobby" })
				f.act("麒麟-退出大厅", func() { page = "home" })
				f.shorten("麒麟", "麒麟-挑战", "麒麟-点准备", "麒麟-战果已见", "麒麟-结算后大厅", "麒麟-等待战果")
				f.run("麒麟", tc.want, tc.reason)
				if attempts > 1 || preparations > 1 || (attempts == 1) != (tc.opponent == "available") ||
					(preparations == 1) != (tc.result == "lobby" || tc.result == "result") {
					t.Fatalf("unexpected clicks: %d challenge, %d prepare", attempts, preparations)
				}
			})
		}
	})
}

func TestNativeKirinStartsWhilePresetApplies(t *testing.T) {
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		for _, presetSucceeds := range []bool{true, false} {
			name := "preset succeeds before auto-start"
			if !presetSucceeds {
				name = "preset failure cannot use the new edge"
			}
			t.Run(name, func(t *testing.T) {
				page := "prep"
				f := newGuildBatch2Replay(t, res, tasker)
				f.recognize("麒麟-备战按钮", func() bool { return page == "prep" })
				f.recognize("出战队伍预设-打开备战预设", func() bool { return page == "prep" })
				f.recognize("麒麟-战中成绩", func() bool {
					if page != "battle" {
						return false
					}
					page = "result"
					return true
				})
				f.recognize("麒麟-伤害结算", func() bool { return page == "result" })
				f.recognize("麒麟-狩猎战大厅", func() bool { return page == "lobby" })
				f.recognize("麒麟-点击继续", func() bool { return false })
				f.recognize("寮活动-庭院", func() bool { return page == "home" })
				f.recognize("寮活动-返回箭头", func() bool { return true })
				key := t.Name() + "/preset"
				calls, prepareClicks := 0, 0
				if err := res.RegisterCustomAction(key, offlineAction(func(*maa.Context, *maa.CustomActionArg) bool {
					calls++
					page = "battle"
					return presetSucceeds
				})); err != nil {
					t.Fatal(err)
				}
				preset := f.node("麒麟-切换出战阵容")
				preset["custom_action"], preset["post_delay"] = key, 0
				f.act("麒麟-点准备", func() { prepareClicks++ })
				f.act("麒麟-关闭伤害结算", func() { page = "lobby" })
				f.act("麒麟-退出大厅", func() { page = "home" })
				f.shorten("麒麟-切换出战阵容", "麒麟-等待战果", "麒麟-战果已见", "麒麟-结算后大厅")
				if presetSucceeds {
					f.run("麒麟-切换出战阵容", runstate.Completed, "kirin_result_verified")
				} else {
					f.run("麒麟-切换出战阵容", runstate.Failed, "kirin_preset_unconfirmed")
				}
				if calls != 1 || prepareClicks != 0 {
					t.Fatalf("preset calls=%d, prepare clicks=%d", calls, prepareClicks)
				}
			})
		}
	})
}

type guildBatch2Replay struct {
	t        *testing.T
	res      *maa.Resource
	tasker   *maa.Tasker
	override map[string]any
}

func newGuildBatch2Replay(t *testing.T, res *maa.Resource, tasker *maa.Tasker) guildBatch2Replay {
	return guildBatch2Replay{t: t, res: res, tasker: tasker, override: map[string]any{}}
}

func (f guildBatch2Replay) node(name string) map[string]any {
	if f.override[name] == nil {
		f.override[name] = map[string]any{}
	}
	return f.override[name].(map[string]any)
}

func (f guildBatch2Replay) recognize(node string, matches func() bool) {
	f.t.Helper()
	name := f.t.Name() + "/recognize/" + node
	if err := f.res.RegisterCustomRecognition(name, replayRecognition(matches)); err != nil {
		f.t.Fatal(err)
	}
	n := f.node(node)
	n["recognition"], n["custom_recognition"] = "Custom", name
}

func (f guildBatch2Replay) act(node string, step func()) {
	f.t.Helper()
	name := f.t.Name() + "/act/" + node
	if err := f.res.RegisterCustomAction(name, offlineAction(func(*maa.Context, *maa.CustomActionArg) bool {
		step()
		return true
	})); err != nil {
		f.t.Fatal(err)
	}
	n := f.node(node)
	n["action"], n["custom_action"], n["post_delay"] = "Custom", name, 0
}

func (f guildBatch2Replay) shorten(nodes ...string) {
	for _, node := range nodes {
		n := f.node(node)
		n["timeout"], n["rate_limit"] = 800, 20
	}
}

func (f guildBatch2Replay) run(entry string, want runstate.Status, reason string) {
	f.t.Helper()
	out, release := runstate.Bind(f.tasker)
	defer release()
	job := f.tasker.PostTask(entry, f.override).Wait()
	got := out.Outcome()
	raw, _ := got.Metrics["task_reason"].(json.RawMessage)
	var actualReason string
	_ = json.Unmarshal(raw, &actualReason)
	if !got.Reported || got.Status != want || actualReason != reason || job.Success() != (want != runstate.Failed) {
		detail, err := job.GetDetail()
		f.t.Fatalf("want %s/%s: job=%v; outcome=%+v; hits=%v", want, reason, job.Status(), got, completedFixtureNodes(f.t, detail, err))
	}
}
