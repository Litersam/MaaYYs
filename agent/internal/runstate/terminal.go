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
	return Publish(ctx, p.Status, p.Reason)
}

// Publish is also used by tasks whose terminal depends on task-local evidence.
func Publish(ctx *maa.Context, status Status, reason string) bool {
	Report(ctx, status)
	if reason != "" {
		SetMetric(ctx, "task_reason", reason)
	}
	return status != Failed
}
