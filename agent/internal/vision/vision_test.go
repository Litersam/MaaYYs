package vision

import (
	"image"
	"image/color"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestFrameReadsOCROnlyOnce(t *testing.T) {
	var calls atomic.Int32
	f := NewFrame(image.NewGray(image.Rect(0, 0, 1280, 720)), func(image.Image) []maa.OCRResult {
		calls.Add(1)
		return []maa.OCRResult{
			{Box: maa.Rect{490, 420, 84, 30}, Text: " 同意并登录 "},
			{Box: maa.Rect{250, 135, 20, 20}, Text: "同意"},
		}
	})
	re := regexp.MustCompile(`^同意(并登录)?$`)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := f.FindText(maa.Rect{360, 340, 560, 170}, re); got == nil || got.X() != 490 {
				t.Errorf("matched wrong region: %v", got)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("performed %d OCR passes", calls.Load())
	}
}

func TestCropAndSaturation(t *testing.T) {
	img := image.NewRGBA(image.Rect(8, 12, 10, 13))
	img.SetRGBA(8, 12, color.RGBA{R: 255, G: 255, A: 255})
	img.SetRGBA(9, 12, color.RGBA{R: 90, G: 90, B: 90, A: 255})
	crop := Crop(img, image.Rect(7, 11, 11, 15))
	if crop.Bounds() != image.Rect(0, 0, 2, 1) || MeanSaturation(crop) != .5 {
		t.Fatalf("incorrect clipped crop: bounds=%v saturation=%v", crop.Bounds(), MeanSaturation(crop))
	}
	h, s, v := HSV(crop.At(0, 0))
	if h != 60 || s != 1 || v != 1 || Crop(img, image.Rect(10, 12, 12, 13)) != nil {
		t.Fatalf("unexpected HSV or accepted an empty crop: %v %v %v", h, s, v)
	}
}
