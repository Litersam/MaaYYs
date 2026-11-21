package battle_flow

import (
	"encoding/json"
	"fmt"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type Outcome struct{}

func (*Outcome) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	var p struct {
		Status runstate.Status `json:"status"`
		Reason string          `json:"reason"`
	}
	if json.Unmarshal([]byte(arg.CustomActionParam), &p) != nil || p.Reason == "" {
		return report(ctx, runstate.Failed, "invalid_battle_outcome")
	}
	switch p.Status {
	case runstate.Failed, runstate.Skipped, runstate.Deferred:
		return report(ctx, p.Status, p.Reason)
	default:
		return report(ctx, runstate.Failed, "invalid_battle_outcome")
	}
}

type SoulCountGate struct{}

func (*SoulCountGate) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	return gateSoulCount(arg.CustomActionParam, func() bool {
		return ctx.OverrideNext(arg.CurrentTaskName, []maa.NextItem{}) == nil
	}, func(status runstate.Status, reason string) bool {
		return report(ctx, status, reason)
	})
}

func gateSoulCount(raw string, clearNext func() bool, finish func(runstate.Status, string) bool) bool {
	var p struct {
		Count *int `json:"count"`
	}
	if json.Unmarshal([]byte(raw), &p) != nil || p.Count == nil || *p.Count < 0 {
		return finish(runstate.Failed, "invalid_soul_count")
	}
	if *p.Count > 0 {
		return true
	}
	if !clearNext() {
		return finish(runstate.Failed, "soul_skip_dispatch_failed")
	}
	return finish(runstate.Skipped, "soul_count_zero")
}

func report(ctx *maa.Context, status runstate.Status, reason string) bool {
	fmt.Printf("战斗任务: %s (%s)\n", status, reason)
	runstate.Report(ctx, status)
	runstate.SetMetric(ctx, "battle_reason", reason)
	return status != runstate.Failed
}
