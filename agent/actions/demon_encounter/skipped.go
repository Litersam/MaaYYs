package demon_encounter

import (
	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type Skipped struct{}

func (*Skipped) Run(ctx *maa.Context, _ *maa.CustomActionArg) bool {
	runstate.SetMetric(ctx, "demon_encounter_remaining", 0)
	runstate.Report(ctx, runstate.Skipped)
	return true
}
