package co_battle_target_recognition

import (
	"encoding/json"
	"slices"

	"maa-yys-agent/ocrtext"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

// Maa owns the anchor for the lifetime of this task context. It is private data,
// never a pipeline transition; the runner itself holds no account state.
const targetsAnchor = "bounty.private.co-battle-targets"

type targets struct {
	TaskID int64    `json:"task_id"`
	Names  []string `json:"names"`
}

func loadTargets(raw string, taskID int64) targets {
	var t targets
	if json.Unmarshal([]byte(raw), &t) != nil || t.TaskID != taskID || len(t.Names) > 3 {
		return targets{TaskID: taskID}
	}
	return t
}

type CoBattleTargetRecognition struct{}

var _ maa.CustomRecognitionRunner = (*CoBattleTargetRecognition)(nil)

func (*CoBattleTargetRecognition) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	if ctx == nil || arg == nil || arg.Img == nil {
		return nil, false
	}
	var p struct {
		Action string `json:"action"`
	}
	if json.Unmarshal([]byte(arg.CustomRecognitionParam), &p) != nil ||
		(p.Action != "add" && p.Action != "check") {
		return nil, false
	}
	raw, _ := ctx.GetAnchor(targetsAnchor)
	t := loadTargets(raw, arg.TaskID)
	if p.Action == "check" && len(t.Names) == 0 {
		return nil, false
	}
	detail, err := ctx.RunRecognition("悬赏封印-识别协战目标名称", arg.Img)
	if err != nil || detail == nil || !detail.Hit {
		return nil, false
	}
	names := ocrtext.Texts(detail)
	if len(names) != 1 || names[0] == "" {
		return nil, false
	}
	known := slices.Contains(t.Names, names[0])
	if p.Action == "add" && !known {
		if len(t.Names) == 3 {
			return nil, false
		}
		t.Names = append(t.Names, names[0])
		value, err := json.Marshal(t)
		known = err == nil && ctx.SetAnchor(targetsAnchor, string(value)) == nil
	}
	if !known {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: detail.Box}, true
}
