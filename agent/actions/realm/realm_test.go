package realm

import (
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRemainingSeconds(t *testing.T) {
	for text, want := range map[string]int{
		"00:00:01": 1, " 05：59：59 ": 21599, "06:02:00": 21720,
		"00:00:00": 0, "06:02:01": 0, "05:60:00": 0, "00:05:60": 0,
		"6:00:00": 0, "00:0O:07": 0, "约01:02:03": 0,
	} {
		got, ok := remainingSeconds(text)
		if got != want || ok != (want > 0) {
			t.Errorf("remainingSeconds(%q) = %d, %t; want %d, %t", text, got, ok, want, want > 0)
		}
	}
}

func TestActivateButtonRejectsGrayEvenWithYellowDecoration(t *testing.T) {
	yellow := color.RGBA{241, 177, 62, 255}
	for _, tc := range []struct {
		name      string
		base      color.Color
		goldWidth int
		want      bool
	}{
		{"yellow on dark brown", color.RGBA{65, 44, 30, 255}, 6, true},
		{"gray with golden trim", color.RGBA{173, 169, 163, 255}, 2, false},
		{"similarly saturated red", color.RGBA{229, 74, 38, 255}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := image.Rect(3, 5, 15, 15)
			img := image.NewRGBA(b)
			draw.Draw(img, b, image.NewUniform(tc.base), image.Point{}, draw.Src)
			draw.Draw(img, image.Rect(b.Min.X, b.Min.Y, b.Min.X+tc.goldWidth, b.Max.Y), image.NewUniform(yellow), image.Point{}, draw.Src)
			if got := isYellowActivation(img); got != tc.want {
				t.Fatalf("activation=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestRealmFlowConstraints(t *testing.T) {
	type node struct {
		Action string   `json:"custom_action"`
		Next   []string `json:"next"`
		And    []any    `json:"all_of"`
		Params struct {
			Status string `json:"status"`
			Metric string `json:"metric"`
		} `json:"custom_action_param"`
	}
	all := map[string]node{}
	for _, name := range []string{"好友寄养", "结界挂卡", "结界收菜"} {
		path := filepath.Join("..", "..", "..", "resource_pack", "base", "pipeline", "日常", name+".json")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &all); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	if next := all["好友寄养_结界就绪"].Next; !slices.Equal(next, []string{"好友寄养_领取到期奖励", "好友寄养_进入育成"}) {
		t.Fatalf("expired rewards must be checked before foster entry; next = %v", next)
	}
	for name, want := range map[string][3]string{
		"好友寄养_已寄养":   {"好友寄养_计时", "skipped", "foster_remaining_sec"},
		"好友寄养_寄养成功":  {"好友寄养_计时", "completed", "foster_remaining_sec"},
		"结界挂卡_已有生效卡": {"结界挂卡_计时", "skipped", "realm_card_remaining_sec"},
		"结界挂卡_挂卡成功":  {"结界挂卡_计时", "completed", "realm_card_remaining_sec"},
	} {
		n := all[name]
		if n.Action != "RealmFinish" || n.Params.Status != want[1] || n.Params.Metric != want[2] ||
			!slices.Contains(n.And, any(want[0])) {
			t.Errorf("%s must gate status/metric on timer: %+v", name, n)
		}
	}
	for name, n := range all {
		if !strings.HasPrefix(name, "结界收菜") {
			continue
		}
		for _, next := range n.Next {
			if dest := strings.TrimPrefix(next, "[JumpBack]"); strings.HasPrefix(dest, "好友寄养") || strings.HasPrefix(dest, "结界挂卡") {
				t.Errorf("collect-only branch %s can advance to %s", name, dest)
			}
		}
	}
}
