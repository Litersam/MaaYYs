package battle_team

import (
	"regexp"
	"testing"
)

func TestBattleTeamMatchesOnlyExactNames(t *testing.T) {
	override := buildPresetOverride(resolvePositionBox("御魂"), "A+B", "队伍1")
	for _, tc := range []struct {
		node, exact string
		other       []string
	}{
		{"出战队伍预设-点击分组", "A+B", []string{"AAAB", "A+B2", "ZA+B"}},
		{"出战队伍预设-识别出战队伍", "队伍1", []string{"队伍10", "甲队伍1"}},
	} {
		r := regexp.MustCompile(override[tc.node].(map[string]any)["expected"].(string))
		if !r.MatchString(tc.exact) {
			t.Errorf("%s rejects exact name %q", r, tc.exact)
		}
		for _, other := range tc.other {
			if r.MatchString(other) {
				t.Errorf("%s incorrectly matches %q", r, other)
			}
		}
	}
}
