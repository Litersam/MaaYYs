package demon_encounter

import (
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type Skipped struct{}

func (*Skipped) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	if ctx == nil || arg == nil {
		return false
	}
	switch arg.CurrentTaskName {
	case "逢魔-今日挑战已用完":
		runstate.SetMetric(ctx, "demon_encounter_remaining", 0)
		runstate.Report(ctx, runstate.Skipped)
		return true
	case "开始逢魔-检查时间":
		open, err := encounterOpen(arg.CustomActionParam, time.Now())
		if err != nil {
			runstate.Report(ctx, runstate.Failed)
			return false
		}
		entry := "逢魔-时段未开放"
		if open {
			entry = "逢魔-时段已开放"
		}
		if ctx.OverrideNext(arg.CurrentTaskName, []maa.NextItem{{Name: entry}}) != nil {
			runstate.Report(ctx, runstate.Failed)
			return false
		}
		return true
	case "逢魔-时段未开放":
		runstate.Report(ctx, runstate.Deferred)
		return true
	default:
		return false
	}
}

var clock = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)

func encounterOpen(raw string, now time.Time) (bool, error) {
	var p struct {
		Start string `json:"start"`
		End   string `json:"end"`
		Mode  string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return false, err
	}
	if p.Mode != "" && p.Mode != "in" || !clock.MatchString(p.Start) || !clock.MatchString(p.End) || p.Start == p.End {
		return false, errors.New("invalid Demon Encounter schedule")
	}
	local := now.Format("15:04")
	if p.Start < p.End {
		return local >= p.Start && local < p.End, nil
	}
	return local >= p.Start || local < p.End, nil
}
