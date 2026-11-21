package switch_soul

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/MaaXYZ/maa-framework-go/v4"
)

type SwitchSoul struct{}

type SwitchSoulParams struct {
	GroupName string `json:"group_name"`
	TeamName  string `json:"team_name"`
}

// Run 直接执行装备御魂任务，并通过 pipeline 覆盖传入分组和队伍名称。
func (a *SwitchSoul) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	params := SwitchSoulParams{}
	if arg != nil && arg.CustomActionParam != "" {
		if err := json.Unmarshal([]byte(arg.CustomActionParam), &params); err != nil {
			return Abort(ctx, arg, fmt.Errorf("切换御魂参数解析失败: %w", err))
		}
	}

	return Equip(ctx, arg, params.GroupName, params.TeamName)
}

// Equip runs the shared pipeline and requires a successful subtask result.
func Equip(ctx *maa.Context, arg *maa.CustomActionArg, groupName, teamName string) bool {
	if err := equip(ctx.RunTask, groupName, teamName); err != nil {
		return Abort(ctx, arg, err)
	}
	return true
}

func equip(runTask func(string, ...any) (*maa.TaskDetail, error), groupName, teamName string) error {
	groupName, teamName = strings.TrimSpace(groupName), strings.TrimSpace(teamName)
	if groupName == "" || teamName == "" {
		return fmt.Errorf("切换御魂失败：分组和队伍名称不能为空")
	}

	result, err := runTask("装备御魂", map[string]any{
		"装备御魂-识别目标分组": map[string]any{
			"expected": ExactPresetName(groupName),
		},
		"装备御魂-识别队伍名称": map[string]any{
			"expected": ExactPresetName(teamName),
		},
	})
	switch {
	case err != nil:
		return fmt.Errorf("执行任务 装备御魂 失败: %w", err)
	case result == nil:
		return fmt.Errorf("执行任务 装备御魂 未返回结果")
	case !result.Status.Success():
		return fmt.Errorf("执行任务 装备御魂 失败: %s", result.Status)
	}
	return nil
}

// ExactPresetName is shared by the equipment and battle preparation panels.
func ExactPresetName(name string) string {
	return "^" + regexp.QuoteMeta(name) + "$"
}

// Abort prevents a legacy recovery from turning a failed preset into a successful task.
func Abort(ctx *maa.Context, arg *maa.CustomActionArg, err error) bool {
	fmt.Println(err)
	if arg != nil && arg.CurrentTaskName != "" {
		// Override only this context, so the task fails normally without canceling the queue.
		overrideErr := ctx.OverridePipeline(map[string]any{
			arg.CurrentTaskName: map[string]any{"on_error": []string{}},
		})
		if overrideErr == nil {
			return false
		}
		fmt.Printf("清除预设切换错误回退失败: %v\n", overrideErr)
	}
	ctx.GetTasker().PostStop()
	return false
}
