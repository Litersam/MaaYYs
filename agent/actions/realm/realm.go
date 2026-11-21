package realm

import (
	"encoding/json"
	"image"
	"regexp"
	"strconv"
	"strings"

	"maa-yys-agent/internal/runstate"
	"maa-yys-agent/internal/vision"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const maxRemainingSeconds = 6*60*60 + 120

var timerPattern = regexp.MustCompile(`^([0-9]{2}):([0-9]{2}):([0-9]{2})$`)

func remainingSeconds(text string) (int, bool) {
	parts := timerPattern.FindStringSubmatch(strings.ReplaceAll(strings.TrimSpace(text), "：", ":"))
	if len(parts) != 4 {
		return 0, false
	}
	h, _ := strconv.Atoi(parts[1])
	m, _ := strconv.Atoi(parts[2])
	s, _ := strconv.Atoi(parts[3])
	seconds := h*3600 + m*60 + s
	if m >= 60 || s >= 60 || seconds <= 0 || seconds > maxRemainingSeconds {
		return 0, false
	}
	return seconds, true
}

type TimerProbe struct{}

func (*TimerProbe) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	var p struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal([]byte(arg.CustomRecognitionParam), &p) != nil {
		return nil, false
	}
	node := ""
	switch p.Kind {
	case "foster":
		node = "好友寄养_计时OCR"
	case "card":
		node = "结界挂卡_计时OCR"
	default:
		return nil, false
	}
	d, err := ctx.RunRecognition(node, arg.Img)
	if err != nil || d == nil || !d.Hit || d.Results == nil || d.Results.Best == nil {
		return nil, false
	}
	r, ok := d.Results.Best.AsOCR()
	if !ok {
		return nil, false
	}
	seconds, ok := remainingSeconds(r.Text)
	if !ok {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: r.Box, Detail: strconv.Itoa(seconds)}, true
}

type Finish struct{}

func (*Finish) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	var p struct {
		Status runstate.Status `json:"status"`
		Metric string          `json:"metric"`
	}
	if json.Unmarshal([]byte(arg.CustomActionParam), &p) != nil {
		p.Status = runstate.Failed
	}
	switch p.Status {
	case runstate.Completed, runstate.Skipped, runstate.Failed:
	default:
		p.Status = runstate.Failed
	}
	seconds := 0
	if p.Metric != "" {
		if arg.RecognitionDetail != nil && arg.RecognitionDetail.Hit {
			seconds = timerResult(arg.RecognitionDetail)
		}
		if seconds == 0 || (p.Metric != "foster_remaining_sec" && p.Metric != "realm_card_remaining_sec") {
			p.Status = runstate.Failed
		}
	}
	d, err := ctx.RunTask("结界批次_返回庭院")
	if err != nil || d == nil || !d.Status.Success() {
		p.Status = runstate.Failed
	}
	if p.Status != runstate.Failed && seconds > 0 {
		runstate.SetMetric(ctx, p.Metric, seconds)
	}
	runstate.Report(ctx, p.Status)
	return p.Status != runstate.Failed
}

func timerResult(d *maa.RecognitionDetail) int {
	// The framework does not populate Hit on children of a successful And.
	if d == nil {
		return 0
	}
	if d.Results != nil && d.Results.Best != nil {
		if result, ok := d.Results.Best.AsCustom(); ok {
			seconds, err := strconv.Atoi(result.Detail)
			if err == nil && seconds > 0 && seconds <= maxRemainingSeconds {
				return seconds
			}
		}
	}
	for _, part := range d.CombinedResult {
		if seconds := timerResult(part); seconds > 0 {
			return seconds
		}
	}
	return 0
}

type ActivateButton struct{}

func (*ActivateButton) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	d, err := ctx.RunRecognition("结界挂卡_激活按钮模板", arg.Img)
	if err != nil || d == nil || !d.Hit {
		return nil, false
	}
	x, y, w, h := d.Box.X(), d.Box.Y(), d.Box.Width(), d.Box.Height()
	// The button rim contains warm UI decoration even when its center is gray.
	core := vision.Crop(arg.Img, image.Rect(x+w/5, y+h/5, x+w-w/5, y+h-h/5))
	if !isYellowActivation(core) {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: d.Box}, true
}

func isYellowActivation(img image.Image) bool {
	if img == nil || img.Bounds().Empty() || vision.MeanSaturation(img) < .24 {
		return false
	}
	b := img.Bounds()
	yellow, gray := 0, 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			h, s, v := vision.HSV(img.At(x, y))
			switch {
			case h >= 24 && h <= 70 && s >= .3 && v >= .45:
				yellow++
			case s < .2:
				gray++
			}
		}
	}
	return yellow*100 >= b.Dx()*b.Dy()*35 && yellow > gray
}

type GrowthSelect struct{}

func (*GrowthSelect) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	p := struct {
		Order int `json:"growth_shikigami_order"`
	}{Order: 7}
	if arg.CustomActionParam != "" && json.Unmarshal([]byte(arg.CustomActionParam), &p) != nil {
		return false
	}
	const count = 7
	xs := [count]int32{213, 345, 486, 621, 754, 889, 1027}
	if p.Order < 1 || p.Order > count {
		return false
	}
	controller := ctx.GetTasker().GetController()
	return controller != nil && controller.PostClick(xs[p.Order-1], 575).Wait().Success()
}
