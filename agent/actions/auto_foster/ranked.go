package auto_foster

import (
	"fmt"
	"image"
	"sort"
	"strings"

	"maa-yys-agent/internal/vision"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const (
	maxFriendsPerTab = 10
	maxFriendPages   = 4
	fosterResult     = "好友寄养结果"
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
	if s.ctx.GetTasker().Stopping() || !s.controller.PostScreencap().Wait().Success() {
		return nil
	}
	img, _ := s.controller.CacheImage()
	return img
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

func (s fosterScan) recognize(node string, img image.Image) *maa.RecognitionResults {
	d, err := s.ctx.RunRecognition(node, img)
	if err != nil || d == nil || !d.Hit {
		return nil
	}
	return d.Results
}

func (s fosterScan) rows(img image.Image) []friendRow {
	r := s.recognize("好友寄养_好友名单OCR", img)
	if r == nil {
		return nil
	}
	var stars []*maa.RecognitionResult
	if detected := s.recognize("好友寄养_候选星级", img); detected != nil {
		stars = detected.Filtered
	}
	var rows []friendRow
	for _, hit := range r.Filtered {
		text, ok := hit.AsOCR()
		if !ok {
			continue
		}
		name := strings.Join(strings.Fields(text.Text), "")
		box := text.Box
		// Leave the partially clipped bottom row for the overlapping next page;
		// its name can be visible while its star strip is still off-screen.
		if box.Y()+box.Height()/2 > 576 || name == "" || strings.HasPrefix("最近寄养", name) ||
			!isFriendName(vision.Crop(img, image.Rect(box.X(), box.Y(), box.X()+box.Width(), box.Y()+box.Height()))) {
			continue
		}
		rows = append(rows, friendRow{name, box, starsForRow(box, stars)})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].box.Y() < rows[j].box.Y() })
	return rows
}

func (s fosterScan) readCandidate(tab string, row friendRow) (candidate, error) {
	c := candidate{host: host{tab, row.name}, stars: row.stars}
	if row.stars < 4 {
		return c, nil
	}
	const selectRow = "好友寄养_点好友行"
	if _, ok := s.task(selectRow, map[string]any{
		selectRow: map[string]any{"target": [4]int{row.box.X(), row.box.Y(), row.box.Width(), row.box.Height()}},
	}); !ok {
		return c, fmt.Errorf("无法选择好友行")
	}
	selected := s.frame()
	if selected == nil {
		return c, fmt.Errorf("无法读取所选好友卡片画面")
	}
	title := s.recognize("好友寄养_卡名OCR", selected)
	value := s.recognize("阴阳寮奖励领取_结界寄养_识别结界卡收益", selected)
	if title == nil || value == nil || title.Best == nil || value.Best == nil {
		return c, nil
	}
	t, ok := title.Best.AsOCR()
	v, valueOK := value.Best.AsOCR()
	if !ok || !valueOK {
		return c, nil
	}
	yieldType, yieldValue := parseRewardText(v.Text)
	if !(strings.Contains(t.Text, "太鼓") && yieldType == "勾玉") &&
		!(strings.Contains(t.Text, "斗鱼") && yieldType == "体力") {
		return c, nil
	}
	c.RewardInfo = RewardInfo{yieldType, yieldValue}
	return c, nil
}

// visitTab starts at the top even when the game remembers each tab's scroll position.
// Stop as soon as ten distinct real friend rows have been seen, not ten eligible cards.
func (s fosterScan) visitTab(tab string, visit func(friendRow) (bool, error)) (bool, error) {
	if _, ok := s.task(tab); !ok {
		return false, fmt.Errorf("无法切换寄养标签页 %s", tab)
	}
	seen := make(map[string]bool)
	for page := 0; page < maxFriendPages; page++ {
		img := s.frame()
		if img == nil {
			return false, fmt.Errorf("无法读取寄养好友列表")
		}
		before := len(seen)
		for _, row := range s.rows(img) {
			if seen[row.name] {
				continue
			}
			seen[row.name] = true
			if stop, err := visit(row); stop || err != nil {
				return stop, err
			}
			if len(seen) == maxFriendsPerTab {
				return false, nil
			}
		}
		if len(seen) == before || page+1 == maxFriendPages {
			break
		}
		if _, ok := s.task("好友寄养_好友列表向下"); !ok {
			return false, fmt.Errorf("寄养好友列表翻页失败")
		}
	}
	return false, nil
}

func runRankedFoster(ctx *maa.Context, p FosterParams) error {
	preferred := "勾玉"
	if p.FosterTarget == 2 {
		preferred = "体力"
	}
	s := fosterScan{ctx, ctx.GetTasker().GetController()}
	if s.controller == nil {
		return fmt.Errorf("寄养控制器不可用")
	}
	threshold := getDirectSelectThreshold(preferred, p.DirectSelectThreshold)
	var candidates []candidate
	for _, tab := range []string{"好友寄养_切换同服", "好友寄养_切换跨区"} {
		found, err := s.visitTab(tab, func(row friendRow) (bool, error) {
			c, err := s.readCandidate(tab, row)
			if c.YieldValue > 0 {
				candidates = append(candidates, c)
			}
			return p.LegacySelectOnly && shouldDirectSelect(c.RewardInfo, preferred, threshold), err
		})
		if found || err != nil {
			return err
		}
	}
	excluded := make(map[host]bool)
	for _, best := range rankedCandidates(candidates, preferred, nil) {
		if excluded[best.host] {
			continue
		}
		found, err := s.visitTab(best.tab, func(row friendRow) (bool, error) {
			if row.name != best.name {
				return false, nil
			}
			current, err := s.readCandidate(best.tab, row)
			return current == best, err
		})
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		if p.LegacySelectOnly {
			return nil
		}
		outcome, ok := s.task("好友寄养_入客验证")
		if !ok {
			return fmt.Errorf("无法核验好友客位")
		}
		switch outcome {
		case "好友寄养_提交完成":
			return ctx.SetAnchor(fosterResult, "好友寄养_返回后核验")
		case "好友寄养_客位已占", "好友寄养_确认客位冲突":
			excluded[best.host] = true
			if _, ok := s.task("好友寄养_重新打开列表"); !ok {
				return fmt.Errorf("客位已占，无法重新打开好友列表")
			}
		default:
			return fmt.Errorf("未识别的寄养提交结果 %q", outcome)
		}
	}
	if p.LegacySelectOnly {
		return fmt.Errorf("没有可寄养的结界卡")
	}
	return ctx.SetAnchor(fosterResult, "好友寄养_无候选")
}
