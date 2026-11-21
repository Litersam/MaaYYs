package guild_donation

import (
	"encoding/json"
	"image"
	"regexp"
	"strconv"
	"strings"
	"time"

	"maa-yys-agent/internal/vision"
	"maa-yys-agent/ocrtext"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

// Donate fills the verified daily deficit using one material row.
type Donate struct{}

const (
	dailyLimit   = 30
	maxRefreshes = 20
	panelNode    = "寮捐-面板"
	progressNode = "寮捐-进度"
	counterNode  = "寮捐-材料计数"
	plusNode     = "寮捐-材料加号"
	submitNode   = "寮捐-材料提交按钮"
	terminalNode = "寮捐-庭院终态"
)

var materialTitles = map[string]string{
	"低级觉醒材料": "觉醒一",
	"中级觉醒材料": "觉醒二",
	"高级觉醒材料": "觉醒三",
	"低级御灵材料": "御灵一",
	"中级御灵材料": "御灵二",
	"高级御灵材料": "御灵三",
}

var missions = [...]struct{ title, description, submit maa.Rect }{
	{maa.Rect{333, 135, 92, 44}, maa.Rect{162, 183, 283, 124}, maa.Rect{231, 468, 141, 55}},
	{maa.Rect{667, 136, 94, 41}, maa.Rect{498, 183, 283, 124}, maa.Rect{567, 468, 143, 55}},
	{maa.Rect{1002, 133, 91, 42}, maa.Rect{833, 183, 283, 124}, maa.Rect{903, 470, 136, 58}},
}

var rows = [...]struct{ counter, plus maa.Rect }{
	{maa.Rect{427, 172, 100, 27}, maa.Rect{893, 133, 70, 68}},
	{maa.Rect{429, 293, 100, 31}, maa.Rect{893, 253, 72, 71}},
	{maa.Rect{428, 416, 100, 29}, maa.Rect{893, 376, 70, 68}},
	{maa.Rect{430, 536, 100, 33}, maa.Rect{893, 499, 69, 68}},
}

type fraction struct{ selected, total int }

var counterPattern = regexp.MustCompile(`^([0-9]{1,8})[ \t]*/[ \t]*([0-9]{1,8})$`)

func parseFraction(texts []string) (fraction, bool) {
	if len(texts) != 1 {
		return fraction{}, false
	}
	text := strings.Map(func(r rune) rune {
		switch {
		case r >= '０' && r <= '９':
			return r - '０' + '0'
		case r == '／':
			return '/'
		default:
			return r
		}
	}, texts[0])
	m := counterPattern.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return fraction{}, false
	}
	selected, _ := strconv.Atoi(m[1])
	total, _ := strconv.Atoi(m[2])
	return fraction{selected, total}, selected <= total
}

// pickRow chooses the largest stock as in CollectiveMissions. A distinct row
// with a nonzero selection could change the eventual donation total.
func pickRow(need int, stock [len(rows)]fraction) (index int, ok bool) {
	index = 0
	for i, f := range stock {
		if f.total > stock[index].total {
			index = i
		}
	}
	for i, f := range stock {
		if f.selected > need || (i != index && f.selected != 0) {
			return -1, false
		}
	}
	if stock[index].total < need {
		return -1, true
	}
	return index, true
}

func selectQuantity(need int, initial fraction, clickPlus func() bool, readFresh func() (fraction, bool)) bool {
	if need < 1 || need > dailyLimit || initial.total < need || initial.selected < 0 || initial.selected > need {
		return false
	}
	for selected := initial.selected; selected < need; selected++ {
		if !clickPlus() {
			return false
		}
		// No second click on a stale, unreadable, or unexpectedly advanced frame.
		got, ok := readFresh()
		if !ok || got.total != initial.total || got.selected != selected+1 {
			return false
		}
	}
	return true
}

func (*Donate) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	var p struct {
		Material string `json:"material"`
	}
	if json.Unmarshal([]byte(arg.CustomActionParam), &p) != nil || materialTitles[p.Material] == "" {
		return false
	}
	tasker := ctx.GetTasker()
	s := session{ctx, tasker, tasker.GetController()}
	frame := s.capture(0)
	progress, ok := s.progress(frame)
	if !ok {
		return false
	}
	if progress == dailyLimit {
		return s.finish("skipped", "already_at_daily_limit")
	}
	if !s.openMaterial(frame, p.Material, progress) {
		return false
	}
	frame = s.capture(0)
	stock, ok := s.stock(frame)
	if !ok {
		return false
	}
	need := dailyLimit - progress
	row, ok := pickRow(need, stock)
	if !ok {
		return false
	}
	if row < 0 {
		return s.run("寮捐-离开材料页", nil) && s.finish("skipped", "insufficient_material")
	}
	if !selectQuantity(need, stock[row], func() bool {
		return s.click(s.recognize(frame, plusNode, rows[row].plus, ""))
	}, func() (fraction, bool) {
		frame = s.capture(350 * time.Millisecond)
		if s.recognize(frame, submitNode, maa.Rect{}, "") == nil {
			return fraction{}, false
		}
		return s.readCounter(frame, counterNode, rows[row].counter)
	}) {
		return false
	}
	// Recheck every row on the final frame before making the irreversible click.
	final, ok := s.stock(frame)
	if !ok {
		return false
	}
	stock[row].selected = need
	if final != stock || !s.click(s.recognize(frame, submitNode, maa.Rect{}, "")) {
		return false
	}
	return s.run("寮捐-提交后等待面板", nil) && s.finish("completed", "")
}

type session struct {
	ctx    *maa.Context
	tasker *maa.Tasker
	ctrl   *maa.Controller
}

func (s session) capture(settle time.Duration) image.Image {
	time.Sleep(settle)
	if s.tasker.Stopping() || !s.ctrl.PostScreencap().Wait().Success() {
		return nil
	}
	img, err := s.ctrl.CacheImage()
	if err != nil {
		return nil
	}
	return img
}

func (s session) recognize(frame image.Image, node string, roi maa.Rect, expected string) *maa.RecognitionDetail {
	if frame == nil || s.tasker.Stopping() {
		return nil
	}
	params := map[string]any{}
	if roi != (maa.Rect{}) {
		params["roi"] = roi
	}
	if expected != "" {
		params["expected"] = expected
	}
	var override map[string]any
	if len(params) > 0 {
		override = map[string]any{node: params}
	}
	d, err := s.ctx.RunRecognition(node, frame, override)
	if err != nil || d == nil || !d.Hit {
		return nil
	}
	return d
}

func (s session) readCounter(frame image.Image, node string, roi maa.Rect) (fraction, bool) {
	return parseFraction(ocrtext.Texts(s.recognize(frame, node, roi, "")))
}

func (s session) progress(frame image.Image) (int, bool) {
	if s.recognize(frame, panelNode, maa.Rect{}, "") == nil {
		return 0, false
	}
	f, ok := s.readCounter(frame, progressNode, maa.Rect{})
	return f.selected, ok && f.total == dailyLimit
}

func (s session) stock(frame image.Image) ([len(rows)]fraction, bool) {
	var stock [len(rows)]fraction
	if s.recognize(frame, submitNode, maa.Rect{}, "") == nil {
		return stock, false
	}
	for i, r := range rows {
		f, ok := s.readCounter(frame, counterNode, r.counter)
		if !ok {
			return stock, false
		}
		stock[i] = f
	}
	return stock, true
}

func (s session) openMaterial(frame image.Image, material string, progress int) bool {
	title := "^" + materialTitles[material] + "$"
	description := "^提交" + material + "$"
	for refreshes := 0; ; refreshes++ {
		for _, m := range missions {
			if s.recognize(frame, "寮捐-任务种类", m.title, title) == nil &&
				s.recognize(frame, "寮捐-任务描述", m.description, description) == nil {
				continue
			}
			if button := s.recognize(frame, "寮捐-任务提交按钮", m.submit, ""); button != nil {
				return s.click(button) && s.run("寮捐-确认材料页", nil)
			}
		}
		if refreshes == maxRefreshes || !s.click(s.recognize(frame, "寮捐-刷新任务", maa.Rect{}, "")) {
			return false
		}
		frame = s.capture(800 * time.Millisecond)
		if n, ok := s.progress(frame); !ok || n != progress {
			return false
		}
	}
}

func (s session) click(detail *maa.RecognitionDetail) bool {
	if detail == nil || s.tasker.Stopping() {
		return false
	}
	r := vision.ImageRect(detail.Box)
	if r.Empty() || !r.In(image.Rect(0, 0, 1280, 720)) {
		return false
	}
	p := r.Min.Add(r.Size().Div(2))
	return s.ctrl.PostClick(int32(p.X), int32(p.Y)).Wait().Success()
}

func (s session) run(node string, override map[string]any) bool {
	if s.tasker.Stopping() {
		return false
	}
	d, err := s.ctx.RunTask(node, override)
	return err == nil && d != nil && d.Status.Success()
}

func (s session) finish(status, reason string) bool {
	return s.run("寮捐-离开面板", map[string]any{
		terminalNode: map[string]any{"custom_action_param": map[string]string{"status": status, "reason": reason}},
	})
}
