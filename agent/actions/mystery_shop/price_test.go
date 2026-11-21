package mystery_shop

import "testing"

func TestOnlyVerifiedOneTicketWithinUnitCap(t *testing.T) {
	tests := []struct {
		name       string
		price, qty []string
		max        int
		want       bool
	}{
		{"below cap", []string{"50"}, []string{"1"}, 85, true},
		{"at cap", []string{"85"}, []string{"1"}, 85, true},
		{"above cap", []string{"86"}, []string{"1"}, 85, false},
		{"narrow user cap", []string{"85"}, []string{"1"}, 80, false},
		{"unknown price", nil, []string{"1"}, 85, false},
		{"misread price", []string{"9"}, []string{"1"}, 85, false},
		{"ambiguous price", []string{"85", "90"}, []string{"1"}, 85, false},
		{"trailing OCR noise", []string{"85 0"}, []string{"1"}, 85, false},
		{"unknown quantity", []string{"85"}, nil, 85, false},
		{"unexpected quantity", []string{"85"}, []string{"2"}, 85, false},
		{"ambiguous quantity", []string{"85"}, []string{"1", "2"}, 85, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allowPurchase(tt.price, tt.qty, tt.max); got != tt.want {
				t.Fatalf("allowPurchase = %t, want %t", got, tt.want)
			}
		})
	}
}
