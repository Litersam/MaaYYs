package battle_team

import (
	"encoding/json"
	"fmt"

	"github.com/MaaXYZ/maa-framework-go/v4"

	"maa-yys-agent/actions/switch_soul"
)

const (
	defaultPosition = "御魂"
	presetTaskName  = "出战队伍预设"
)

type BattleTeam struct{}

type BattleTeamParams struct {
	Position      string `json:"位置"`
	GroupName     string `json:"分组名称"`
	TeamName      string `json:"队伍名称"`
	PositionAlias string `json:"position"`
	GroupAlias    string `json:"group_name"`
	TeamAlias     string `json:"team_name"`
}

var (
	defaultBattleTeamBox = []int{400, 150, 560, 400}

	positionBoxes = map[string][]int{
		"御魂": {400, 150, 560, 400},
		"契灵": {590, 140, 560, 400},
		"麒麟": {400, 150, 560, 400},
	}

	presetBoxFields = map[string][]string{
		"出战队伍预设-面板已打开":       {"roi"},
		"出战队伍预设-向上滑动":        {"begin", "end"},
		"出战队伍预设-向上滑动队伍":      {"begin", "end"},
		"出战队伍预设-滑动分组到最上方":    {"begin", "end"},
		"出战队伍预设-滑动队伍到最上方":    {"begin", "end"},
		"出战队伍预设-点击分组":        {"roi"},
		"出战队伍预设-识别出战队伍":      {"roi"},
		"出战队伍预设-识别出战队伍-点击出战": {"roi"},
		"出战队伍预设-识别出战队伍-确保没有出战": {
			"roi",
		},
	}
)

func (p *BattleTeamParams) normalize() {
	if p.Position == "" {
		p.Position = p.PositionAlias
	}
	if p.Position == "" {
		p.Position = defaultPosition
	}
	if p.GroupName == "" {
		p.GroupName = p.GroupAlias
	}
	if p.TeamName == "" {
		p.TeamName = p.TeamAlias
	}
}

// Run selects the lineup from a supported preparation screen; callers opt in separately from SwitchSoul.
func (a *BattleTeam) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	params := BattleTeamParams{}
	if arg != nil && arg.CustomActionParam != "" {
		if err := json.Unmarshal([]byte(arg.CustomActionParam), &params); err != nil {
			return switch_soul.Abort(ctx, arg, fmt.Errorf("出战队伍参数解析失败: %w", err))
		}
	}

	params.normalize()
	if params.GroupName == "" || params.TeamName == "" {
		return switch_soul.Abort(ctx, arg, fmt.Errorf("出战队伍参数错误：分组名称和队伍名称不能为空"))
	}

	box := resolvePositionBox(params.Position)
	fmt.Printf("出战队伍 - 位置：%s，分组：%s，队伍：%s，box：%v\n",
		params.Position, params.GroupName, params.TeamName, box)

	result, err := ctx.RunTask(presetTaskName, buildPresetOverride(box, params.GroupName, params.TeamName))
	switch {
	case err != nil:
		return switch_soul.Abort(ctx, arg, fmt.Errorf("执行任务 %s 失败: %w", presetTaskName, err))
	case result == nil:
		return switch_soul.Abort(ctx, arg, fmt.Errorf("执行任务 %s 未返回结果", presetTaskName))
	case !result.Status.Success():
		return switch_soul.Abort(ctx, arg, fmt.Errorf("执行任务 %s 失败: %s", presetTaskName, result.Status))
	}

	fmt.Println("出战队伍完成")
	return true
}

func resolvePositionBox(position string) []int {
	box, ok := positionBoxes[position]
	if !ok {
		fmt.Printf("未配置位置 %s 的box，使用默认位置box\n", position)
		box = defaultBattleTeamBox
	}
	return cloneBox(box)
}

func buildPresetOverride(box []int, groupName string, teamName string) map[string]any {
	override := map[string]any{}

	for nodeName, fields := range presetBoxFields {
		nodeOverride := ensureNodeOverride(override, nodeName)
		for _, field := range fields {
			nodeOverride[field] = cloneBox(box)
		}
	}

	ensureNodeOverride(override, "出战队伍预设-点击分组")["expected"] = groupName
	ensureNodeOverride(override, "出战队伍预设-识别出战队伍")["expected"] = teamName

	return override
}

func ensureNodeOverride(override map[string]any, nodeName string) map[string]any {
	if nodeOverride, ok := override[nodeName].(map[string]any); ok {
		return nodeOverride
	}

	nodeOverride := map[string]any{}
	override[nodeName] = nodeOverride
	return nodeOverride
}

func cloneBox(box []int) []int {
	cloned := make([]int, len(box))
	copy(cloned, box)
	return cloned
}
