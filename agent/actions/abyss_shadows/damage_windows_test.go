//go:build windows

package abyss_shadows

import (
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"testing"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type damageOCRFixture struct {
	frame image.Image
	value int64
	ok    bool
}

func (f *damageOCRFixture) Run(ctx *maa.Context, _ *maa.CustomActionArg) bool {
	f.value, f.ok = readDamage(ctx, f.frame)
	return f.ok
}

func TestNativeAbyssDamageOCR(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	lib := os.Getenv("MAAFW_LIB_DIR")
	if lib == "" {
		lib = filepath.Join(root, "maafw")
	}
	if _, err := os.Stat(filepath.Join(lib, "MaaFramework.dll")); err != nil {
		t.Skipf("offline MaaFramework unavailable: %v", err)
	}
	if err := maa.Init(maa.WithLibDir(lib), maa.WithLogDir(filepath.Join(root, "debug", "abyss-ocr"))); err != nil {
		t.Fatal(err)
	}
	defer maa.Release()
	t.Logf("native damage OCR: MaaFramework %s", maa.Version())
	res, err := maa.NewResource()
	if err != nil {
		t.Fatal(err)
	}
	defer res.Destroy()
	for _, bundle := range []string{"base", "official"} {
		if !res.PostBundle(filepath.Join(root, "resource_pack", bundle)).Wait().Success() {
			t.Fatalf("load %s bundle", bundle)
		}
	}

	const probe = "AbyssDamageOCRFixture"
	p := &damageOCRFixture{frame: syntheticDamageFrame()}
	if err := res.RegisterCustomAction(probe, p); err != nil {
		t.Fatal(err)
	}
	ctrl := replayController(t)
	defer ctrl.Destroy()
	if !ctrl.PostConnect().Wait().Success() {
		t.Fatal("start test-owned controller")
	}
	tasker, err := maa.NewTasker()
	if err != nil {
		t.Fatal(err)
	}
	defer tasker.Destroy()
	if err := tasker.BindResource(res); err != nil {
		t.Fatal(err)
	}
	if err := tasker.BindController(ctrl); err != nil {
		t.Fatal(err)
	}
	job := tasker.PostTask(probe, map[string]any{
		probe: map[string]any{"action": "Custom", "custom_action": probe, "timeout": 500, "post_delay": 0},
	}).Wait()
	if !job.Success() || !p.ok || p.value != 30_000_000 {
		t.Fatalf("preprocessed damage OCR: job=%v damage=%d readable=%v", job.Status(), p.value, p.ok)
	}
}

func syntheticDamageFrame() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{30, 48, 80, 255}), image.Point{}, draw.Src)
	ink := image.NewUniform(color.RGBA{255, 209, 52, 255})
	digits := map[rune][7]string{
		'3': {"01110", "10001", "00001", "00110", "00001", "10001", "01110"},
		'0': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	}
	x := 118
	for _, digit := range "30000000" {
		for y, row := range digits[digit] {
			for column, active := range row {
				if active == '1' {
					p := image.Pt(x+4*column, 120+4*y)
					draw.Draw(img, image.Rectangle{Min: p, Max: p.Add(image.Pt(4, 4))}, ink, image.Point{}, draw.Src)
				}
			}
		}
		x += 26
	}
	draw.Draw(img, image.Rect(103, 113, 107, 117), ink, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(331, 139, 335, 143), ink, image.Point{}, draw.Src)
	return img
}
