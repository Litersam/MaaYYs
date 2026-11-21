package party

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"maa-yys-agent/actions/bonus_toggle"
	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/rs/zerolog/log"
)

const stateAnchor = "party.private.state"

type Config struct {
	Role          string `json:"role"`
	Flow          string `json:"flow"`
	Dungeon       string `json:"dungeon"`
	TeammateNames string `json:"teammate_names"`
	CaptainNames  string `json:"captain_names"`
	LimitCount    int    `json:"limit_count"`
	AutoAccept    bool   `json:"auto_accept"`
}

type state struct {
	Config
	Names         []string
	Selected      []string
	Fights        int
	End           string
	InRound       bool
	AcceptedPeer  string
	AcceptedUntil time.Time
	MemberJoined  bool
}

func (s state) authorized(now time.Time) bool {
	return s.Role == "member" && s.AcceptedPeer != "" && (s.MemberJoined || now.Before(s.AcceptedUntil))
}

func (s *state) clearInvitation() {
	s.AcceptedPeer = ""
	s.AcceptedUntil = time.Time{}
	s.MemberJoined = false
}

func clearHits(ctx *maa.Context, nodes ...string) bool {
	for _, node := range nodes {
		if err := ctx.ClearHitCount(node); err != nil {
			log.Warn().Err(err).Str("node", node).Msg("无法重置组队节点次数")
			return false
		}
	}
	return true
}

func splitNames(input string) ([]string, error) {
	if strings.TrimSpace(input) == "" {
		return nil, nil
	}
	var names []string
	seen := map[string]bool{}
	for _, segment := range strings.Split(strings.ReplaceAll(input, "，", ","), ",") {
		n := strings.TrimSpace(segment)
		if n == "" || seen[n] || strings.ContainsAny(segment, "\x00\r\n\t") {
			return nil, fmt.Errorf("队员或队长昵称为空、重复或包含控制字符")
		}
		seen[n] = true
		names = append(names, n)
	}
	return names, nil
}

func newState(p Config) (state, error) {
	s := state{Config: p}
	if p.LimitCount < 0 {
		return s, fmt.Errorf("limit_count 必须非负")
	}
	if p.Flow != "party" && p.Flow != "awake" && p.Flow != "bond" {
		return s, fmt.Errorf("未知组队流程")
	}
	var err error
	switch p.Role {
	case "manual":
		if p.Flow != "party" {
			return s, fmt.Errorf("manual 只适用于组队刷本")
		}
	case "solo":
		if p.Flow != "awake" {
			return s, fmt.Errorf("solo 只适用于觉醒")
		}
	case "captain":
		if p.LimitCount > 0 {
			s.Names, err = splitNames(p.TeammateNames)
			if err == nil && (len(s.Names) == 0 || len(s.Names) > 2 || (p.Flow == "bond" || p.Dungeon == "eternity_sea") && len(s.Names) != 1) {
				err = fmt.Errorf("队长须指定 1–2 名不同队友；同心之兰和永生之海仅接受 1 名")
			}
		}
	case "member":
		if p.LimitCount > 0 {
			s.Names, err = splitNames(p.CaptainNames)
			if err == nil && (len(s.Names) == 0 || p.Flow == "bond" && len(s.Names) != 1) {
				err = fmt.Errorf("队员须指定允许接邀的队长；同心之兰仅接受 1 名")
			}
		}
	default:
		return s, fmt.Errorf("未知 party_role")
	}
	return s, err
}

func load(ctx *maa.Context) (state, bool) {
	var s state
	raw, err := ctx.GetAnchor(stateAnchor)
	ok := err == nil && json.Unmarshal([]byte(raw), &s) == nil && s.Role != ""
	return s, ok
}

func save(ctx *maa.Context, s state) bool {
	raw, err := json.Marshal(s)
	return err == nil && ctx.SetAnchor(stateAnchor, string(raw)) == nil
}

func (s *state) recordFight() bool {
	if !s.InRound || s.Fights >= s.LimitCount {
		return false
	}
	s.InRound = false
	s.Fights++
	return true
}

type StateAction struct{}

func (*StateAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	var p struct {
		Config
		Op string `json:"op"`
	}
	p.Config = Config{Role: "captain", Flow: "party", Dungeon: "soul_10", LimitCount: 10}
	if arg == nil || json.Unmarshal([]byte(arg.CustomActionParam), &p) != nil {
		return false
	}
	if p.Op == "" || p.Op == "init" {
		s, err := newState(p.Config)
		if err != nil {
			log.Warn().Err(err).Msg("组队参数无效")
			return runstate.Publish(ctx, runstate.Failed, "party_invalid_parameters")
		}
		if s.Role == "captain" && !clearHits(ctx, "组队管控-打开建房", "组队管控-勾私密甲",
			"组队管控-勾私密乙", "组队管控-层数向下") {
			return runstate.Publish(ctx, runstate.Failed, "party_build_room_reset_failed")
		}
		runstate.SetMetric(ctx, "party_battles", 0)
		return save(ctx, s)
	}
	s, ok := load(ctx)
	if !ok {
		return false
	}
	switch p.Op {
	case "start":
		if s.Fights >= s.LimitCount || s.Role == "member" && !s.authorized(time.Now()) {
			return false
		}
		if s.Role == "member" {
			s.MemberJoined = true
		}
		s.InRound = true
	case "settled":
		if s.recordFight() {
			runstate.SetMetric(ctx, "party_battles", s.Fights)
		}
	case "failed":
		if s.End == "" {
			s.End = "party_state_unconfirmed"
		}
	case "invite_timeout":
		s.End = "party_invitation_timeout"
	case "lost":
		s.End = "party_room_disbanded"
		s.clearInvitation()
	case "manual_idle":
		s.End = "party_manual_member_idle"
	case "cleanup":
		s.clearInvitation()
		if !clearHits(ctx, "组队管控-取消再邀", "组队管控-关闭邀请", "组队管控-退出房间",
			"组队管控-确认退出房间", "组队管控-恢复关闭面板", "组队管控-恢复返回",
			"组队管控-恢复快捷返回", "组队管控-恢复结算", "组队管控-恢复关闭结算",
			"组队管控-恢复点击继续", "组队管控-恢复领取奖励", "组队管控-归位打开面板",
			"组队管控-恢复打开房间加成") {
			return runstate.Publish(ctx, runstate.Failed, "party_cleanup_reset_failed")
		}
	case "terminal":
		switch {
		case s.End == "party_invitation_timeout" || s.End == "party_room_disbanded" || s.End == "party_manual_member_idle":
			return runstate.Publish(ctx, runstate.Deferred, s.End)
		case s.End != "":
			return runstate.Publish(ctx, runstate.Failed, s.End)
		case s.Role == "manual" && s.LimitCount > 0 && s.Fights == s.LimitCount:
			return runstate.Publish(ctx, runstate.Completed, "party_manual_budget_reached")
		case s.LimitCount > 0 && s.Fights == s.LimitCount && s.Flow != "bond":
			return runstate.Publish(ctx, runstate.Completed, "party_battle_budget_reached")
		default:
			return runstate.Publish(ctx, runstate.Failed, "party_terminal_unconfirmed")
		}
	case "cleanup_failure":
		runstate.SetMetric(ctx, "party_bonus_cleanup_pending", bonus_toggle.OwnsAny(ctx))
		if s.End != "party_teammate_not_found" {
			s.End = "party_courtyard_or_bonus_unconfirmed"
		}
		return runstate.Publish(ctx, runstate.Failed, s.End)
	default:
		return false
	}
	return save(ctx, s)
}

type StateRecognition struct{}

func (*StateRecognition) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	var p struct {
		Is string `json:"is"`
	}
	if arg == nil || json.Unmarshal([]byte(arg.CustomRecognitionParam), &p) != nil {
		return nil, false
	}
	s, ok := load(ctx)
	if !ok {
		return nil, false
	}
	match := false
	switch p.Is {
	case "manual", "solo", "captain", "member":
		match = p.Is == s.Role
	case "awake":
		match = s.Flow == "awake"
	case "sea":
		match = s.Dungeon == "eternity_sea"
	case "zero":
		match = s.LimitCount == 0
	case "done":
		match = s.LimitCount > 0 && s.Fights == s.LimitCount
	case "member_authorized":
		match = s.authorized(time.Now())
	case "can_settle":
		match = s.InRound && s.Fights < s.LimitCount
	case "has_bonus":
		match = bonus_toggle.OwnsAny(ctx)
	case "bond_done":
		match = s.Flow == "bond" && s.End == "" && s.LimitCount > 0 && s.Fights == s.LimitCount
	case "selected":
		match = len(s.Names) > 0 && len(s.Selected) == len(s.Names)
	case "missing":
		match = s.End == "party_teammate_not_found"
	}
	if !match {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: maa.Rect{0, 0, 1, 1}}, true
}
