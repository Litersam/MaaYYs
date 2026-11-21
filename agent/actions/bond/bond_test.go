package bond

import "testing"

func TestBondPartnerRequiresOneExactName(t *testing.T) {
	for _, tc := range []struct {
		input, want string
	}{
		{`  好友·(A)  `, "好友·(A)"},
		{`["好友 B"]`, ""},
		{`["好友 A","好友 B"]`, ""},
		{`[]`, ""},
		{"A，B", ""},
		{"A\nB", ""},
	} {
		got, valid := partner(tc.input)
		if valid != (tc.want != "") || valid && got != tc.want {
			t.Errorf("partner(%q) = %q, %t; want %q", tc.input, got, valid, tc.want)
		}
	}
}

func TestBondGrowthAndRoundBudget(t *testing.T) {
	for _, tc := range []struct {
		texts []string
		want  progress
	}{
		{[]string{"每天限购2次(1/2)", "成长值： 7／10", "7/10"}, progress{7, 10}},
		{[]string{"-0/10"}, progress{0, 10}},
		{[]string{"10/20"}, progress{10, 20}},
		{[]string{"20/20"}, progress{20, 20}},
		{[]string{"9/20"}, progress{}},
		{[]string{"110/10"}, progress{}},
		{[]string{"1/2", "2/10", "10/20"}, progress{}},
	} {
		p, valid := readProgress(tc.texts)
		if p != tc.want || valid != (tc.want.Target != 0) {
			t.Errorf("readProgress(%q) = %+v, %t; want %+v", tc.texts, p, valid, tc.want)
		}
	}
	s := state{Limit: 10}
	if s.budget(progress{0, 10}) != 10 || s.budget(progress{7, 10}) != 3 {
		t.Fatal("first round must use the remaining growth, not start over")
	}
	s.UsedTarget = 10
	if s.budget(progress{7, 10}) != 0 || s.budget(progress{10, 20}) != 10 {
		t.Fatal("first round must not rerun; return gift unlocks a separate second budget")
	}
	s.Limit = 4
	if s.budget(progress{11, 20}) != 4 {
		t.Fatal("configured per-round cap was exceeded")
	}
}
