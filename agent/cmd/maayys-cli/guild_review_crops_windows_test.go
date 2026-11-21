//go:build windows

package main

import (
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maa-yys-agent/ocrtext"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestNativeGuildEntryOCR(t *testing.T) {
	const (
		// Synthetic text crops rendered in Microsoft YaHei, not game assets.
		title = "iVBORw0KGgoAAAANSUhEUgAAAFAAAAAiCAYAAADI+15nAAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAAGNSURBVGhD7ZDLisQwEAPz/z+9S2ACoeiHlHaGObhAB7slpePjbzPi4MXGYz/gkP2AQ8YPeBzjipCz963ulYw3fOtHr943uk9WdacN9x9YpW90K0yyJE3zIyv0je4OZpxsRJpWFuMyGfTwPMHpivbl2aVN3j86UdQ57WdXBXPqrKNNcOmnyqDPkYrif9J7UrqrQs547u4vunmEmnEfxfWftE4W8kwc78l9aVcZqi/DyfaOYCFHHfQ7iog8vOsUZTLySVCyQiS7r+gynHGHTsxWhFMWRsUd90yV5zcc/QLpFvcFubijDvod/QLSFlzckcPTHOEOU1XU0w9KEckyXG6l3vpGRT39wEJHhPOVUnC8ClITF3WkovpVX0SWje5U7CQ/Fi3FcwcfPcpn9ypdvptnyInsB7Mz7xWYpZ6i5lXfndbd/UR0zwznFcx1qqDXlULqqso44/wi8/A+E+G88l7Q52Y6SkdWoC5yEXncjohJtkPt1VyblP2AQ/YDDtkPOGQ/4JD9gEP2Aw7ZDzjkH1WijlBEQR1pAAAAAElFTkSuQmCC"
		zero  = "iVBORw0KGgoAAAANSUhEUgAAAIAAAAAiCAYAAACX6tEuAAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAAGQSURBVHhe7ZIxbsBADMPy/0+3uCFAwEo+dZYJZLBN36D4+VmqedhYutgDKGcPoJw9gHL2AMqJDuB5nj/fDfrJjoPvJG/RT3YauabCEJNA6SU7Du4nb9FLdloZE3HBuf40c/0Jt+P608z12xnTmAJzM9c/TDPF5LuZ6x+mWSs2jTcsF5iaq96X2/zLzVVz1ftymzdik0iCosNaQcf9FNUjdFgrEqcJm0QSFB3WCjpvzT3VI3RYKxKnCZtEEhQd1go6b8091SN0WCsSpwmbRBIUHdaKxDkkHh3WisRpwiaRBEWHtSJxDolHh7UicZqwSSRB0WGtSJxD4tFhrUicJmwSSVB0WCsS55B4dFgrEqcJm0QSFB3WisQ5JB4d1orEaWJMYgrLzVz/MM0Uk+9mrn+YZq2MabyBMTTXn2b/7U8z159mrt/ONY1vcPwc9KadaXbg/s0/0Et2WokSYYhJkPTdzm1+4DuT+0I/2WlkUylnD6CcPYBy9gDK2QMoZw+gnD2AcvYAytkDKGcPoJw9gHJ+AcMWtcAxinCbAAAAAElFTkSuQmCC"
		score = "iVBORw0KGgoAAAANSUhEUgAAAGgAAAAiCAYAAACz35yrAAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAAJFSURBVGhD7ZFRasAwDEN7/0tvZCxgHpLtNt0WRh74I5LsuM31cdiai8JhL84Dbc55oM1ZfqDrur7qDTpzOpk7cH+eHW/nHGVnNbxaIPNIZ1aVuYuap7RI5UfuZBVlZ3VB5j/5oS7v9FXcXKcPnBe/t1sVdSJZaOC8bAku+VY94W5vluc+naqoE9+4gUqvFuCSb9VP3cG5RGkTNeMO7c5s4ai53G/Dn7xSnKnu4blTHXqpb9RQXqYyCi7rSvW4cyTzIt3cgDm1J/fPqkMvlXDnsgiXdaV63DmSeZG7OVUZnUxG2cllnhZx+kT51HiOZF7E5ajFb4mVed3KyN0XLp9FnD5RPjWeI7xfVcy5Xkf0OPduZeTuC7gluKQr1ePOEc5RFXOq15F5b/PjN7mP5c9ypXrcOeI8zlY5pU2cPoizO9Whl1rALeP0ifKp8RxxntKjpvwJM8xSr6pDmeLQp0WcPlE+NZ4jzlN61JQfdVXMVHRzgzLFZZ4Woe9K9bgz9cxTmvImlT9Qfldz9FILuGX4U1ypHneu9IHy3H3R66CyXc3RSy3glnH6RPnUeK70gfKmRj16HVS2qzl6qQXcMk6fVP5AZaY29Xim5/yI0qIefWZVRuUyeqkF3DJOn1T+QGWo8Sc5LzvTU0Xo8dwlTXOJlSJKZw/9KqM0h8tx9swoTVHlMk+RpnjZShGlVz2DKqM04non1R2E+arnVpbC23SW+A88+c5Ovk4c/pTzQJtzHmhzzgNtznmgzTkPtDnngTbnPNDmfAJV3qjQcCd3iQAAAABJRU5ErkJggg=="
	)
	decode := func(data string) image.Image {
		img, err := png.Decode(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)))
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	blankTimer := drawReviewCrop(decode(zero), image.Pt(580, 51))
	ended := drawReviewCrop(decode(zero), image.Pt(580, 51))
	titleImage := decode(title)
	draw.Draw(ended, image.Rect(585, 3, 585+titleImage.Bounds().Dx(), 3+titleImage.Bounds().Dy()), titleImage, image.Point{}, draw.Src)
	battle := drawReviewCrop(decode(score), image.Pt(58, 82))
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		runCropProbe(t, res, tasker,
			cropCheck{"寮宴会-入场已完席", ended, true},
			cropCheck{"寮宴会-在席", ended, false},
			cropCheck{"寮宴会-入场已完席", blankTimer, false},
			cropCheck{"麒麟-开战中", battle, true},
			cropCheck{"麒麟-备战按钮", battle, false},
		)
	})
}

// Reference captures are read from a local yys-automation checkout rather
// than redistributed with the MIT resources.
func TestNativeGuildReviewCrops(t *testing.T) {
	ref := os.Getenv("MAAYYS_REVIEW_REFERENCE")
	if ref == "" {
		t.Skip("set MAAYYS_REVIEW_REFERENCE to yys-automation at 00252fc4 to test the real crops")
	}
	board, point := reviewCrop(t, ref, filepath.Join("tasks", "AbyssShadows", "res", "res_abyss_map.png"), image.Pt(306, 147))
	confirm, confirmPoint := reviewCrop(t, ref, filepath.Join("tests", "fixtures", "dokan_soul_confirm_20261028.png"), image.Pt(390, 210))
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		t.Logf("production recognizers on MaaFramework %s", maa.Version())
		const (
			panel      = "狭间暗域-战报面板"
			prompt     = "装备御魂-识别更换确认"
			button     = "装备御魂-确认按钮"
			click      = "装备御魂-点击确定"
			disappears = "装备御魂-确认框已关闭"
		)
		boardFrame := drawReviewCrop(board, point)
		confirmFrame := drawReviewCrop(confirm, confirmPoint)
		bg := image.NewUniform(color.RGBA{64, 60, 56, 255})
		buttonOnly := drawReviewCrop(confirm, confirmPoint)
		draw.Draw(buttonOnly, image.Rect(360, 192, 920, 354), bg, image.Point{}, draw.Src)
		promptOnly := drawReviewCrop(confirm, confirmPoint)
		draw.Draw(promptOnly, image.Rect(401, 354, 868, 489), bg, image.Point{}, draw.Src)
		runCropProbe(t, res, tasker,
			cropCheck{panel, boardFrame, true},
			cropCheck{panel, confirmFrame, false},
			cropCheck{prompt, confirmFrame, true},
			cropCheck{click, confirmFrame, true},
			cropCheck{"道馆-处理换装确认", confirmFrame, true},
			cropCheck{"狭间暗域-处理延迟换装确认", confirmFrame, true},
			cropCheck{button, buttonOnly, true},
			cropCheck{click, buttonOnly, false},
		)

		// Route captured frames through the same native OCR definitions and
		// let the actual Pipeline successor apply inverse.
		evidence := make(map[string]json.RawMessage)
		for _, node := range []string{prompt, button} {
			raw, err := res.GetNodeJSON(node)
			if err != nil {
				t.Fatal(err)
			}
			evidence[node] = json.RawMessage(raw)
		}
		for _, tc := range []struct {
			name     string
			frame    image.Image
			wantOpen bool
		}{
			{"named confirmation", confirmFrame, true},
			{"message still present", promptOnly, true},
			{"button still present", buttonOnly, true},
			{"dialog gone", boardFrame, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				const entry = "GuildConfirmDialogReplay"
				override := map[string]any{
					entry:      map[string]any{"next": []string{disappears}, "timeout": 650, "post_delay": 0},
					disappears: map[string]any{"post_delay": 0},
				}
				for node, spec := range evidence {
					name := t.Name() + "/" + node
					if err := res.RegisterCustomRecognition(name, cropRecognition(func(ctx *maa.Context) (*maa.CustomRecognitionResult, bool) {
						const sample = "GuildConfirmDialogSample"
						d, err := ctx.RunRecognition(sample, tc.frame, map[string]json.RawMessage{sample: spec})
						if err != nil {
							t.Error(err)
							return nil, false
						}
						if d == nil || !d.Hit {
							return nil, false
						}
						return &maa.CustomRecognitionResult{Box: d.Box}, true
					})); err != nil {
						t.Fatal(err)
					}
					override[node] = replayRecognizedBy(name)
				}
				job := tasker.PostTask(entry, override).Wait()
				if job.Failure() != tc.wantOpen || job.Success() == tc.wantOpen {
					t.Fatalf("native disappearance gate: job=%v dialog should still be open=%v", job.Status(), tc.wantOpen)
				}
			})
		}
	})
}

type cropRecognition func(*maa.Context) (*maa.CustomRecognitionResult, bool)

func (fn cropRecognition) Run(ctx *maa.Context, _ *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	return fn(ctx)
}

type cropCheck struct {
	name  string
	frame image.Image
	hit   bool
}

func runCropProbe(t *testing.T, res *maa.Resource, tasker *maa.Tasker, checks ...cropCheck) {
	t.Helper()
	const probe = "GuildReviewCropProbe"
	if err := res.RegisterCustomAction(probe, offlineAction(func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
		for _, tc := range checks {
			d, err := ctx.RunRecognition(tc.name, tc.frame)
			if hit := d != nil && d.Hit; err != nil || hit != tc.hit {
				t.Errorf("%s: hit=%v, want %v, OCR=%v, err=%v", tc.name, hit, tc.hit, ocrtext.Texts(d), err)
			}
		}
		return true
	})); err != nil {
		t.Fatal(err)
	}
	job := tasker.PostTask(probe, map[string]any{
		probe: map[string]any{"action": "Custom", "custom_action": probe, "post_delay": 0},
	}).Wait()
	if !job.Success() {
		t.Fatalf("native crop probe: %v", job.Status())
	}
}

func reviewCrop(t *testing.T, root, path string, point image.Point) (image.Image, image.Point) {
	t.Helper()
	img, err := loadPNG(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	return img, point
}

func drawReviewCrop(crop image.Image, point image.Point) *image.RGBA {
	frame := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	draw.Draw(frame, frame.Bounds(), image.NewUniform(color.RGBA{64, 60, 56, 255}), image.Point{}, draw.Src)
	draw.Draw(frame, image.Rectangle{Min: point, Max: point.Add(crop.Bounds().Size())}, crop, crop.Bounds().Min, draw.Src)
	return frame
}
