package challenge_dungeon_boss

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestChallengeButtonRejectsGrayscaleVersionOfTemplate(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "..", "resource_pack", "base", "image", "战斗", "自动地鬼", "地鬼-识别挑战位置_1.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	template, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	w, h := template.Bounds().Dx(), template.Bounds().Dy()
	frame := image.NewRGBA(image.Rect(0, 0, w, 2*h))
	gray := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(gray, gray.Bounds(), template, template.Bounds().Min, draw.Src)
	draw.Draw(frame, gray.Bounds(), gray, image.Point{}, draw.Src)
	draw.Draw(frame, image.Rect(0, h, w, 2*h), template, template.Bounds().Min, draw.Src)
	boxes := []maa.Rect{{0, h, w, h}, {0, 0, w, h}}

	if _, ok := challengeButton(frame, boxes, 0); ok {
		t.Fatal("gray row was accepted")
	}
	if box, ok := challengeButton(frame, boxes, 1); !ok || box[1] != h {
		t.Fatalf("enabled row rejected: %v, %t", box, ok)
	}
	draw.Draw(frame, gray.Bounds(), image.NewUniform(color.RGBA{131, 128, 121, 255}), image.Point{}, draw.Src)
	if _, ok := challengeButton(frame, boxes, 0); ok {
		t.Fatal("near-gray row was accepted")
	}
}

func TestCountsOnlyDistinctConfirmedBattles(t *testing.T) {
	names := []string{"涂壁", "涂壁", "", "帚神"}
	battles, closes := 0, 0
	count := fightRequested(ChallengeParams{Count: 3, IsReward: true}, func(_ bool, i int) (string, bool) {
		name := names[i+1]
		return name, name != ""
	}, func() bool {
		battles++
		return true
	}, func() bool {
		closes++
		return true
	})
	if count != 2 || battles != 2 || closes != 1 {
		t.Fatalf("duplicates or unavailable rows altered count: count=%d battles=%d closes=%d", count, battles, closes)
	}
}

func TestVictoryDoesNotTreatFrameworkSuccessAsWin(t *testing.T) {
	node := func(name string) *maa.NodeDetail { return &maa.NodeDetail{Name: name, RunCompleted: true} }
	start := node("自动地鬼-匹配挑战图片")
	win := node("自动地鬼6")
	returned := node("地鬼-确认胜利并返回")
	tests := []struct {
		name  string
		nodes []*maa.NodeDetail
		want  bool
	}{
		{"skip", []*maa.NodeDetail{node("跳过")}, false},
		{"stale settlement", []*maa.NodeDetail{win, returned}, false},
		{"loss", []*maa.NodeDetail{start, node("地鬼-战斗失败"), returned}, false},
		{"return missing", []*maa.NodeDetail{start, win}, false},
		{"confirmed", []*maa.NodeDetail{start, win, returned}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &maa.TaskDetail{Status: maa.StatusSuccess, NodeDetails: tt.nodes}
			if got := confirmedVictory(task); got != tt.want {
				t.Fatalf("confirmedVictory = %t, want %t", got, tt.want)
			}
		})
	}
}
