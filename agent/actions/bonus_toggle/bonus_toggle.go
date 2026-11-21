package bonus_toggle

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"maa-yys-agent/internal/vision"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/rs/zerolog/log"
)

type BonusToggleAction struct{}

var labels = map[string]string{
	"exp_100%":   `经验增加100[%％]`,
	"exp_50%":    `经验增加50[%％]`,
	"gold_100%":  `金币增加100[%％]`,
	"gold_50%":   `金币增加50[%％]`,
	"drop_awake": `觉醒副本掉落额外的觉醒材料`,
	"drop_soul":  `八岐大蛇掉落额外的御魂材料`,
}

const (
	clickNode    = "通用加成-拨动"
	activeNode   = "通用加成-启用色"
	inactiveNode = "通用加成-停用色"
	newcomerNode = "通用加成-新手冲突"
	cleanupEntry = "通用加成-归位"
	cleanupNode  = "通用加成-关闭本轮加成"
)

type switchState uint8

const (
	unknown switchState = iota
	on
	off
)

type request struct {
	toggles   map[string]bool
	remaining *int
}

// Keep accepting the original flat {"exp_50%":true} parameters.
func parse(raw string) (request, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || fields == nil {
		return request{}, errors.New("加成参数应为 JSON 对象")
	}
	req := request{toggles: make(map[string]bool)}
	for name, value := range fields {
		if name == "remaining" {
			if err := json.Unmarshal(value, &req.remaining); err != nil || req.remaining == nil || *req.remaining < 0 {
				return request{}, errors.New("加成 remaining 应为非负整数")
			}
			continue
		}
		if _, ok := labels[name]; !ok {
			return request{}, fmt.Errorf("未知加成 %q", name)
		}
		var enabled *bool
		if err := json.Unmarshal(value, &enabled); err != nil || enabled == nil {
			return request{}, fmt.Errorf("加成 %q 应为布尔值", name)
		}
		req.toggles[name] = *enabled
	}
	if req.remaining != nil && *req.remaining == 0 {
		maps.DeleteFunc(req.toggles, func(_ string, enabled bool) bool { return enabled })
	}
	return req, nil
}

func (*BonusToggleAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	req, err := parse(arg.CustomActionParam)
	if err != nil {
		log.Warn().Err(err).Msg("加成参数错误")
		return false
	}
	owned := ownershipFor(ctx, arg.CurrentTaskName)
	ok := true
	for _, name := range slices.Sorted(maps.Keys(req.toggles)) {
		enable := req.toggles[name]
		if !enable && !owned.contains(name) {
			continue
		}
		if err := setBonus(ctx, owned, name, enable); err != nil {
			log.Warn().Err(err).Str("bonus", name).Msg("加成状态未确认")
			ok = false
		}
	}
	return ok
}

func setBonus(ctx *maa.Context, owned ownership, name string, enable bool) error {
	img, err := screenshot(ctx)
	if err != nil {
		return err
	}
	state, at := locate(ctx, img, name)
	want := off
	if enable {
		want = on
	}
	if state == want {
		if !enable {
			return owned.set(name, false)
		}
		return nil
	}
	if state == unknown || at == nil {
		return errors.New("未定位到唯一且可用的加成拨杆")
	}
	action, err := ctx.RunAction(clickNode, *at, "")
	if err != nil || action == nil || !action.Success {
		return errors.New("点击加成拨杆失败")
	}
	// Only claim switches read as off before our successful click. If an
	// interrupt arrives during verification, cleanup will re-read the switch.
	if enable {
		if err := owned.set(name, true); err != nil {
			return err
		}
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		img, err = screenshot(ctx)
		if err != nil {
			return err
		}
		after, _ := locate(ctx, img, name)
		if after == want {
			if !enable {
				return owned.set(name, false)
			}
			return nil
		}
		if enable && after == off {
			if detail, err := ctx.RunRecognition(newcomerNode, img); err == nil && detail != nil && detail.Hit {
				return owned.set(name, false)
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return errors.New("拨杆未达到目标状态")
}

func screenshot(ctx *maa.Context) (image.Image, error) {
	tasker := ctx.GetTasker()
	if tasker.Stopping() {
		return nil, errors.New("加成操作已停止")
	}
	img := vision.Screencap(tasker, 0)
	if img == nil {
		return nil, errors.New("无法获取加成面板截图")
	}
	return img, nil
}

func locate(ctx *maa.Context, img image.Image, name string) (switchState, *maa.Rect) {
	if img == nil {
		return unknown, nil
	}
	expected := regexp.MustCompile(labels[name])
	for _, row := range vision.OCRItems(ctx, img, maa.Rect{414, 109, 357, 390}) {
		if !expected.MatchString(strings.Join(strings.Fields(row.Text), "")) {
			continue
		}
		// The color match locates the left of the timer bar, not the knob.
		roi := maa.Rect{row.Box.X() + row.Box.Width() + 10, row.Box.Y() - 10, 80, row.Box.Height() + 20}
		active := recognizeBox(ctx, activeNode, roi, img)
		inactive := recognizeBox(ctx, inactiveNode, roi, img)
		if (active == nil) == (inactive == nil) {
			continue
		}
		if active != nil {
			return on, knob(*active, img.Bounds())
		}
		return off, knob(*inactive, img.Bounds())
	}
	return unknown, nil
}

func recognizeBox(ctx *maa.Context, node string, roi maa.Rect, img image.Image) *maa.Rect {
	detail, err := ctx.RunRecognition(node, img, map[string]any{node: map[string]any{"roi": roi}})
	if err != nil || detail == nil || !detail.Hit {
		return nil
	}
	return &detail.Box
}

func knob(bar maa.Rect, bounds image.Rectangle) *maa.Rect {
	x, y := bar.X()+90, bar.Y()+bar.Height()/2
	if !image.Pt(x, y).In(bounds) {
		return nil
	}
	return &maa.Rect{x, y, 1, 1}
}
