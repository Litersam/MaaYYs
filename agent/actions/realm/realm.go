package realm

import (
	"encoding/json"
	"fmt"
	"image"
	"regexp"
	"strconv"
	"strings"
	"time"

	"maa-yys-agent/internal/runstate"
	"maa-yys-agent/internal/vision"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type timerKind string

func (kind timerKind) limit() int {
	switch kind {
	case "foster":
		return 6*3600 + 120
	case "realm_card":
		return 72 * 3600
	default:
		return 0
	}
}

var timerPattern = regexp.MustCompile(`^([0-9]{2}):([0-9]{2}):([0-9]{2})$`)

func remainingSeconds(kind timerKind, text string) (int, bool) {
	parts := timerPattern.FindStringSubmatch(strings.ReplaceAll(strings.TrimSpace(text), "：", ":"))
	if len(parts) != 4 {
		return 0, false
	}
	h, _ := strconv.Atoi(parts[1])
	m, _ := strconv.Atoi(parts[2])
	s, _ := strconv.Atoi(parts[3])
	seconds := h*3600 + m*60 + s
	if m >= 60 || s >= 60 || seconds <= 0 || seconds > kind.limit() {
		return 0, false
	}
	return seconds, true
}

type timerObservation struct {
	Kind       timerKind `json:"kind"`
	Remaining  int       `json:"remaining"`
	ObservedAt int64     `json:"observed_at"`
}

func (o timerObservation) validFor(kind timerKind) bool {
	return o.Kind == kind && o.Remaining > 0 && o.Remaining <= kind.limit() && o.ObservedAt > 0
}

func (o timerObservation) metrics() map[string]int64 {
	prefix := string(o.Kind)
	// Remaining is an observation, not a countdown rebased to task completion.
	return map[string]int64{
		prefix + "_remaining_sec": int64(o.Remaining),
		prefix + "_due_at":        o.ObservedAt + int64(o.Remaining),
	}
}

type TimerProbe struct{}

func (*TimerProbe) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	var params struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal([]byte(arg.CustomRecognitionParam), &params) != nil {
		return nil, false
	}
	var node string
	kind := timerKind(params.Kind)
	switch params.Kind {
	case "foster":
		node = "好友寄养_计时OCR"
	case "card":
		node = "结界挂卡_计时OCR"
		kind = "realm_card"
	default:
		return nil, false
	}
	observedAt := time.Now().Unix()
	d, err := ctx.RunRecognition(node, arg.Img)
	if err != nil || d == nil || !d.Hit || d.Results == nil || d.Results.Best == nil {
		return nil, false
	}
	r, ok := d.Results.Best.AsOCR()
	if !ok {
		return nil, false
	}
	seconds, ok := remainingSeconds(kind, r.Text)
	if !ok {
		return nil, false
	}
	data, err := json.Marshal(timerObservation{kind, seconds, observedAt})
	if err != nil {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: r.Box, Detail: string(data)}, true
}

type Finish struct{}

const returnToCourtyard = "结界批次_返回庭院"
const courtyardConfirmed = "结界批次_已确认庭院"

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
	var observation timerObservation
	if p.Metric != "" {
		kind, supported := strings.CutSuffix(p.Metric, "_remaining_sec")
		if arg.RecognitionDetail != nil && arg.RecognitionDetail.Hit {
			observation = timerResult(arg.RecognitionDetail, timerKind(kind))
		}
		if !supported || !observation.validFor(timerKind(kind)) {
			p.Status = runstate.Failed
		}
	}
	if p.Status != runstate.Failed {
		d, err := ctx.RunTask(returnToCourtyard)
		if err != nil || !confirmedReturn(d) || ctx.GetTasker().Stopping() {
			fmt.Println("结界任务未能确认已返回庭院")
			p.Status = runstate.Failed
		}
	}
	if p.Status != runstate.Failed && observation.Remaining > 0 {
		for key, value := range observation.metrics() {
			runstate.SetMetric(ctx, key, value)
		}
	}
	runstate.Report(ctx, p.Status)
	return p.Status != runstate.Failed
}

func confirmedReturn(d *maa.TaskDetail) bool {
	if d == nil || !d.Status.Success() || len(d.NodeDetails) == 0 {
		return false
	}
	last := d.NodeDetails[len(d.NodeDetails)-1]
	return last != nil && last.Name == courtyardConfirmed && last.RunCompleted
}

func timerResult(d *maa.RecognitionDetail, kind timerKind) timerObservation {
	// The framework does not populate Hit on children of a successful And.
	if d == nil {
		return timerObservation{}
	}
	if d.Results != nil && d.Results.Best != nil {
		if result, ok := d.Results.Best.AsCustom(); ok {
			var o timerObservation
			if json.Unmarshal([]byte(result.Detail), &o) == nil && o.validFor(kind) {
				return o
			}
		}
	}
	for _, part := range d.CombinedResult {
		if o := timerResult(part, kind); o.Remaining > 0 {
			return o
		}
	}
	return timerObservation{}
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
