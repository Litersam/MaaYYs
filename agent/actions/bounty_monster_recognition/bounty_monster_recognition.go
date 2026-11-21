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
	if ctx == nil || arg == nil || arg.CurrentTaskName != selectRow {
		return false
	}
	controller := ctx.GetTasker().GetController()
	if controller == nil {
		return false
	}
	img, err := controller.CacheImage()
	if err != nil || img == nil {
		return false
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
		runstate.Report(ctx, runstate.Skipped)
	}
	return ctx.OverrideNext(selectRow, []maa.NextItem{{Name: "导航-庭院"}}) == nil
}
