package mystery_shop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

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
			if got := allowPurchase(quantityWindow, tt.price, tt.qty, tt.max); got != tt.want {
				t.Fatalf("allowPurchase = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestDistinguishesSingleAndQuantityWindowsBeforeReadingQuantity(t *testing.T) {
	for _, tc := range []struct {
		name string
		jade maa.Rect
		plus bool
		qty  []string
		want bool
	}{
		{"single with positive button position", maa.Rect{604, 500, 42, 48}, false, nil, true},
		{"obscured plus window", maa.Rect{604, 537, 42, 52}, false, nil, false},
		{"plus window with unreadable quantity", maa.Rect{604, 537, 42, 52}, true, nil, false},
		{"plus window with one", maa.Rect{604, 537, 42, 52}, true, []string{"1"}, true},
		{"contradictory controls", maa.Rect{604, 500, 42, 48}, true, []string{"1"}, false},
		{"unknown confirmation location", maa.Rect{720, 500, 42, 48}, false, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := allowPurchase(purchaseWindow(tc.jade, tc.plus), []string{"65"}, tc.qty, 85); got != tc.want {
				t.Fatalf("accepted = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestExistingWeeklyCompletionPreemptsAllShopIngresses(t *testing.T) {
	path := filepath.Join("..", "..", "..", "resource_pack", "base", "pipeline", "日常", "商店购买.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]struct{ Next []string }
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	entry := p["商店购买"].Next
	const gate = "商店购买-检查本周是否已完成"
	if len(entry) < 2 || entry[0] != gate || !strings.Contains(strings.Join(entry[1:], ","), "秘魂屋-限价购买入口") {
		t.Fatalf("weekly guard must short-circuit before opening mystery shop: %v", entry)
	}
	if next := p[gate].Next; len(next) != 1 || next[0] != "导航-庭院" {
		t.Fatalf("same-week path must not return to the shopping sequence: %v", next)
	}
}
