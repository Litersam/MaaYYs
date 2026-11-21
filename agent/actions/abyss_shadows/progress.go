package abyss_shadows

import (
	"encoding/json"
	"strings"
	"time"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type Window struct{}
type Progress struct{}

var shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

type stateParam struct {
	State string `json:"state"`
}

type tier string

const (
	boss    tier = "boss"
	general tier = "general"
	elite   tier = "elite"
)

var tiers = [...]tier{boss, general, elite}

var budgets = map[tier]uint64{boss: 2, general: 4, elite: 6}

var counterNodes = map[tier]string{
	boss:    "狭间暗域-首领战已返回",
	general: "狭间暗域-副将战已返回",
	elite:   "狭间暗域-精英战已返回",
}

var unresolvedNodes = map[tier]string{
	boss:    "狭间暗域-首领未核实",
	general: "狭间暗域-副将未核实",
	elite:   "狭间暗域-精英未核实",
}

var authoritativeEndNodes = [...]string{
	"狭间暗域-活动已结束弹窗",
	"狭间暗域-确认领域已结束",
	"狭间暗域-确认战报全歼",
	"狭间暗域-奖励已达上限",
}

func windowState(now time.Time) string {
	now = now.In(shanghai)
	if now.Weekday() != time.Friday && now.Weekday() != time.Saturday && now.Weekday() != time.Sunday {
		return "closed"
	}
	y, m, d := now.Date()
	switch {
	case now.Before(time.Date(y, m, d, 18, 55, 0, 0, shanghai)):
		return "before"
	case now.After(time.Date(y, m, d, 21, 15, 0, 0, shanghai)):
		return "closed"
	default:
		return "open"
	}
}

func (*Window) Run(_ *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	var p stateParam
	if arg == nil || json.Unmarshal([]byte(arg.CustomRecognitionParam), &p) != nil || p.State != windowState(time.Now()) {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: arg.Roi}, true
}

func (*Progress) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	var p stateParam
	if arg == nil || json.Unmarshal([]byte(arg.CustomRecognitionParam), &p) != nil {
		return nil, false
	}
	if (p.State == "worked" || p.State == "empty") && !scanResolved(ctx.GetHitCount) {
		return nil, false
	}
	if !matchesProgress(p.State, func(t tier) (uint64, error) { return ctx.GetHitCount(counterNodes[t]) }) {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: arg.Roi}, true
}

func scanResolved(count func(string) (uint64, error)) bool {
	// An authoritative in-game ending takes precedence over a prior unknown slot.
	for _, node := range authoritativeEndNodes {
		if n, err := count(node); err == nil && n > 0 {
			return true
		}
	}
	for _, t := range tiers {
		unknown, err := count(unresolvedNodes[t])
		if err != nil {
			return false
		}
		if unknown != 0 {
			if n, err := count(counterNodes[t]); err != nil || n < budgets[t] {
				return false
			}
		}
	}
	return true
}

func matchesProgress(state string, count func(tier) (uint64, error)) bool {
	if state == "worked" || state == "empty" {
		var total uint64
		for _, t := range tiers {
			n, err := count(t)
			if err != nil {
				return false
			}
			total += n
		}
		return (total > 0) == (state == "worked")
	}
	name, full := strings.CutSuffix(state, "_full")
	t := tier(name)
	if !full || budgets[t] == 0 {
		return false
	}
	n, err := count(t)
	return err == nil && n >= budgets[t]
}
