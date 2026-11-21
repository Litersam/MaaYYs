package abyss_shadows

import (
	"encoding/json"
	"fmt"
	"image"
	"time"

	"maa-yys-agent/internal/runstate"
	"maa-yys-agent/ocrtext"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const (
	sampleInterval = time.Second
	battleTimeout  = 4 * time.Minute
	settlementNode = "狭间暗域-结算标识"
	activeNode     = "狭间暗域-战斗中"
	retreatNode    = "狭间暗域-退战按钮"
)

type Battle struct{}

type battleParams struct {
	Tier         tier  `json:"tier"`
	DamageTarget int64 `json:"damage_target"`
}

type observation struct {
	Tier         tier   `json:"tier"`
	DamageTarget int64  `json:"damage_target"`
	LastDamage   *int64 `json:"last_damage"`
	Samples      int    `json:"samples"`
	Readable     int    `json:"readable_samples"`
	Result       string `json:"result"`
}

type streak struct {
	target int64
	hits   int
}

func (s *streak) sample(value int64, ok bool) bool {
	if !ok || value < s.target {
		s.hits = 0
		return false
	}
	s.hits++
	return s.hits >= 2
}

func (*Battle) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	var p battleParams
	if arg == nil || json.Unmarshal([]byte(arg.CustomActionParam), &p) != nil || budgets[p.Tier] == 0 || p.DamageTarget <= 0 {
		return false
	}
	n, err := ctx.GetHitCount(counterNodes[p.Tier])
	if err != nil || n >= budgets[p.Tier] {
		return false
	}
	obs := observation{Tier: p.Tier, DamageTarget: p.DamageTarget, Result: "unverified"}
	defer func() { runstate.SetMetric(ctx, fmt.Sprintf("abyss_battle_%s_%d", p.Tier, n+1), obs) }()
	tasker := ctx.GetTasker()
	ctrl := tasker.GetController()
	if ctrl == nil {
		return false
	}
	fresh := func() image.Image {
		if tasker.Stopping() || !ctrl.PostScreencap().Wait().Success() {
			return nil
		}
		img, err := ctrl.CacheImage()
		if err != nil {
			return nil
		}
		return img
	}
	match := func(node string, img image.Image) *maa.RecognitionDetail {
		if img != nil {
			if d, err := ctx.RunRecognition(node, img); err == nil && d != nil && d.Hit {
				return d
			}
		}
		return nil
	}
	obs.Result = monitorBattle(&obs, battleTimeout, sampleInterval, tasker.Stopping, fresh, match,
		func(img image.Image) (int64, bool) { return readDamage(ctx, img) },
		func(d *maa.RecognitionDetail) bool {
			act, err := ctx.RunAction(retreatNode, d.Box, d.DetailJson)
			return err == nil && act != nil && act.Success
		})
	return obs.Result == "natural_settlement_seen" || obs.Result == "retreat_requested"
}

func monitorBattle(obs *observation, timeout, interval time.Duration, stopping func() bool,
	fresh func() image.Image, match func(string, image.Image) *maa.RecognitionDetail,
	read func(image.Image) (int64, bool), retreat func(*maa.RecognitionDetail) bool,
) string {
	s := streak{target: obs.DamageTarget}
	deadline := time.Now().Add(timeout)
	for !stopping() && time.Now().Before(deadline) {
		start := time.Now()
		img := fresh()
		if match(settlementNode, img) != nil {
			return "natural_settlement_seen"
		}
		var value int64
		var ok bool
		if match(activeNode, img) != nil {
			value, ok = read(img)
		}
		obs.Samples++
		if ok {
			obs.Readable++
			obs.LastDamage = &value
		}
		if s.sample(value, ok) {
			// OCR may finish after settlement appeared. Never click from the OCR frame.
			img = fresh()
			if match(settlementNode, img) != nil {
				return "natural_settlement_seen"
			}
			// The active matcher returns its first child's box: the verified retreat button.
			if button := match(activeNode, img); button != nil && !stopping() && time.Now().Before(deadline) {
				if retreat(button) {
					return "retreat_requested"
				}
				return "retreat_click_failed"
			}
			s.sample(0, false)
		}
		if !stopping() {
			time.Sleep(min(time.Until(deadline), time.Until(start.Add(interval))))
		}
	}
	return "unverified"
}

func readDamage(ctx *maa.Context, img image.Image) (int64, bool) {
	line := damageImage(img)
	if line == nil {
		return 0, false
	}
	detail, err := ctx.RunRecognitionDirect(maa.RecognitionTypeOCR, &maa.OCRParam{OnlyRec: true}, line)
	if err != nil || detail == nil || !detail.Hit {
		return 0, false
	}
	if texts := ocrtext.Texts(detail); len(texts) == 1 {
		return parseDamage(texts[0])
	}
	return 0, false
}
