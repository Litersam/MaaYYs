// Package bondling reconciles the task-wide summon budget and verified battle
// results. Screen recognition, clicks and navigation live in the pipeline.
package bondling

import (
	"encoding/json"
	"log"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const (
	prefix        = "契灵结契-"
	stonesMetric  = "bondling_stones_spent"
	bondsMetric   = "bondling_verified_bonds"
	memoryLimit   = 2000
	ready         = "在挑战"
	mapPage       = "在地图"
	quantityModal = "在召唤数量窗"
)

type Params struct {
	MaxSummonStones int `json:"max_summon_stones"`
}

type Action struct{}

func (*Action) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	result := outcome{status: runstate.Failed, reason: "bondling_invalid_params"}
	var p Params
	if (arg.CustomActionParam == "" || json.Unmarshal([]byte(arg.CustomActionParam), &p) == nil) && p.MaxSummonStones >= 0 {
		if count, ok := challengeCount(ctx); ok {
			f := flow{ui: maaUI{ctx}, max: p.MaxSummonStones, attempts: count}
			result = f.run()
		}
	}
	runstate.SetMetric(ctx, stonesMetric, result.stones)
	runstate.SetMetric(ctx, bondsMetric, result.bonds)
	runstate.SetMetric(ctx, "task_reason", result.reason)
	runstate.Report(ctx, result.status)
	if result.status == runstate.Failed {
		log.Printf("契灵结契: %s (已核销鸣契石 %d, 已验证结契 %d)", result.reason, result.stones, result.bonds)
	}
	return result.status != runstate.Failed
}

// Keep the v4.1.3 option override at its existing node. Read it as configuration
// rather than executing its increment-on-recognition counter.
func challengeCount(ctx *maa.Context) (int, bool) {
	node, err := ctx.GetNode(prefix + "计数")
	if err != nil {
		return 0, false
	}
	var p struct {
		Target int `json:"target_count"`
	}
	param, ok := node.Recognition.Param.(*maa.CustomRecognitionParam)
	if !ok {
		return 0, false
	}
	raw, err := json.Marshal(param.CustomRecognitionParam)
	if err != nil || json.Unmarshal(raw, &p) != nil || p.Target < 1 {
		return 0, false
	}
	return p.Target, true
}

type screen interface {
	step(string) stepResult
	counters(page string, counters ...counter) ([]int, bool)
	stock(before int) (int, bool)
}

type stepResult struct {
	last            string
	bond, abandoned bool
}

type outcome struct {
	status        runstate.Status
	reason        string
	stones, bonds int
}

type flow struct {
	ui            screen
	max, attempts int
	stones, bonds int
	lastStock     int
	battled       bool
}

func (f *flow) run() outcome {
	if f.ui.step("前置导航").last != mapPage {
		return f.fail("entry_unconfirmed")
	}
	if reason, failed := f.inventory(); reason != "" {
		if failed {
			return f.fail(reason)
		}
		return f.returnHome(reason)
	}

	for remaining := f.attempts; remaining > 0; remaining-- {
		switch f.ui.step("寻找可挑战").last {
		case ready:
			usable, reason := f.canChallenge()
			if reason == "no_discs" {
				return f.returnHome(reason)
			}
			if reason != "" {
				return f.fail(reason)
			}
			if usable {
				break
			}
			if f.ui.step("返回地图").last != mapPage {
				return f.fail("board_exit_unconfirmed")
			}
			fallthrough
		case mapPage:
			if reason, failed := f.selectBoard(); reason != "" {
				if failed {
					return f.fail(reason)
				}
				return f.returnHome(reason)
			}
		default:
			return f.fail("board_unconfirmed")
		}
		battle := f.ui.step("试一场")
		f.battled = true
		if battle.bond {
			f.bonds++
		}
		if battle.last == "低式盘提示" {
			return f.fail("selected_discs_insufficient")
		}
		if battle.last != "结算已返回" || (!battle.bond && !battle.abandoned) {
			return f.fail("battle_unconfirmed")
		}
	}
	if f.bonds == 0 {
		return f.fail("no_verified_bonds")
	}
	return f.result(runstate.Completed, "bonds_verified")
}

func (f *flow) inventory() (reason string, failed bool) {
	if f.ui.step("查看契忆").last != "在契忆商店" {
		return "memory_shop_unconfirmed", true
	}
	stock, ok := f.ui.counters("在契忆商店", counter{"契忆库存", 4000}, counter{"商店式盘一库存", 200}, counter{"商店式盘二库存", 200}, counter{"商店式盘三库存", 200})
	if !ok {
		return "inventory_unconfirmed", true
	}
	page := f.ui.step("退出商店").last
	if page == "" {
		return "shop_exit_unconfirmed", true
	}
	if stock[0] >= memoryLimit {
		return "memory_limit", false
	}
	if stock[1]+stock[2]+stock[3] == 0 {
		return "no_discs", false
	}
	if page != mapPage && f.ui.step("前置导航").last != mapPage {
		return "entry_unconfirmed", true
	}
	return "", false
}

func (f *flow) canChallenge() (bool, string) {
	count, ok := f.ui.counters(ready, counter{"盘上剩余", 99}, counter{"式盘一库存", 200}, counter{"式盘二库存", 200}, counter{"式盘三库存", 200})
	if !ok {
		return false, "board_stock_unconfirmed"
	}
	if count[1]+count[2]+count[3] == 0 {
		return false, "no_discs"
	}
	return count[0] > 0, ""
}

func (f *flow) selectBoard() (reason string, failed bool) {
	var vacant string
	for _, slot := range []string{"盘位一", "盘位二", "盘位三", "盘位四"} {
		switch f.ui.step(slot).last {
		case ready:
			usable, reason := f.canChallenge()
			if usable || reason != "" {
				return reason, reason != "" && reason != "no_discs"
			}
		case quantityModal:
			if vacant == "" {
				vacant = slot
			}
		default:
			return "board_unconfirmed", true
		}
		if f.ui.step("返回地图").last != mapPage {
			return "board_exit_unconfirmed", true
		}
	}
	if vacant == "" || f.stones >= f.max {
		return "no_eligible_board", false
	}
	// Disc stock can change in battle; don't buy a target we can't use.
	if reason, failed := f.inventory(); reason != "" {
		return reason, failed
	}
	if f.ui.step(vacant).last != quantityModal {
		return "summon_dialog_unconfirmed", true
	}
	started, reason := f.summon()
	if !started {
		if reason == "" {
			return "no_eligible_board", false
		}
		return reason, true
	}
	switch f.ui.step("寻找可挑战").last {
	case ready:
	case mapPage:
		if f.ui.step(vacant).last != ready {
			return "summoned_board_unconfirmed", true
		}
	default:
		return "summoned_board_unconfirmed", true
	}
	if ok, reason := f.canChallenge(); !ok {
		if reason == "" {
			reason = "summoned_board_unconfirmed"
		}
		return reason, true
	}
	return "", false
}

func (f *flow) returnHome(why string) outcome {
	if f.ui.step("回庭院").last != "在庭院" {
		return f.fail("courtyard_unconfirmed")
	}
	if f.bonds > 0 {
		return f.result(runstate.Completed, "bonds_verified")
	}
	if f.battled || f.stones > 0 {
		return f.fail("no_verified_bonds")
	}
	return f.result(runstate.Skipped, why)
}

func (f *flow) fail(reason string) outcome { return f.result(runstate.Failed, reason) }

func (f *flow) result(status runstate.Status, reason string) outcome {
	return outcome{status, "bondling_" + reason, f.stones, f.bonds}
}
