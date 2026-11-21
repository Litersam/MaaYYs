package breakthrough

import (
	"maa-yys-agent/internal/runstate"

	"github.com/MaaXYZ/maa-framework-go/v4"
)

const (
	realmPlan    = "结界突破-策略"
	realmAttack  = "突破_点击进攻"
	realmRetreat = "突破_最后一个进攻"
	retreatCount = "突破-退场结算"
	realmLoss    = "突破-战败收尾"
	guildPlan    = "寮突-策略"
	guildAttack  = "寮突-点击进攻"
	guildSwipe   = "寮突16_copy1"
)

var targetNodes = [realmSlots]string{
	"挑战第一个结界", "挑战第二个结界", "挑战第三个结界",
	"挑战第四个结界", "挑战第五个结界", "挑战第六个结界",
	"挑战第七个结界", "挑战第八个结界", "挑战第九个结界",
}

type PolicyAction struct{}

var _ maa.CustomActionRunner = (*PolicyAction)(nil)

func (*PolicyAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	if arg == nil || ctx == nil {
		return false
	}
	if arg.CurrentTaskName == guildSwipe {
		return turnGuildPage(ctx)
	}
	if arg.CurrentTaskName == realmLoss {
		if attempts, err := ctx.GetHitCount(realmAttack); err == nil {
			runstate.SetMetric(ctx, "challenge_count", attempts)
		}
		runstate.Report(ctx, runstate.Deferred)
		return true
	}
	node := arg.CurrentTaskName
	p, err := parsePolicy(arg.CustomActionParam)
	if err != nil || (node != realmPlan && node != guildPlan) || (node == realmPlan) != (p.mode == realm) {
		return false
	}
	capacity, attackNode := 30, realmAttack
	if p.mode == guild {
		capacity, attackNode = 6, guildAttack
	}
	left, ok := recognizedQuota(arg.RecognitionDetail, capacity)
	if !ok {
		return false
	}
	attempts, err := ctx.GetHitCount(attackNode)
	if err != nil {
		return false
	}
	runstate.SetMetric(ctx, "challenge_count", attempts)
	if p.mode == realm {
		runstate.SetMetric(ctx, "tickets_remaining", left)
	} else {
		runstate.SetMetric(ctx, "guild_chances_remaining", left)
	}
	if budget := p.budget(attempts, left); budget != attack {
		runstate.Report(ctx, p.stopStatus(budget, attempts))
		return next(ctx, node, "导航-庭院")
	}
	if p.mode == guild {
		return next(ctx, node, "寮突18_从上往下打")
	}

	controller := ctx.GetTasker().GetController()
	if controller == nil {
		return false
	}
	img, err := controller.CacheImage()
	if err != nil || img == nil {
		return false
	}
	var won [realmSlots]bool
	wins := 0
	for i, target := range targetNodes {
		// Reuse the target's ROI and stamp template, without the click node's inverse.
		d, err := ctx.RunRecognition(target, img, map[string]any{target: map[string]bool{"inverse": false}})
		if err != nil || d == nil {
			return false
		}
		won[i] = d.Hit
		if d.Hit {
			wins++
		}
	}
	retreats, err := ctx.GetHitCount(retreatCount)
	if err != nil {
		return false
	}
	if wins != realmSlots-1 && retreats != 0 {
		if err := ctx.ClearHitCount(retreatCount); err != nil {
			return false
		}
		retreats = 0
	}
	runstate.SetMetric(ctx, "realm_board_wins", wins)
	move := p.realmMove(attempts, left, won, retreats)
	switch move.kind {
	case refresh:
		return next(ctx, node, "突破-先回突破界面")
	case attack, retreat:
		enter := realmAttack
		if move.kind == retreat {
			enter = realmRetreat
		}
		target := targetNodes[move.slot]
		return next(ctx, target, enter) && next(ctx, node, target)
	default:
		return false
	}
}

func (p policy) stopStatus(reason moveKind, attempts uint64) runstate.Status {
	if attempts > 0 {
		return runstate.Completed
	}
	if p.mode == guild && reason == stopQuota {
		return runstate.Deferred
	}
	return runstate.Skipped
}

func recognizedQuota(d *maa.RecognitionDetail, capacity int) (int, bool) {
	if d == nil || !d.Hit || d.Results == nil || len(d.Results.Filtered) != 1 || d.Results.Best == nil {
		return 0, false
	}
	ocr, ok := d.Results.Best.AsOCR()
	if !ok {
		return 0, false
	}
	return readQuota(ocr.Text, capacity)
}

func next(ctx *maa.Context, node, target string) bool {
	return ctx.OverrideNext(node, []maa.NextItem{{Name: target}}) == nil
}
