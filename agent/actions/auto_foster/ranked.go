package auto_foster

import (
	"fmt"
	"image"
	"slices"
	"sort"
	"strings"
	"time"

	"maa-yys-agent/internal/vision"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const (
	maxFriendsPerTab  = 10
	maxFriendPages    = 4
	legacyFriendPages = 6 // The old selector examined up to six pages per tab.
	fosterResult      = "好友寄养结果"
	sameTab           = "好友寄养_切换同服"
	crossTab          = "好友寄养_切换跨区"
)

type friendRow struct {
	name  string
	box   maa.Rect
	stars int
}

func starsForRow(box maa.Rect, detections []*maa.RecognitionResult) int {
	// The star strip sits just below the friend's name. Adjacent rows are
	// roughly 106 px apart, so a bounded y offset avoids borrowing their card.
	center := box.Y() + box.Height()/2
	bestStars, bestScore := 0, 0.0
	for _, hit := range detections {
		result, ok := hit.AsNeuralNetworkDetect()
		if !ok || result.ClsIndex > 5 || result.Score <= bestScore {
			continue
		}
		offset := result.Box.Y() + result.Box.Height()/2 - center
		if offset >= 12 && offset <= 42 {
			bestStars, bestScore = int(result.ClsIndex)+1, result.Score
		}
	}
	return bestStars
}

func isFriendName(img image.Image) bool {
	if img == nil || img.Bounds().Empty() {
		return false
	}
	b := img.Bounds()
	dark, orange := 0, 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			h, s, v := vision.HSV(img.At(x, y))
			switch {
			case h >= 10 && h <= 52 && s >= .47 && v >= .5:
				orange++
			case v < .59 && s < .32:
				dark++
			}
		}
	}
	return dark*100 >= 4*b.Dx()*b.Dy() && orange < dark
}

type fosterScan struct {
	ctx        *maa.Context
	controller *maa.Controller
}

func (s fosterScan) frame() image.Image {
	return vision.Screencap(s.ctx.GetTasker(), 0)
}

func (s fosterScan) task(node string, overrides ...any) (string, bool) {
	if s.ctx.GetTasker().Stopping() {
		return "", false
	}
	d, err := s.ctx.RunTask(node, overrides...)
	if err != nil || d == nil || !d.Status.Success() || len(d.NodeDetails) == 0 {
		return "", false
	}
	return d.NodeDetails[len(d.NodeDetails)-1].Name, true
}

func (s fosterScan) recognize(node string, img image.Image) (*maa.RecognitionDetail, error) {
	d, err := s.ctx.RunRecognition(node, img)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", node, err)
	}
	if d == nil {
		return nil, fmt.Errorf("%s 未返回识别结果", node)
	}
	return d, nil
}

func bestOCR(d *maa.RecognitionDetail) string {
	if d.Hit && d.Results != nil && d.Results.Best != nil {
		if r, ok := d.Results.Best.AsOCR(); ok {
			return r.Text
		}
	}
	return ""
}

// Compare the selected tabs' peach panel and its darker upper band in the two
// known positions, away from label ink. A label alone is not evidence of selection.
func selectedTab(img image.Image) string {
	check := func(x int) bool {
		band := func(top, bottom int, shaded bool) bool {
			rect := image.Rect(x, top, x+10, bottom)
			if !rect.In(img.Bounds()) {
				return false
			}
			matches := 0
			for y := top; y < bottom; y++ {
				for xx := x; xx < x+10; xx++ {
					r16, g16, b16, _ := img.At(xx, y).RGBA()
					r, g, b := int(r16>>8), int(g16>>8), int(b16>>8)
					if shaded && r >= 204 && r <= 234 && r-g >= 37 && r-g <= 70 && g-b >= 30 && g-b <= 57 ||
						!shaded && r >= 221 && r <= 245 && r-g >= 23 && r-g <= 46 && g-b >= 32 && g-b <= 55 {
						matches++
					}
				}
			}
			return matches*4 >= rect.Dx()*rect.Dy()*3
		}
		return band(106, 115, true) && band(141, 148, false)
	}
	if img == nil {
		return ""
	}
	same, cross := check(223), check(344)
	switch {
	case same && !cross:
		return sameTab
	case cross && !same:
		return crossTab
	default:
		return ""
	}
}

func (s fosterScan) verifyTab(img image.Image, tab string) (bool, error) {
	same, err := s.recognize("好友寄养_同服标签", img)
	if err != nil {
		return false, err
	}
	cross, err := s.recognize("好友寄养_跨区标签", img)
	return err == nil && same.Hit && cross.Hit && selectedTab(img) == tab, err
}

func (s fosterScan) rows(img image.Image, ranked bool) ([]friendRow, bool, error) {
	r, err := s.recognize("好友寄养_好友名单OCR", img)
	if err != nil {
		return nil, false, err
	}
	var rows []friendRow
	var names []*maa.RecognitionResult
	if r.Hit && r.Results != nil {
		names = r.Results.Filtered
	}
	for _, hit := range names {
		text, ok := hit.AsOCR()
		if !ok {
			continue
		}
		name := strings.Join(strings.Fields(text.Text), "")
		box := text.Box
		// Leave the partially clipped bottom row for the overlapping next page;
		// its name can be visible while its star strip is still off-screen.
		if box.Y()+box.Height()/2 > 576 || name == "" || name == "最近寄养" ||
			!isFriendName(vision.Crop(img, image.Rect(box.X(), box.Y(), box.X()+box.Width(), box.Y()+box.Height()))) {
			continue
		}
		rows = append(rows, friendRow{name: name, box: box})
	}
	empty, err := s.recognize("好友寄养_名单为空", img)
	if err != nil || empty.Hit {
		return nil, err == nil, err
	}
	if ranked && len(rows) > 0 {
		detected, err := s.recognize("好友寄养_候选星级", img)
		if err != nil {
			return nil, false, err
		}
		if detected.Hit && detected.Results != nil {
			for i := range rows {
				rows[i].stars = starsForRow(rows[i].box, detected.Results.Filtered)
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].box.Y() < rows[j].box.Y() })
	return rows, false, nil
}

func (s fosterScan) readCandidate(tab string, row friendRow, ranked bool) (candidate, error) {
	c := candidate{host: host{tab, row.name}, stars: row.stars}
	if ranked && row.stars > 0 && row.stars < 4 {
		return c, nil
	}
	const selectRow = "好友寄养_点好友行"
	if _, ok := s.task(selectRow, map[string]any{
		selectRow: map[string]any{"target": [4]int{row.box.X(), row.box.Y(), row.box.Width(), row.box.Height()}},
	}); !ok {
		return c, fmt.Errorf("无法选择好友行")
	}
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		selected := s.frame()
		if selected == nil {
			return c, fmt.Errorf("无法读取所选好友卡片画面")
		}
		// A positive no-card state is different from a missed card name or stars.
		empty, err := s.recognize("好友寄养_好友无生效卡", selected)
		if err != nil {
			return c, err
		}
		if empty.Hit {
			return c, nil
		}
		expectedYield := ""
		if ranked {
			title, err := s.recognize("好友寄养_卡名OCR", selected)
			if err != nil {
				return c, err
			}
			name := bestOCR(title)
			if name == "" {
				continue
			}
			drum, fish := strings.Contains(name, "太鼓"), strings.Contains(name, "斗鱼")
			if !drum && !fish {
				return c, nil // A positively identified non-resource card.
			}
			if drum == fish {
				continue
			}
			expectedYield = "勾玉"
			if fish {
				expectedYield = "体力"
			}
			if c.stars == 0 {
				detected, err := s.recognize("好友寄养_候选星级", selected)
				if err != nil {
					return c, err
				}
				if detected.Hit && detected.Results != nil {
					c.stars = starsForRow(row.box, detected.Results.Filtered)
				}
			}
			if c.stars == 0 {
				continue
			}
			if c.stars < 4 {
				return c, nil
			}
		}
		value, err := s.recognize("阴阳寮奖励领取_结界寄养_识别结界卡收益", selected)
		if err != nil {
			return c, err
		}
		yieldType, yieldValue := parseRewardText(bestOCR(value))
		if yieldValue > 0 && (!ranked || yieldType == expectedYield) {
			c.RewardInfo = RewardInfo{yieldType, yieldValue}
			return c, nil
		}
	}
	return c, fmt.Errorf("所选卡片类型、星级或收益未能确认")
}

func (s fosterScan) readPage(tab string, ranked, requireRows bool) ([]friendRow, error) {
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		img := s.frame()
		if img == nil {
			return nil, fmt.Errorf("无法读取寄养好友列表")
		}
		ok, err := s.verifyTab(img, tab)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if !requireRows {
			return nil, nil
		}
		rows, empty, err := s.rows(img, ranked)
		if err != nil || empty || len(rows) > 0 {
			return rows, err
		}
	}
	return nil, fmt.Errorf("标签页 %s 未选中或好友列表无法识别", tab)
}

// visitTab starts at the top even when the game remembers each tab's scroll position.
// Stop as soon as ten distinct real friend rows have been seen, not ten eligible cards.
func (s fosterScan) visitTab(tab string, ranked bool, visit func(friendRow) (bool, error)) (bool, error) {
	if _, ok := s.task(tab); !ok {
		return false, fmt.Errorf("无法切换寄养标签页 %s", tab)
	}
	if _, err := s.readPage(tab, false, false); err != nil {
		return false, err
	}
	if _, ok := s.task("好友寄养_好友列表回顶"); !ok {
		return false, fmt.Errorf("寄养好友列表回顶失败")
	}
	return walkFriendPages(ranked, func() ([]friendRow, error) { return s.readPage(tab, ranked, true) }, func() error {
		if _, ok := s.task("好友寄养_好友列表向下"); !ok {
			return fmt.Errorf("寄养好友列表翻页失败")
		}
		return nil
	}, visit)
}

// A nil, successful page is reserved for a positively identified empty list.
func walkFriendPages(ranked bool, read func() ([]friendRow, error), swipe func() error, visit func(friendRow) (bool, error)) (bool, error) {
	budget := legacyFriendPages
	if ranked {
		budget = maxFriendPages
	}
	seen := make(map[string]bool)
	var previous []string
	for page := 0; page < budget; page++ {
		rows, err := read()
		if err != nil || len(rows) == 0 {
			return false, err
		}
		var names []string
		for _, row := range rows {
			names = append(names, row.name)
			if seen[row.name] {
				continue
			}
			seen[row.name] = true
			if stop, err := visit(row); stop || err != nil {
				return stop, err
			}
			if ranked && len(seen) == maxFriendsPerTab {
				return false, nil
			}
		}
		if slices.Equal(previous, names) || (!ranked && page+1 == budget) {
			return false, nil
		}
		previous = names
		if page+1 < budget {
			if err := swipe(); err != nil {
				return false, err
			}
		}
	}
	return false, fmt.Errorf("寄养好友列表翻页预算耗尽，未完成前十位检查")
}

type fosterWorkflow interface {
	visitTab(tab string, ranked bool, visit func(friendRow) (bool, error)) (bool, error)
	readCandidate(tab string, row friendRow, ranked bool) (candidate, error)
	submit() (bool, error)
}

func (s fosterScan) submit() (bool, error) {
	outcome, ok := s.task("好友寄养_入客验证")
	if !ok {
		return false, fmt.Errorf("无法核验好友客位")
	}
	switch outcome {
	case "好友寄养_提交完成":
		return true, nil
	case "好友寄养_客位已占", "好友寄养_确认客位冲突":
		if _, ok := s.task("好友寄养_重新打开列表"); !ok {
			return false, fmt.Errorf("客位已占，无法重新打开好友列表")
		}
		return false, nil
	default:
		return false, fmt.Errorf("未识别的寄养提交结果 %q", outcome)
	}
}

func runFoster(ctx *maa.Context, p FosterParams) error {
	s := fosterScan{ctx, ctx.GetTasker().GetController()}
	if s.controller == nil {
		return fmt.Errorf("寄养控制器不可用")
	}
	anchor, err := selectFoster(s, p)
	if err != nil || anchor == "" {
		return err
	}
	return ctx.SetAnchor(fosterResult, anchor)
}

func selectFoster(s fosterWorkflow, p FosterParams) (string, error) {
	preferred := "勾玉"
	if p.FosterTarget == 2 {
		preferred = "体力"
	}
	threshold := getDirectSelectThreshold(preferred, p.DirectSelectThreshold)
	ranked := p.Mode == rankedSubmit
	var candidates []candidate
	for _, tab := range []string{sameTab, crossTab} {
		found, err := s.visitTab(tab, ranked, func(row friendRow) (bool, error) {
			c, err := s.readCandidate(tab, row, ranked)
			if c.YieldValue > 0 {
				candidates = append(candidates, c)
			}
			return !ranked && shouldDirectSelect(c.RewardInfo, preferred, threshold), err
		})
		if found || err != nil {
			return "", err
		}
	}
	unconfirmed := false
	for _, best := range orderCandidates(candidates, preferred, nil, ranked) {
		found, err := s.visitTab(best.tab, ranked, func(row friendRow) (bool, error) {
			if row.name != best.name {
				return false, nil
			}
			current, err := s.readCandidate(best.tab, row, ranked)
			return current == best, err
		})
		if err != nil {
			return "", err
		}
		if !found {
			unconfirmed = true
			continue
		}
		if !ranked {
			return "", nil
		}
		done, err := s.submit()
		if err != nil {
			return "", err
		}
		if done {
			return "好友寄养_返回后核验", nil
		}
	}
	if unconfirmed {
		return "", fmt.Errorf("好友列表窗口变化，未能重新核实全部候选")
	}
	if !ranked {
		return "", fmt.Errorf("没有可寄养的结界卡")
	}
	return "好友寄养_无候选", nil
}
