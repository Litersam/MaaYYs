package runstate

import (
	"encoding/json"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

// TerminalAction records the business outcome at a verified pipeline terminal.
type TerminalAction struct{}

func (*TerminalAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	var p struct {
		Status Status `json:"status"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal([]byte(arg.CustomActionParam), &p) != nil || priority(p.Status) == 0 {
		Report(ctx, Failed)
		SetMetric(ctx, "task_reason", "invalid_task_outcome")
		return false
	}
	Report(ctx, p.Status)
	if p.Reason != "" {
		SetMetric(ctx, "task_reason", p.Reason)
	}
	return p.Status != Failed
}
