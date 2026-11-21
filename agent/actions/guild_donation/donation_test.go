package guild_donation

import "testing"

func TestParseFractionRejectsPartialAndAmbiguousOCR(t *testing.T) {
	for _, tc := range []struct {
		text []string
		want fraction
		ok   bool
	}{
		{[]string{" ２９ ／ ３０ "}, fraction{29, 30}, true},
		{[]string{"0/0"}, fraction{0, 0}, true},
		{[]string{"4/12000"}, fraction{4, 12000}, true},
		{[]string{"31/30"}, fraction{}, false},
		{[]string{"完成 29/30"}, fraction{}, false},
		{[]string{"2/30/40"}, fraction{}, false},
		{[]string{"7/?"}, fraction{}, false},
		{[]string{"29/30", "28/30"}, fraction{}, false},
		{nil, fraction{}, false},
	} {
		got, ok := parseFraction(tc.text)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseFraction(%v) = %+v, %v; want %+v, %v", tc.text, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPickRowRequiresOneSufficientStock(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stock [4]fraction
		row   int
		ok    bool
	}{
		{"use largest", [4]fraction{{0, 20}, {1, 60}, {0, 60}, {0, 5}}, 1, true},
		{"exact inventory sufficient", [4]fraction{{0, 10}}, 0, true},
		{"cannot combine insufficient rows", [4]fraction{{0, 8}, {0, 7}}, -1, true},
		{"all empty", [4]fraction{}, -1, true},
		{"other row was already selected", [4]fraction{{1, 10}, {0, 50}}, -1, false},
		{"already exceeds deficit", [4]fraction{{11, 100}}, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, ok := pickRow(10, tc.stock)
			if row != tc.row || ok != tc.ok {
				t.Fatalf("row %d, ok %v; want %d, %v", row, ok, tc.row, tc.ok)
			}
		})
	}
}

func TestSelectQuantityRequiresOneFreshIncrementPerClick(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial fraction
		frames  []fraction
		clicks  int
		ok      bool
	}{
		{"respect initial selection", fraction{2, 50}, []fraction{{3, 50}, {4, 50}}, 2, true},
		{"already exact", fraction{4, 50}, nil, 0, true},
		{"already too large", fraction{5, 50}, nil, 0, false},
		{"insufficient stock", fraction{2, 3}, nil, 0, false},
		{"miss permits no retry", fraction{2, 50}, nil, 1, false},
		{"stale permits no retry", fraction{2, 50}, []fraction{{2, 50}}, 1, false},
		{"even a jump to target is unsafe", fraction{2, 50}, []fraction{{4, 50}}, 1, false},
		{"stock changed while selecting", fraction{2, 50}, []fraction{{3, 49}}, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clicks, reads := 0, 0
			ok := selectQuantity(4, tc.initial, func() bool {
				if clicks != reads {
					t.Fatal("clicked again before reading the previous click")
				}
				clicks++
				return true
			}, func() (fraction, bool) {
				reads++
				if reads > len(tc.frames) {
					return fraction{}, false
				}
				return tc.frames[reads-1], true
			})
			if ok != tc.ok || clicks != tc.clicks || reads != tc.clicks {
				t.Fatalf("ok %v after %d clicks/%d captures; want %v after %d", ok, clicks, reads, tc.ok, tc.clicks)
			}
		})
	}
}
