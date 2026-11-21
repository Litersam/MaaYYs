package vision

import (
	"image"
	"regexp"
	"strings"
	"sync"

	"maa-yys-agent/ocrtext"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type Frame struct {
	Image image.Image
	once  sync.Once
	read  func(image.Image) []maa.OCRResult
	items []maa.OCRResult
}

// NewFrame caches one full-frame OCR pass for all ROI queries on this screenshot.
// Treat img as immutable for the lifetime of the frame.
func NewFrame(img image.Image, read func(image.Image) []maa.OCRResult) *Frame {
	return &Frame{Image: img, read: read}
}

func CaptureFrame(ctx *maa.Context, img image.Image) *Frame {
	return NewFrame(img, func(img image.Image) []maa.OCRResult {
		b := img.Bounds()
		return OCRItems(ctx, img, maa.Rect{b.Min.X, b.Min.Y, b.Dx(), b.Dy()})
	})
}

func (f *Frame) ItemsInROI(roi maa.Rect) []maa.OCRResult {
	if f == nil || f.Image == nil || f.read == nil {
		return nil
	}
	f.once.Do(func() { f.items = f.read(f.Image) })
	var out []maa.OCRResult
	for _, it := range f.items {
		if boxInROI(it.Box, roi) {
			out = append(out, it)
		}
	}
	return out
}

func boxInROI(box, roi maa.Rect) bool {
	b, r := ImageRect(box), ImageRect(roi)
	if b.Empty() || r.Empty() {
		return false
	}
	if b.Min.Add(b.Size().Div(2)).In(r) {
		return true
	}
	overlap := b.Intersect(r)
	return 2*overlap.Dx()*overlap.Dy() >= b.Dx()*b.Dy()
}

func (f *Frame) FindText(roi maa.Rect, pattern *regexp.Regexp) *maa.Rect {
	if pattern != nil {
		for _, it := range f.ItemsInROI(roi) {
			if pattern.MatchString(strings.TrimSpace(it.Text)) {
				box := it.Box
				return &box
			}
		}
	}
	return nil
}

func OCRItems(ctx *maa.Context, img image.Image, roi maa.Rect) []maa.OCRResult {
	detail, err := ctx.RunRecognitionDirect(maa.RecognitionTypeOCR, &maa.OCRParam{
		ROI: maa.NewTargetRect(roi),
	}, img)
	if err != nil || detail == nil {
		return nil
	}
	var out []maa.OCRResult
	if detail.Results != nil {
		for _, r := range detail.Results.All {
			if r != nil {
				if item, ok := r.AsOCR(); ok && item != nil && strings.TrimSpace(item.Text) != "" {
					out = append(out, *item)
				}
			}
		}
	}
	if len(out) == 0 {
		for _, text := range ocrtext.Texts(detail) {
			out = append(out, maa.OCRResult{Box: detail.Box, Text: text})
		}
	}
	return out
}

// FindCroppedText runs a separate pass for text missed by full-frame OCR. Using
// an actual crop gives an unambiguous conversion back to screenshot coordinates.
func FindCroppedText(ctx *maa.Context, img image.Image, roi maa.Rect, expr string) *maa.Rect {
	if img == nil {
		return nil
	}
	rect := ImageRect(roi).Intersect(img.Bounds())
	crop := Crop(img, rect)
	if crop == nil {
		return nil
	}
	detail, err := ctx.RunRecognitionDirect(maa.RecognitionTypeOCR, &maa.OCRParam{
		Expected: []string{expr},
	}, crop)
	if err != nil || detail == nil || !detail.Hit || ImageRect(detail.Box).Empty() {
		return nil
	}
	box := detail.Box
	box[0] += rect.Min.X
	box[1] += rect.Min.Y
	return &box
}
