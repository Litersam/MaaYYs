//go:build windows

package main

import (
	"image"
	"image/draw"
	"path/filepath"
	"testing"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func partyCourtFrame(t *testing.T, root string) *image.RGBA {
	t.Helper()
	crop, err := loadPNG(filepath.Join(root, "resource_pack", "base", "image", "导航", "导航-庭院-识别是否位于庭院-模板匹配_1.png"))
	if err != nil {
		t.Fatal(err)
	}
	return drawReviewCrop(crop, image.Pt(1200, 640))
}

func TestNativePartyCannotReuseInvitationAfterSettlement(t *testing.T) {
	const gi = "tasks/Component/GeneralInvite/gi/"
	accept := historicalPartyCrop(t, gi+"gi_i_accept.png")
	defaultAccept := historicalPartyCrop(t, gi+"gi_i_accept_default.png")
	reject := historicalPartyCrop(t, gi+"gi_i_reject.png")
	roomTitle := historicalPartyCrop(t, gi+"gi_gi_in_room.png")
	prepare := historicalPartyCrop(t, "tasks/Component/GeneralBattle/gb/gb_prepare_highlight.png")
	nickname := syntheticGhostFrame(t)
	c := &partyFrameController{}
	withPartyFrameReplay(t, c, func(root string, res *maa.Resource, tasker *maa.Tasker) {
		court := partyCourtFrame(t, root)
		invite := func(allowed bool) *image.RGBA {
			f := blankPartyFrame()
			draw.Draw(f, f.Bounds(), court, image.Point{}, draw.Src)
			pastePartyCrop(f, accept, 113, 225)
			pastePartyCrop(f, defaultAccept, 205, 223)
			pastePartyCrop(f, reject, 12, 226)
			if allowed {
				draw.Draw(f, image.Rect(12, 158, 300, 198), image.White, image.Point{}, draw.Src)
				draw.Draw(f, image.Rect(30, 162, 110, 192), nickname, image.Pt(145, 52), draw.Src)
			} else {
				drawPartyCaption(t, f, "陌生人邀请你组队", 12, 162)
			}
			return f
		}
		back, err := loadPNG(filepath.Join(root, "resource_pack", "base", "image", "导航", "导航-庭院-点击返回按钮-模板匹配_1.png"))
		if err != nil {
			t.Fatal(err)
		}
		firstReady := drawReviewCrop(prepare, image.Pt(1128, 536))
		strangerReady := drawReviewCrop(prepare, image.Pt(1128, 536))
		pastePartyCrop(strangerReady, back, 21, 19)
		sameRoom := drawReviewCrop(prepare, image.Pt(1128, 536))
		pastePartyCrop(sameRoom, roomTitle, 92, 17)
		draw.Draw(sameRoom, image.Rect(195, 366, 275, 396), nickname, image.Pt(145, 52), draw.Src)
		victory := blankPartyFrame()
		drawPartyCaption(t, victory, "本次通关时间", 530, 575)
		readyArea := image.Rect(1116, 535, 1253, 629)
		settleArea := image.Rect(608, 641, 809, 720)
		rejectArea := image.Rect(12, 226, 76, 287)
		backArea := image.Rectangle{Min: image.Pt(21, 19), Max: image.Pt(21, 19).Add(back.Bounds().Size())}
		for _, tc := range []struct {
			name    string
			auto    bool
			area    image.Rectangle
			stage   []image.Image
			extra   []image.Rectangle
			want    runstate.Status
			battles string
			reason  string
		}{
			{"ordinary rejects stranger", false, image.Rect(113, 225, 176, 297),
				[]image.Image{invite(false), strangerReady, court}, []image.Rectangle{rejectArea, backArea},
				runstate.Failed, "1", `"party_member_entry_unverified"`},
			{"default rejects stranger", true, image.Rect(205, 223, 266, 291),
				[]image.Image{invite(false), strangerReady, court}, []image.Rectangle{rejectArea, backArea},
				runstate.Failed, "1", `"party_member_entry_unverified"`},
			{"default renews from the same captain seat", true, image.Rect(205, 223, 266, 291),
				[]image.Image{sameRoom, victory, court}, []image.Rectangle{readyArea, settleArea},
				runstate.Completed, "2", `"party_battle_budget_reached"`},
			{"courtyard ends default continuity", true, image.Rect(205, 223, 266, 291),
				[]image.Image{court}, nil, runstate.Deferred, "1", `"party_room_disbanded"`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				areas := append([]image.Rectangle{tc.area, readyArea, settleArea}, tc.extra...)
				frames := append([]image.Image{firstReady, victory}, tc.stage...)
				c.mu.Lock()
				c.clicks = nil
				c.frame = invite(true)
				c.step = func(p image.Point) image.Image {
					i := len(c.clicks) - 1
					if i < len(areas) && p.In(areas[i]) {
						return frames[i]
					}
					return nil
				}
				c.mu.Unlock()
				collector, release := runstate.Bind(tasker)
				defer release()
				job := tasker.PostTask("组队管控-初始化", map[string]any{
					"组队管控-初始化": map[string]any{
						"custom_action_param": map[string]any{"role": "member", "captain_names": "涂壁", "limit_count": 2, "auto_accept": tc.auto},
					},
					"组队管控-庭院等待": map[string]any{"timeout": 3500, "rate_limit": 30},
					"组队管控-战斗等待": map[string]any{"timeout": 3500, "rate_limit": 30},
					"组队管控-战后去向": map[string]any{"timeout": 3500, "rate_limit": 30},
					"组队管控-安全归庭": map[string]any{"timeout": 3500, "rate_limit": 30},
				}).Wait()
				out, clicks := collector.Outcome(), c.recorded()
				if job.Success() == (tc.want == runstate.Failed) || !out.Reported || out.Status != tc.want ||
					!partyMetricEquals(out, "task_reason", tc.reason) ||
					!partyMetricEquals(out, "party_battles", tc.battles) || len(clicks) != len(areas) {
					t.Fatalf("reused member authorization: job=%v outcome=%+v clicks=%v", job.Status(), out, clicks)
				}
				for i, click := range clicks {
					if !click.In(areas[i]) {
						t.Errorf("unexpected click %d: %v outside %v", i, click, areas[i])
					}
				}
			})
		}
	})
}

func TestNativePartyManualGuestNeedsNewRound(t *testing.T) {
	prepare := historicalPartyCrop(t, "tasks/Component/GeneralBattle/gb/gb_prepare_highlight.png")
	ready := drawReviewCrop(prepare, image.Pt(1128, 536))
	victory := blankPartyFrame()
	drawPartyCaption(t, victory, "本次通关时间", 530, 575)
	c := &partyFrameController{}
	withPartyFrameReplay(t, c, func(root string, _ *maa.Resource, tasker *maa.Tasker) {
		_, tasks, err := loadTaskDefinitions(root)
		if err != nil {
			t.Fatal(err)
		}
		court := partyCourtFrame(t, root)
		for _, tc := range []struct {
			name    string
			start   image.Image
			limit   int
			battles string
			repeat  bool
			want    runstate.Status
		}{
			{"opens on a stale result", victory, 1, "0", false, runstate.Deferred},
			{"one genuine result is enough for one round", ready, 1, "1", false, runstate.Completed},
			{"same victory after a blank frame", ready, 2, "1", true, runstate.Deferred},
		} {
			t.Run(tc.name, func(t *testing.T) {
				c.mu.Lock()
				c.clicks, c.afterCapture = nil, nil
				c.frame = tc.start
				c.step = func(p image.Point) image.Image {
					i := len(c.clicks)
					switch {
					case p.In(image.Rect(1116, 535, 1253, 629)):
						return victory
					case p.In(image.Rect(608, 641, 809, 720)):
						if i == 2 && tc.repeat {
							c.afterCapture = []image.Image{victory}
							return blankPartyFrame()
						}
						return court
					}
					return nil
				}
				c.mu.Unlock()
				task := tasks["组队刷本"]
				overrides := resolveTaskOverride(task, map[string]any{"组队刷本-组队身份": "房客", "挑战次数": tc.limit})
				overrides["组队刷本-队友"] = map[string]any{"enabled": true, "timeout": 1600, "rate_limit": 30}
				overrides["组队刷本-房客新一局"] = map[string]any{"timeout": 2000, "rate_limit": 30}
				overrides["组队刷本-手动离开结算"] = map[string]any{"timeout": 2200, "rate_limit": 30}
				overrides["组队管控-安全归庭"] = map[string]any{"timeout": 2000, "rate_limit": 30}
				collector, release := runstate.Bind(tasker)
				defer release()
				job := tasker.PostTask(task.Entry, overrides).Wait()
				if out := collector.Outcome(); !job.Success() || !out.Reported || out.Status != tc.want ||
					!partyMetricEquals(out, "party_battles", tc.battles) || (tc.repeat && len(c.recorded()) < 3) {
					t.Fatalf("manual guest: job=%v outcome=%+v clicks=%v", job.Status(), out, c.recorded())
				}
			})
		}
	})
}
