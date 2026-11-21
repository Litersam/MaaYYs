package bounty_monster_recognition

import (
	"maa-yys-agent/internal/runstate"
	"strings"

	"github.com/MaaXYZ/maa-framework-go/v4"
)

const (
	selectRow = "悬赏-筛选未处理妖怪"
	tryRow    = "悬赏-执行待选行"
	skipRow   = "悬赏-跳过当前行"
	unknown   = "悬赏-侧栏未知线索"
)

type bountyRow struct {
	open    string
	skipped string
	roi     maa.Rect
}

// The sidebar is scrolled to the bottom by the caller; row identities here
// are coordinates on that stable viewport, not global after scrolling.
var rows = []bountyRow{
	{"悬赏封印-识别最下方第一个是不是妖怪", "悬赏-第一行已跳过", maa.Rect{0, 386, 127, 125}},
	{"悬赏封印-识别最下方第二个是不是妖怪", "悬赏-第二行已跳过", maa.Rect{0, 248, 121, 138}},
	{"悬赏封印-识别最下方第三个是不是妖怪", "悬赏-第三行已跳过", maa.Rect{0, 121, 137, 127}},
}

func isUnknownClue(text string) bool {
	text = strings.TrimSpace(text)
	return text != "" && strings.Trim(text, "?？") == ""
}

func eligibleRow(visible, skipped, unidentified bool) bool {
	return visible && !skipped && !unidentified
}

type BountyMonsterRecognition struct{}

var _ maa.CustomActionRunner = (*BountyMonsterRecognition)(nil)

func (*BountyMonsterRecognition) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	if ctx == nil || arg == nil {
		return false
	}
	switch arg.CurrentTaskName {
	case "悬赏-返回庭院后报告完成":
		runstate.Report(ctx, runstate.Completed)
		return true
	case "悬赏-返回庭院后报告跳过":
		runstate.Report(ctx, runstate.Skipped)
		return true
	case "悬赏-无法证明终态":
		runstate.Report(ctx, runstate.Failed)
		return false
	case selectRow:
	default:
		return false
	}
	controller := ctx.GetTasker().GetController()
	if controller == nil {
		return false
	}
	// DirectHit does not request a screencap. A retry must see a fresh frame,
	// not the same cached image while a panel was loading.
	if !controller.PostScreencap().Wait().Success() {
		return retryOrVerify(ctx)
	}
	img, err := controller.CacheImage()
	if err != nil || img == nil {
		return retryOrVerify(ctx)
	}
	// Recheck the exploration surface on the same frame used for every row.
	// All misses on a loading/covered screen cannot imply an empty sidebar.
	panel, err := ctx.RunRecognition("悬赏-探索主界面", img)
	if err != nil || panel == nil {
		return false
	}
	if !panel.Hit {
		return retryOrVerify(ctx)
	}

	unresolved := false
	for _, row := range rows {
		skipped, err := ctx.GetHitCount(row.skipped)
		if err != nil {
			return false
		}
		card, err := ctx.RunRecognition(row.open, img)
		if err != nil || card == nil {
			return false
		}
		clue, err := ctx.RunRecognition(unknown, img, map[string]any{unknown: map[string]any{"roi": row.roi}})
		if err != nil || clue == nil {
			return false
		}
		unidentified := false
		if clue.Hit && clue.Results != nil && clue.Results.Best != nil {
			if result, ok := clue.Results.Best.AsOCR(); ok {
				unidentified = isUnknownClue(result.Text)
			}
		}
		unresolved = unresolved || skipped > 0 || unidentified
		if eligibleRow(card.Hit, skipped > 0, unidentified) {
			hits, err := ctx.GetHitCount(row.open)
			if err != nil {
				return false
			}
			if hits >= 10 {
				unresolved = true
				continue
			}
			return ctx.ClearHitCount(tryRow) == nil &&
				ctx.OverrideNext(skipRow, []maa.NextItem{{Name: row.skipped}}) == nil &&
				ctx.OverrideNext(tryRow, []maa.NextItem{{Name: row.open}}) == nil &&
				ctx.OverrideNext(selectRow, []maa.NextItem{
					{Name: tryRow, JumpBack: true},
					{Name: "悬赏-继续筛选"},
				}) == nil
		}
	}
	if unresolved {
		return setNext(ctx, "悬赏-带未处理目标返回庭院")
	}
	return retryOrVerify(ctx)
}

func retryOrVerify(ctx *maa.Context) bool {
	return setNext(ctx, "悬赏-重试侧栏", "悬赏-复核完成状态")
}

func setNext(ctx *maa.Context, names ...string) bool {
	next := make([]maa.NextItem, 0, len(names))
	for _, name := range names {
		next = append(next, maa.NextItem{Name: name})
	}
	return ctx.OverrideNext(selectRow, next) == nil
}
