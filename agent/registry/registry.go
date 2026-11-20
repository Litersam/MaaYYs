package registry

import (
	"fmt"

	"maa-yys-agent/actions/auto_battle"
	"maa-yys-agent/actions/auto_foster"
	"maa-yys-agent/actions/battle_team"
	"maa-yys-agent/actions/bonus_toggle"
	"maa-yys-agent/actions/bounty_monster_recognition"
	"maa-yys-agent/actions/challenge_dungeon_boss"
	"maa-yys-agent/actions/clear_hit_count"
	"maa-yys-agent/actions/count_action"
	"maa-yys-agent/actions/guess"
	"maa-yys-agent/actions/human_touch"
	"maa-yys-agent/actions/kun28"
	"maa-yys-agent/actions/loop_action"
	"maa-yys-agent/actions/question_matcher"
	"maa-yys-agent/actions/random_swipe"
	"maa-yys-agent/actions/random_task"
	"maa-yys-agent/actions/random_touch"
	"maa-yys-agent/actions/random_wait"
	"maa-yys-agent/actions/repeat_challenge_n_times"
	"maa-yys-agent/actions/restart"
	"maa-yys-agent/actions/restartgame"
	"maa-yys-agent/actions/switch_account"
	"maa-yys-agent/actions/switch_soul"
	"maa-yys-agent/actions/switch_soul_by_week"
	"maa-yys-agent/actions/task_counter"
	"maa-yys-agent/actions/task_list"
	"maa-yys-agent/actions/team_builder"
	"maa-yys-agent/actions/time_check"
	"maa-yys-agent/recognition/co_battle_target_recognition"
	"maa-yys-agent/recognition/config_value_recognition"
	"maa-yys-agent/recognition/my_recognizer"
	"maa-yys-agent/recognition/ocr_result_counter_recognition"
	"maa-yys-agent/recognition/task_counter_recognition"
	"maa-yys-agent/recognition/time_check_recognition"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type ActionRegistration struct {
	Name   string
	Runner maa.CustomActionRunner
}

type RecognitionRegistration struct {
	Name   string
	Runner maa.CustomRecognitionRunner
}

// SwitchAccountRunner is shared so in-process headless CLI runs can attach RuntimeHook safely.
var SwitchAccountRunner = &switch_account.SwitchAccount{}

var CustomActions = []ActionRegistration{
	{"RandomTouch", &random_touch.RandomTouch{}},
	{"RandomSwipe", &random_swipe.RandomSwipe{}},
	{"RandomTask", &random_task.RandomTask{}},
	{"RandomWait", &random_wait.RandomWait{}},
	{"HumanTouch", &human_touch.HumanTouch{}},
	{"ClearHitCount", &clear_hit_count.ClearHitCount{}},
	{"CountAction", &count_action.CountAction{}},
	{"LoopAction", &loop_action.LoopAction{}},
	{"TaskCounter", &task_counter.TaskCounter{}},
	{"TaskList", &task_list.TaskList{}},
	{"ReStart", &restart.ReStart{}},
	{"ReStartGame", &restartgame.ReStartGame{}},
	{"AutoBattle", &auto_battle.AutoBattle{}},
	{"AutoFoster", &auto_foster.AutoFoster{}},
	{"BattleTeam", &battle_team.BattleTeam{}},
	{"SwitchSoul", &switch_soul.SwitchSoul{}},
	{"SwitchSoulByWeek", &switch_soul_by_week.SwitchSoulByWeek{}},
	{"SwitchAccount", SwitchAccountRunner},
	{"TeamBuilder", &team_builder.TeamBuilder{}},
	{"BountyMonsterRecognition", &bounty_monster_recognition.BountyMonsterRecognition{}},
	{"ChallengeDungeonBoss", &challenge_dungeon_boss.ChallengeDungeonBoss{}},
	{"RepeatChallengeNTimes", &repeat_challenge_n_times.RepeatChallengeNTimes{}},
	{"Kun28", &kun28.Kun28{}},
	{"BonusToggleAction", &bonus_toggle.BonusToggleAction{}},
	{"QuestionMatcher", &question_matcher.QuestionMatcher{}},
	{"Guess", &guess.Guess{}},
	{"TimeCheck", &time_check.TimeCheck{}},
}

var CustomRecognitions = []RecognitionRegistration{
	{"ConfigValueWriteRecognition", &config_value_recognition.ConfigValueWriteRecognition{}},
	{"ConfigValueCheckRecognition", &config_value_recognition.ConfigValueCheckRecognition{}},
	{"WeekStatusWriteRecognition", &config_value_recognition.WeekStatusWriteRecognition{}},
	{"WeekStatusCheckRecognition", &config_value_recognition.WeekStatusCheckRecognition{}},
	{"CoBattleTargetRecognition", &co_battle_target_recognition.CoBattleTargetRecognition{}},
	{"MyRecognizer", &my_recognizer.MyRecognizer{}},
	{"OCRResultCounterRecognition", &ocr_result_counter_recognition.OCRResultCounterRecognition{}},
	{"TaskCounterRecognition", &task_counter_recognition.TaskCounterRecognition{}},
	{"TimeCheckRecognition", &time_check_recognition.TimeCheckRecognition{}},
}

// RegisterAgentServer registers all custom actions and recognitions with MaaAgentServer.
func RegisterAgentServer() error {
	for _, action := range CustomActions {
		if err := maa.AgentServerRegisterCustomAction(action.Name, action.Runner); err != nil {
			return fmt.Errorf("register custom action %q: %w", action.Name, err)
		}
	}
	for _, recognition := range CustomRecognitions {
		if err := maa.AgentServerRegisterCustomRecognition(recognition.Name, recognition.Runner); err != nil {
			return fmt.Errorf("register custom recognition %q: %w", recognition.Name, err)
		}
	}
	return nil
}

// RegisterResource registers all custom actions and recognitions directly on an in-process maa.Resource.
func RegisterResource(res *maa.Resource) error {
	for _, action := range CustomActions {
		if err := res.RegisterCustomAction(action.Name, action.Runner); err != nil {
			return fmt.Errorf("register resource action %q: %w", action.Name, err)
		}
	}
	for _, recognition := range CustomRecognitions {
		if err := res.RegisterCustomRecognition(recognition.Name, recognition.Runner); err != nil {
			return fmt.Errorf("register resource recognition %q: %w", recognition.Name, err)
		}
	}
	return nil
}
