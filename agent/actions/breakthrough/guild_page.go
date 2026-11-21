package breakthrough

import (
	"fmt"
	"image"
	"image/color"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const guildPageBudget = 20

func turnGuildPage(ctx *maa.Context) bool {
	n, err := ctx.GetHitCount(guildSwipe)
	if err != nil {
		return guildPageFailed(ctx, "guild_page_counter_failed")
	}
	if n >= guildPageBudget {
		return guildPageFailed(ctx, "guild_page_budget_exhausted")
	}
	ctrl := ctx.GetTasker().GetController()
	if ctrl == nil {
		return guildPageFailed(ctx, "guild_page_capture_failed")
	}
	img, err := ctrl.CacheImage() // The frame on which guildSwipe's board recognition hit.
	if err != nil || img == nil {
		return guildPageFailed(ctx, "guild_page_capture_failed")
	}
	reason := advanceGuildPage(guildPageSample(img), func() ([]color.RGBA, string) {
		task, err := ctx.RunTask("寮突-执行翻页")
		if err != nil || task == nil || !task.Status.Success() {
			return nil, "guild_page_swipe_failed"
		}
		if !ctrl.PostScreencap().Wait().Status().Success() {
			return nil, "guild_page_capture_failed"
		}
		img, err := ctrl.CacheImage()
		if err != nil || img == nil {
			return nil, "guild_page_capture_failed"
		}
		rec, err := ctx.RunRecognition(guildSwipe, img)
		if err != nil || rec == nil || !rec.Hit {
			return nil, "guild_page_unconfirmed"
		}
		return guildPageSample(img), ""
	})
	return reason == "" || guildPageFailed(ctx, reason)
}

func guildPageFailed(ctx *maa.Context, reason string) bool {
	fmt.Printf("寮突破：未能继续扫描 (%s)\n", reason)
	runstate.SetMetric(ctx, "battle_reason", reason)
	runstate.Report(ctx, runstate.Failed)
	return false
}

func advanceGuildPage(before []color.RGBA, swipe func() ([]color.RGBA, string)) string {
	for range 2 {
		after, reason := swipe()
		if reason != "" {
			return reason
		}
		if guildPageChanged(before, after) {
			return ""
		}
	}
	return "guild_page_no_progress"
}

func guildPageSample(img image.Image) []color.RGBA {
	b := img.Bounds()
	// Sample the opponent board, not the remaining chances or the guild header.
	var sample []color.RGBA
	for y := 125; y < 535; y += 8 {
		for x := 423; x < 1095; x += 8 {
			sample = append(sample, color.RGBAModel.Convert(img.At(b.Min.X+x*b.Dx()/1280, b.Min.Y+y*b.Dy()/720)).(color.RGBA))
		}
	}
	return sample
}

func guildPageChanged(before, after []color.RGBA) bool {
	if len(before) == 0 || len(before) != len(after) {
		return false
	}
	changed := 0
	for i, a := range before {
		b := after[i]
		if abs(int(a.R)-int(b.R))+abs(int(a.G)-int(b.G))+abs(int(a.B)-int(b.B)) >= 72 {
			changed++
		}
	}
	return changed*25 >= len(before)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
