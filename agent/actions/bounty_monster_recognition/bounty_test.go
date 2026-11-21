package bounty_monster_recognition

import "testing"

func TestInviteWhitelistFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		params   string
		observed []string
		want     bool
	}{
		{"exact match", `{"whitelist":"甲号，乙号\nOwnAccount"}`, []string{"乙号"}, true},
		{"array", `{"whitelist":["甲号", "乙号"]}`, []string{"甲号"}, true},
		{"not a substring", `{"whitelist":"甲号"}`, []string{"别人甲号"}, false},
		{"not case folded", `{"whitelist":"OwnAccount"}`, []string{"ownaccount"}, false},
		{"not an invitation sentence", `{"whitelist":"甲号"}`, []string{"甲号邀请你协作"}, false},
		{"ambiguous OCR", `{"whitelist":"甲号,乙号"}`, []string{"甲号", "乙号"}, false},
		{"empty OCR", `{"whitelist":"甲号"}`, nil, false},
		{"empty config", `{"whitelist":""}`, []string{"甲号"}, false},
		{"invalid config", `{"whitelist":[47]}`, []string{"甲号"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := allowedSender(parseWhitelist(tc.params), tc.observed); got != tc.want {
				t.Fatalf("allowed = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUnknownAndSkippedRowsDoNotOpen(t *testing.T) {
	for _, tc := range []struct {
		visible bool
		skipped bool
		clue    string
		want    bool
	}{
		{true, false, "觉", true},
		{true, false, "?", false},
		{true, false, " ？？ ", false},
		{true, true, "觉", false},
		{false, false, "觉", false},
	} {
		if got := eligibleRow(tc.visible, tc.skipped, isUnknownClue(tc.clue)); got != tc.want {
			t.Errorf("visible %v, skipped %v, clue %q: eligible = %v, want %v", tc.visible, tc.skipped, tc.clue, got, tc.want)
		}
	}
}
