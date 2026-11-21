package challenge_dungeon_boss

import (
	"encoding/json"
	"fmt"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type ChallengeDungeonBoss struct{}

type ChallengeParams struct {
	Count    int  `json:"count"`
	IsReward bool `json:"isReward"` // 是否挑战悬赏
}

func (a *ChallengeDungeonBoss) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	params := ChallengeParams{Count: 1}
	if (arg.CustomActionParam != "" && json.Unmarshal([]byte(arg.CustomActionParam), &params) != nil) ||
		params.Count < 1 || params.Count > 3 {
		runstate.Report(ctx, runstate.Failed)
		return false
	}
	runstate.SetMetric(ctx, "area_boss_requested", params.Count)
	fought := fightRequested(params, func(reward bool, index int) (string, bool) {
		if reward {
			return selectBoss(ctx, "地鬼-选择悬赏", nil)
		}
		return selectBoss(ctx, "地鬼-选择热门", map[string]any{
			"地鬼-识别挑战位置": map[string]any{
				"custom_recognition_param": buttonParams{Node: "地鬼-热门挑战按钮模板", Index: index},
			},
		})
	}, func() bool {
		task, err := ctx.RunTask("自动地鬼3")
		return err == nil && confirmedVictory(task)
	}, func() bool {
		task, err := ctx.RunTask("地鬼-退出未挑战详情")
		return err == nil && task != nil && task.Status.Success()
	})
	runstate.SetMetric(ctx, "area_boss_completed", fought)
	if fought != params.Count {
		fmt.Printf("地域鬼王：仅确认完成 %d/%d\n", fought, params.Count)
		runstate.Report(ctx, runstate.Failed)
		return false
	}
	return true
}

func fightRequested(params ChallengeParams, selectBoss func(bool, int) (string, bool), win, closeDetail func() bool) int {
	defeated := make(map[string]bool)
	first := 0
	if params.IsReward {
		first = -1
	}
	for index := first; index < 3 && len(defeated) < params.Count; index++ {
		name, opened := selectBoss(index < 0, index)
		if !opened {
			continue
		}
		if name == "" || defeated[name] {
			if !closeDetail() {
				break
			}
			continue
		}
		if !win() {
			break
		}
		defeated[name] = true
	}
	return len(defeated)
}

func selectBoss(ctx *maa.Context, node string, override map[string]any) (string, bool) {
	task, err := ctx.RunTask(node, override)
	if err != nil || task == nil {
		return "", false
	}
	opened := false
	for _, detail := range task.NodeDetails {
		if detail == nil || !detail.RunCompleted {
			continue
		}
		switch detail.Name {
		case "地鬼-悬赏准备详情", "地鬼-热门准备详情":
			opened = true
		case "地鬼-确认鬼王详情":
			if !task.Status.Success() {
				break
			}
			if name := recognizedBossName(detail.Recognition); name != "" {
				return name, true
			}
		}
	}
	return "", opened
}

func confirmedVictory(task *maa.TaskDetail) bool {
	if task == nil || !task.Status.Success() {
		return false
	}
	started, won, returned := false, false, false
	for _, node := range task.NodeDetails {
		if node == nil || !node.RunCompleted {
			continue
		}
		switch node.Name {
		case "自动地鬼-匹配挑战图片":
			started = true
		case "地鬼-战斗失败":
			return false
		case "自动地鬼6", "自动地鬼16", "自动地鬼17":
			won = started
		case "地鬼-确认胜利并返回":
			returned = won
		}
	}
	return returned
}
