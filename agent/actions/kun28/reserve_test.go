package kun28

import "testing"

func TestReserveRequiresTwoConsecutiveEmptyCaptures(t *testing.T) {
	tests := []struct {
		name      string
		frames    [][]string
		captures  int
		replenish bool
	}{
		{"reuse partial", [][]string{{"1/50"}}, 1, false},
		{"reuse full", [][]string{{"50/50"}}, 1, false},
		{"verified empty", [][]string{{"0/50"}, {"0 ／ 50"}}, 2, true},
		{"restored stock", [][]string{{"0/50"}, {"5/50"}}, 2, false},
		{"miss then empty", [][]string{nil, {"0/50"}}, 2, false},
		{"failed second capture", [][]string{{"0/50"}, nil}, 2, false},
		{"wrong denominator", [][]string{{"0/5"}, {"0/50"}}, 2, false},
		{"contradictory OCR", [][]string{{"0/50", "8/50"}, {"0/50"}}, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captures := 0
			_, got := inspectReserve(func() int {
				defer func() { captures++ }()
				return reserveCount(tt.frames[captures])
			})
			if got != tt.replenish || captures != tt.captures {
				t.Fatalf("replenish/captures = %t/%d, want %t/%d", got, captures, tt.replenish, tt.captures)
			}
		})
	}
}
