package challenge_dungeon_boss

import "testing"

func TestBossNameUsesUnicodeAndRejectsAmbiguity(t *testing.T) {
	tests := []struct {
		texts []string
		want  string
	}{
		{[]string{"涂壁"}, "涂壁"},
		{[]string{" 𠮷·鬼王 "}, "𠮷·鬼王"},
		{[]string{"涂 壁"}, ""},
		{[]string{"涂壁3"}, ""},
		{[]string{"·涂壁"}, ""},
		{[]string{"涂··壁"}, ""},
		{[]string{"壁"}, ""},
		{[]string{"涂壁", "山童"}, ""},
		{[]string{"涂壁", "涂壁"}, "涂壁"},
	}
	for _, tc := range tests {
		if got := bossName(tc.texts); got != tc.want {
			t.Errorf("bossName(%q)=%q, want %q", tc.texts, got, tc.want)
		}
	}
}
