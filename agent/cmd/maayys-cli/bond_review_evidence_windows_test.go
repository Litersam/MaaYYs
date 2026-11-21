//go:build windows

package main

import (
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/draw"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const bondVisualEntry = "同心之兰"

func TestNativeBatch3BondPanelClaim(t *testing.T) {
	frames := newBondVisualFrames(t, testRepoRoot(t))
	gift := image.Rect(1080, 108, 1128, 153)
	close := image.Rect(1135, 75, 1219, 154)
	dismiss := image.Rect(604, 59, 675, 84)
	type transition struct {
		target image.Rectangle
		page   string
	}
	for _, tc := range []struct {
		name, initial string
		steps         []transition
		status        runstate.Status
		reason        string
		nodes         []string
	}{
		{"panel", "panel", []transition{{close, "friends"}, {gift, "newly claimed"}, {close, "friends"}, {close, "courtyard"}},
			runstate.Completed, "bond_growth_reward_claimed", []string{"检查面板", "关闭二档满成长面板", "面板后领二档", "二档点击后已领", "归庭领奖完成"}},
		{"unripe", "unripe", []transition{{gift, "panel"}, {close, "friends"}, {gift, "popup"}, {dismiss, "friends"}, {close, "courtyard"}},
			runstate.Completed, "bond_growth_reward_claimed", []string{"打开礼袋", "检查面板", "关闭二档满成长面板", "面板后领二档", "二档入账", "归庭领奖完成"}},
		{"recheck after tier-one reward with unreadable friend growth", "first ready", []transition{
			{gift, "popup"}, {dismiss, "unripe"}, {gift, "panel"}, {close, "unripe"}, {gift, "popup"}, {dismiss, "friends"}, {close, "courtyard"},
		}, runstate.Completed, "bond_growth_reward_claimed", []string{
			"一档入账", "再次打开礼袋", "复核回赠面板", "关闭二档满成长面板", "面板后领二档", "二档入账", "归庭领奖完成",
		}},
		{"preclaimed panel", "newly claimed", []transition{{close, "friends"}, {close, "courtyard"}},
			runstate.Skipped, "bond_rewards_already_claimed", []string{"今日已领", "归庭已领"}},
		{"full but cooling", "full cooldown", []transition{{close, "friends"}, {close, "courtyard"}},
			runstate.Skipped, "bond_cooldown", []string{"冷却", "归庭冷却"}},
		{"partner changed after close", "panel", []transition{{close, "different friend"}},
			runstate.Failed, "bond_partner_unconfirmed", []string{"检查面板", "关闭二档满成长面板"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &partyFrameController{frame: frames[tc.initial]}
			c.step = func(p image.Point) image.Image {
				i := len(c.clicks) - 1
				if i >= len(tc.steps) || !p.In(tc.steps[i].target) {
					return nil
				}
				return frames[tc.steps[i].page]
			}
			withPartyFrameReplay(t, c, func(_ string, _ *maa.Resource, tasker *maa.Tasker) {
				collector, release := runstate.Bind(tasker)
				defer release()
				// Supply only task parameters and frames. Maa runs all
				// recognizers, actions, edges and limits from the resource.
				job := tasker.PostTask(bondVisualEntry, map[string]any{
					bondVisualEntry: map[string]any{"custom_action_param": map[string]any{
						"role": "captain", "teammate_names": "测试搭档",
					}},
				}).Wait()
				out, clicks := collector.Outcome(), c.recorded()
				t.Logf("%s: clicks=%v status=%s reason=%s", maa.Version(), clicks, out.Status, out.Metrics["task_reason"])
				if job.Success() != (tc.status != runstate.Failed) || !out.Reported || out.Status != tc.status ||
					!partyMetricEquals(out, "task_reason", `"`+tc.reason+`"`) {
					t.Errorf("job=%v outcome=%+v; want %s / %s", job.Status(), out, tc.status, tc.reason)
				}
				if len(clicks) != len(tc.steps) {
					t.Errorf("got %d clicks; want %d", len(clicks), len(tc.steps))
				}
				for i := 0; i < min(len(clicks), len(tc.steps)); i++ {
					if !clicks[i].In(tc.steps[i].target) {
						t.Errorf("click %d: %v outside %v", i, clicks[i], tc.steps[i].target)
					}
				}
				detail, err := job.GetDetail()
				nodes := completedFixtureNodes(t, detail, err)
				for _, suffix := range tc.nodes {
					if nodes[bondVisualEntry+"-"+suffix] != 1 {
						t.Errorf("expected exactly one completed %s node, got %v", suffix, nodes)
					}
				}
			})
		})
	}
}

func TestNativeBatch3BondVisualEvidence(t *testing.T) {
	withNativeBattleFixture(t, func(root string, res *maa.Resource, tasker *maa.Tasker) {
		t.Logf("native runtime: %s", maa.Version())
		frames := newBondVisualFrames(t, root)
		params := map[string]any{"role": "captain", "teammate_names": "测试搭档"}
		pipelineFile := filepath.Join(root, "resource_pack", "base", "pipeline", "战斗", "同心之兰.json")
		raw, err := os.ReadFile(pipelineFile)
		if err != nil {
			t.Fatal(err)
		}
		var production map[string]json.RawMessage
		if err := json.Unmarshal(raw, &production); err != nil {
			t.Fatal(err)
		}

		t.Run("production page conditions", func(t *testing.T) {
			const probe = "BondVisualPageConditions"
			if err := res.RegisterCustomAction(probe, offlineAction(func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
				for _, check := range []struct {
					node  string
					pages []string
				}{
					{"寮活动-庭院", []string{"courtyard", "friends", "panel"}},
					{"同心之兰-庭院", []string{"courtyard"}},
					{"同心之兰-开好友", []string{"courtyard"}},
					{"同心之兰-好友页", []string{"friends", "panel"}},
					{"同心之兰-搭档好友名", []string{"friends", "panel"}},
					{"同心之兰-面板标题", []string{"panel"}},
					{"同心之兰-打开礼袋", []string{"friends"}},
					{"同心之兰-再次打开礼袋", []string{"friends"}},
					{"同心之兰-关闭礼袋面板", []string{"panel"}},
					{"同心之兰-退出好友", []string{"friends"}},
					{"同心之兰-二档满成长", []string{"friends", "panel"}},
					{"同心之兰-领二档", []string{"friends"}},
				} {
					for _, page := range []string{"courtyard", "friends", "panel", "unknown"} {
						d, err := ctx.RunRecognition(check.node, frames[page])
						if want := slices.Contains(check.pages, page); err != nil || (d != nil && d.Hit) != want {
							t.Errorf("%s on %s: want hit=%t, got hit=%t, err=%v", check.node, page, want, d != nil && d.Hit, err)
						}
					}
				}
				return !t.Failed()
			})); err != nil {
				t.Fatal(err)
			}
			job := tasker.PostTask(bondVisualEntry, map[string]any{
				bondVisualEntry: map[string]any{"custom_action_param": params, "next": []string{probe}},
				probe:           map[string]any{"action": "Custom", "custom_action": probe},
			}).Wait()
			if !job.Success() {
				t.Fatal("native page probe failed")
			}
		})

		for _, test := range []struct {
			name, initial, afterClaim, reason string
			status                            runstate.Status
			clicks                            []string
		}{
			{"open friends, see cooldown, and return", "courtyard", "", "bond_cooldown", runstate.Skipped,
				[]string{"开好友", "打开礼袋", "关闭礼袋面板", "退出好友"}},
			{"claimed before any click", "already claimed", "", "bond_rewards_already_claimed", runstate.Skipped,
				[]string{"退出好友"}},
			{"claimed text and cooldown appear after the click without a popup", "friends", "newly claimed", "bond_growth_reward_claimed", runstate.Completed,
				[]string{"领二档", "关闭礼袋面板", "退出好友"}},
			{"reward popup appears after the click", "friends", "popup", "bond_growth_reward_claimed", runstate.Completed,
				[]string{"领二档", "二档入账", "退出好友"}},
			{"only cooldown appears after the click", "friends", "cooldown", "bond_reward_unconfirmed", runstate.Failed,
				[]string{"领二档"}},
		} {
			t.Run(test.name, func(t *testing.T) {
				page := test.initial
				f := newFastBondReplay(t, root, res, tasker)
				f.node(bondVisualEntry)["custom_action_param"] = params
				f.node("同心之兰-领二档")["timeout"] = 1500
				// Only dispatch to the fixed-frame recognition and intercept
				// input. The recognizer receives the exact production definition
				// and a real image, including for all nested BondProbe calls.
				for _, suffix := range []string{
					"核对好友", "开好友", "打开礼袋", "检查面板", "冷却", "今日已领", "领二档",
					"二档点击后已领", "二档入账", "关闭礼袋面板", "退出好友", "归庭冷却", "归庭已领", "归庭领奖完成",
				} {
					node := bondVisualEntry + "-" + suffix
					if err := f.res.RegisterCustomRecognition(t.Name()+"/"+suffix, bondVisualRecognition(func(ctx *maa.Context) *maa.RecognitionDetail {
						d, err := ctx.RunRecognition(node, frames[page], map[string]json.RawMessage{node: production[node]})
						if err != nil {
							t.Errorf("run %s on %s: %v", node, page, err)
							return nil
						}
						return d
					})); err != nil {
						t.Fatal(err)
					}
					n := f.node(node)
					n["recognition"], n["custom_recognition"] = "Custom", t.Name()+"/"+suffix
				}
				var clicks []string
				for suffix, next := range map[string]string{
					"开好友": "unripe", "打开礼袋": "cooldown", "关闭礼袋面板": "friends",
					"退出好友": "courtyard", "领二档": test.afterClaim, "二档入账": "friends",
				} {
					f.act(bondVisualEntry+"-"+suffix, func() {
						clicks = append(clicks, suffix)
						page = next
					})
				}
				f.run(bondVisualEntry, test.status, test.reason)
				if !slices.Equal(clicks, test.clicks) || test.status != runstate.Failed && page != "courtyard" {
					t.Fatalf("clicks=%v final page=%s; want clicks=%v and a verified courtyard on success", clicks, page, test.clicks)
				}
			})
		}
	})
}

type bondVisualRecognition func(*maa.Context) *maa.RecognitionDetail

func (f bondVisualRecognition) Run(ctx *maa.Context, _ *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	d := f(ctx)
	if d == nil || !d.Hit {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: d.Box}, true
}

func newBondVisualFrames(t *testing.T, root string) map[string]image.Image {
	t.Helper()
	templates := map[string]image.Image{}
	for name, path := range map[string]string{
		"courtyard": filepath.Join("导航", "导航-庭院-识别是否位于庭院-模板匹配_1.png"),
		"friends":   filepath.Join("导航", "导航-好友-点击好友_1.png"),
		"close":     filepath.Join("通用图标", "关闭.png"),
	} {
		template, err := loadPNG(filepath.Join(root, "resource_pack", "base", "image", path))
		if err != nil {
			t.Fatal(err)
		}
		templates[name] = template
	}
	frames := map[string]image.Image{}
	for page, flags := range map[string]string{
		"unknown": "", "courtyard": "home", "friends": "home friend ready", "panel": "home friend panel ready",
		"unripe": "home friend", "already claimed": "home friend ready claimed", "popup": "popup",
		"cooldown": "home friend panel cooling", "newly claimed": "home friend panel ready claimed cooling",
		"full cooldown": "home friend panel ready cooling", "different friend": "home wrong-friend ready",
		"first ready": "home friend first",
	} {
		img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
		draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
		for _, flag := range strings.Fields(flags) {
			switch flag {
			case "home":
				// Keep both actual courtyard templates behind Friends to prove
				// positive template evidence alone cannot authorize navigation.
				bondDrawVisual(img, templates["courtyard"], 100, 572)
				bondDrawVisual(img, templates["friends"], 880, 620)
			case "friend", "wrong-friend":
				bondDrawVisual(img, templates["close"], 1150, 85)
				bondDrawGlyph(t, img, "好友", 600, 45)
				if flag == "friend" {
					bondDrawGlyph(t, img, "测试搭档", 620, 96)
				} else {
					bondDrawGlyph(t, img, "同心之兰", 620, 96)
				}
			case "panel":
				bondDrawGlyph(t, img, "同心之兰", 825, 150)
			case "first":
				bondDrawGlyph(t, img, "10/10", 1068, 137)
			case "ready":
				bondDrawGlyph(t, img, "20/20", 1068, 137)
				bondDrawGlyph(t, img, "20/20", 715, 610)
			case "claimed":
				bondDrawGlyph(t, img, "同心之兰奖励已全部领取", 730, 295)
			case "cooling":
				bondDrawGlyph(t, img, "同心之兰冷却中", 730, 410)
			case "popup":
				bondDrawGlyph(t, img, "获得奖励", 555, 250)
			}
		}
		frames[page] = img
	}
	return frames
}

func bondDrawVisual(dst draw.Image, src image.Image, x, y int) {
	at := image.Pt(x, y)
	draw.Draw(dst, src.Bounds().Sub(src.Bounds().Min).Add(at), src, src.Bounds().Min, draw.Over)
}

func bondDrawGlyph(t *testing.T, dst draw.Image, text string, x, y int) {
	t.Helper()
	g := bondVisualGlyphs[text]
	z := flate.NewReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(g.bits)))
	defer z.Close()
	rows, err := io.ReadAll(z)
	if err != nil || len(rows) != g.stride*42 || g.stride == 0 {
		t.Fatalf("invalid synthetic glyph %q: %v", text, err)
	}
	for i, b := range rows {
		for bit := range 8 {
			if b&(0x80>>bit) != 0 {
				dst.Set(x+8*(i%g.stride)+bit, y+i/g.stride, image.Black)
			}
		}
	}
}

// Synthetic 42 px-high monochrome masks (Microsoft YaHei, 30 px; numbers 22 px).
// Keeping the glyphs in the test removes the dependency on locally installed
// fonts. These are not extracted game assets.
var bondVisualGlyphs = map[string]struct {
	stride int
	bits   string
}{
	"好友":          {8, "ndAxDsIwDAXQX1lKGKykY5EqwREyduAwOUJHBiRyA64UToKPkLEDUnAKSIgNPPhJXx5sA/+VaS1+6Gtp8lugQJqd1FrlLJSf2oQMbKJHc4gO3apOpi9rRX+rC6mhGQkhMyiaVTsbbFUTGD4x3KSZyuMAUkcXYIUx2AhTGBNl8JExk2DsVV8w6YZyOmh2765ll/a04JKpvG6UX//yAA=="},
	"测试搭档":        {15, "vdKxSgNBEADQWRP2IqxJu4ET0tnOaXEJHMlVfkeqsz1II1i4UUhsxB/wI1JarihqcXA/YLFVUhorLQ7H3ewlf6DDMsyDYWZhF+D/At3RiExx6Nk6heOazXMyaCBWjle8ndASc4j0ExGbc9HWGfYBleP1LGxahvWo6UxCkeE+0UeFOKpe0JE5xpbLPLXc7O0opk57ZstD5a6RF1m0dhzohmW/5ltuGnOqQtuMlt8mD1ZUtSwjWsCnDgMDAjyDV6Jg4YkSO+Xl1+0DiNaGUjwDO7gHITxBA3RvQITvno9UDewiKTlGnpFjyPHEUQmmQMghYCxlWhNjhUMpxltONCbdYkdjMBntONE5XvzwsZ3M7qg6U0frieJlzT0ITA8a5dQ3+2ey1SaJv/sJvw=="},
	"同心之兰":        {15, "vdGxbsJADAbg/zDiOqDAyBCRPkIkFoZK8ChBeQHGDh0s8VodTuJBGokXyJgB4dp3FygrQ0/O8Em2z74A/3mWz9zqt7fgnUiHUtlpuBA5Vx41iGugQZEKtpk+tSs9jEQgkRveikw2LkSuG7k15CaavR7ZxuIPm8CSW1C6KfEC4jhH4hmO4UKkDMYAzw8iYBGXqcPqcLbC9Z0nG/TTJuszj+j/cPruujG5JcxyY+t8MFYY2Ri/MoMuqK8zPJFG2vpc0veDTgZfMSp9kRpVB+kLL/KTaJtNIcIv/9QXzi8="},
	"10/10":       {9, "Y2CgDAiwH2BjYvjBwFDA/4CHj/EPA+MHmQIeO+Y+BuYNEgY8ZcxsDEwGFhYSbuzsDAwGBhISbGzMqAwD3IwEXAwJgwNsQJMZDGQKgIw+IIP/wQE2oO0MBuwHG9hA7mFgYGyAuhHOYKbQs1QFAA=="},
	"20/20":       {8, "rc9BCsIwEIXhGQK6GZptFoV6hLfMzqtM6QVygEIjAc8VUOw14g1cuijFCOoBiv/m275HtC0bR+ofNh7zSvOzy7jPdGt9OfSJrgKlIZ2ihEADVxU/L5zIKfitV+xNtSgaw2fOirZqogLVHSkCJ2kIqjyLkC9TWcUuXZ7K6OxiY53iPpO+YuOlv/QC"},
	"同心之兰奖励已全部领取": {42, "7dS/b9NAFAfw7/WiuINld7yqVlz+AiyxBDVS+6cYlZEh3TJUyplIsESwFgkBfwJ/QIdnRQoMCFYYoEZITAi8YYkqx7s7uw3twg6nJEqcj/N+3LsA/9dfrq3Lt5TzS3bxUV+RQ34e2IcGVZeygiCg36J9Yyok7jIqQbtEMKtMGnMOYS+lTZSaH2y8DFmO+SH1pDR1J2XtZGylMTZQjshnMJR6tDA1ueghgiH/zona0Zkonrcy8FUlAaJXyDoZJVZm28RyLp2UEhxuhc0IvbfISdi0QiQxej2VZZSvSW1lbJP7qKtOTgIdRirPKi9vmVUuxQaHGzj5uqKz8+yzZlkL6ocqvzs1pZn3nTx0iY5s0yQd6VZKQt1Xir7fZPmg76IfQvo2ATHtYQEnY42RZLkMURat/AKpXeuBYxp0csBN3Mi2i1mYXcgFhHYbB9Q0cpJT3jTm69R80PedDJGZxkpC4IbhoKytLG1xIEzNe97BdckXYz8H7wiLJitt7bBR1Nh84xtnLEndWdgkB14ucUUOvUycnNkdndh9sqGxUFailQr3OPpMdXKM2kv5h0yNCvGGZXHSyt6ucE3iEkyzUGOWQvA8XsqXVh5KnmtfeitzKwuWSqrwxpylrm3td6xM2zPAdyy95JMCK4/meVnUleuSlcetjIhltS73wjEfhMrOLcsQTSuTXCwVsZTwUqVoYlPUnZSdnAzFo3W500T4FIvZiZ9knchTD0U1EPNtKskOopN1JB5DPEycFKYJUs3N47mnoHy6U1B7BLhLCIKZFs8ilmkFU0eBMWf89QrCPPm1r6fmvJMyEiROA83SbloPxmg/znu3f3YtsxWBE6tEIwnXltjawLqEwoF4ce0f6p9avwE="},
	"同心之兰冷却中":     {26, "1dLNSsNAEAfw/3ZLohDSHnOoaR8h4qWHWvVNUvoC8ZaD4BbBpyp0IVfBRzDi1UOOKajj7G5W24p3HdKv/cHsfBT4tzH8eZS7jym/Ls2jgAyBkwuiGiP+WvMjNFDsSQRzgkKqCUoEgjRLZjh2GadSlXjdk9BVMQoxEw8syouUkEQfOIqRynvQVlX804kyMiB6C0Pp5Yw+cil6nC410otXirNVyAMjS3vRzLZ2rutOTLYlpCubo6yLHXmBVLZVQNT5bEcqCAXTP4cuEi/UGtEIlZ/hnkBj4Ocu6N2JThaVuSS1532+akfuzIRKM4cmuKU6Em4GTgo0VqJD6U+ELZr3IXRy5bMtJS+rKy3i1pJnX9vCyNhVlgxDZAu3Ba7ayE0n8xTF9coKv0lO0nZ9DtbITzfqW2QnmWzwuN3A71SN5LoTtII350VQG44VxtxEgTmak07GNaiJQ6InlomZ37EXbedF5DrixQv19e89iN/lr8Qn"},
	"获得奖励":        {15, "vdIxT8JAFAfw/1nSLo+C25FUy0cocelAUtz9ELg74NZEEy+SwEJwdTDyFUxYHEtIcCHOjEVXBpysScPzFcRvoG+4y+/yv7y73AH/VW29nciWwdryoKD7SyfiWyJU+B03zJkdcX/g854UqO48YvoJe4Eyq461ZxixOUttgr8BFDrC8sItWG+dmDcJ05DKPHXDtLF8ETYdXfAoCTo9CefWEwWWWzFBnaTvXKUFHQSQvndkJQV9nzO5RIl8WbXoShgyp5SDoi59mABxwfaWpyZEBluSoEaXLBNjIcSOThKre9gOMw8afXKTC9SYP7fskZfMC64Rg857FCev0FXZuxIOaT2ZQTdtqBm88wG1Jn1o14bTNXo5IEwto0s2jlWqJw+kZirR0jfHdW3y7EWPWGu1qaTwv0bjLAoRa5U78gTj0eUaVTnerg5R/8O/8A0="},
}
