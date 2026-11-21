package realm

import (
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRemainingSeconds(t *testing.T) {
	for text, want := range map[string]int{
		"00:00:01": 1, " 05：59：59 ": 21599, "06:02:00": 21720,
		"00:00:00": 0, "06:02:01": 0, "05:60:00": 0, "00:05:60": 0,
		"6:00:00": 0, "00:0O:07": 0, "约01:02:03": 0, "21:00:00": 0,
	} {
		got, ok := remainingSeconds("foster", text)
		if got != want || ok != (want > 0) {
			t.Errorf("foster %q = %d, %t; want %d, %t", text, got, ok, want, want > 0)
		}
	}
	for text, want := range map[string]int{"21:00:00": 75600, "72:00:00": 259200, "72:00:01": 0, "99:00:00": 0} {
		got, ok := remainingSeconds("realm_card", text)
		if got != want || ok != (want > 0) {
			t.Errorf("realm card %q = %d, %t; want %d, %t", text, got, ok, want, want > 0)
		}
	}
}

func TestAbsoluteDeadlineSurvivesLateFinish(t *testing.T) {
	observed := time.Date(2026, 11, 21, 18, 10, 0, 0, time.FixedZone("UTC+8", 8*3600))
	finished := observed.Add(20 * time.Minute)
	got := (timerObservation{"foster", 600, observed.Unix()}).metrics()
	if got["foster_remaining_sec"] != 600 || got["foster_due_at"] != observed.Add(10*time.Minute).Unix() ||
		got["foster_due_at"] >= finished.Unix() {
		t.Fatalf("must preserve original already-due observation, got %v", got)
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
		Action   string   `json:"action"`
		Custom   string   `json:"custom_action"`
		Next     []string `json:"next"`
		Error    []string `json:"on_error"`
		And      []any    `json:"all_of"`
		MaxHit   int      `json:"max_hit"`
		Expected any      `json:"expected"`
		Params   struct {
			Status string `json:"status"`
			Metric string `json:"metric"`
		} `json:"custom_action_param"`
	}
	all := map[string]node{}
	for _, name := range []string{"好友寄养", "结界挂卡", "结界收菜", "结界批次导航"} {
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
		if n.Custom != "RealmFinish" || n.Params.Status != want[1] || n.Params.Metric != want[2] ||
			!slices.Contains(n.And, any(want[0])) {
			t.Errorf("%s must gate status/metric on timer: %+v", name, n)
		}
		if next := all["结界挂卡"].Next; len(next) < 2 || next[0] != "结界挂卡_结算自家卡" || next[1] != "结界挂卡_进入卡页" {
			t.Fatalf("own-card settlement must be checked before entering the card panel: %v", next)
		}
		if next := all["结界挂卡_收下自家卡奖励"].Next; !slices.Equal(next, []string{"结界挂卡_确认结算完成"}) {
			t.Errorf("settlement must confirm reward overlay gone: %v", next)
		}
		if n := all["结界挂卡_筛选已生效"]; !slices.Equal(n.Error, []string{"结界批次_失败"}) || all["结界挂卡_翻页"].MaxHit <= 0 {
			t.Errorf("a bounded scan without a recognized end must fail: %+v", n)
		}
		for _, name := range []string{"好友寄养", "结界挂卡", "结界收菜"} {
			n := all[name]
			if len(n.Error) != 0 || !slices.Contains(n.Next, "[JumpBack]结界批次_进入结界") {
				t.Errorf("%s must fail without a business terminal when safe navigation fails: %+v", name, n)
			}
		}
		for _, name := range []string{"结界批次_进入结界", returnToCourtyard} {
			for _, edge := range all[name].Next {
				dest := strings.TrimPrefix(edge, "[JumpBack]")
				n, ok := all[dest]
				if !ok || len(n.Next)+len(n.Error) != 0 || n.Action == "StopTask" || n.Custom != "" ||
					(strings.HasPrefix(edge, "[JumpBack]") && n.MaxHit <= 0) {
					t.Errorf("%s unsafe or unbounded navigation edge %s: %+v", name, edge, n)
				}
			}
		}
		zero := regexp.MustCompile(all["结界收菜_计数为零"].Expected.(string))
		positive := regexp.MustCompile(all["结界收菜_计数为正"].Expected.(string))
		for _, tc := range []struct {
			page, text     string
			staminaEmpty   bool
			expEmpty       bool
			collectStamina bool
		}{
			{"结界收菜_经验页", "0/100", false, true, false},
			{"结界收菜_体力页", "0 ／ 100", true, false, false},
			{"结界收菜_体力页", "10/100", false, false, true},
		} {
			emptyOn := func(name string) bool {
				n := all[name]
				return slices.Contains(n.And, any(tc.page)) && slices.Contains(n.And, any("结界收菜_计数为零")) && zero.MatchString(tc.text)
			}
			take := all["结界收菜_取体力"]
			if emptyOn("结界收菜_体力未到") != tc.staminaEmpty || emptyOn("结界收菜_经验未到") != tc.expEmpty ||
				(slices.Contains(take.And, any(tc.page)) && slices.Contains(take.And, any("结界收菜_计数为正")) && positive.MatchString(tc.text)) != tc.collectStamina {
				t.Errorf("harvest on %s with %s does not distinguish tab and whole count", tc.page, tc.text)
			}
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
