package bondling

import (
	"image"
	"strconv"
	"strings"
	"time"

	"maa-yys-agent/ocrtext"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type maaUI struct{ ctx *maa.Context }

func (m maaUI) step(entry string) stepResult {
	return m.runStep(entry, map[string]any{})
}

func (m maaUI) runStep(entry string, override map[string]any) stepResult {
	var out stepResult
	if m.ctx.GetTasker().Stopping() {
		return out
	}
	// RunTask shares the parent's hit counts. Reset only the actions scoped
	// to this visit or battle, not every graph node or the continuation.
	for _, node := range visitHits(entry) {
		if err := m.ctx.ClearHitCount(prefix + node); err != nil {
			return out
		}
	}
	// A Maa entry's action need not wait on its own recognition. A neutral
	// root makes every state-changing node a recognized next edge.
	const route = prefix + "执行子图"
	next := []string{prefix + entry}
	if entry != "确认只用一石" {
		// Maa composites don't inherit a child's inverse flag. Put modal
		// evidence ahead of every possible background action instead.
		next = append([]string{prefix + "召唤确认文案"}, next...)
	}
	override[route] = map[string]any{"next": next}
	result, err := m.ctx.RunTask(route, override)
	if err != nil || result == nil {
		return out
	}
	for _, n := range result.NodeDetails {
		if n.RunCompleted {
			last := strings.TrimPrefix(n.Name, prefix)
			out.last = last
			out.bond = out.bond || last == "结契成功"
			out.abandoned = out.abandoned || last == "放弃结契"
		}
	}
	if !result.Status.Success() {
		out.last = ""
	}
	return out
}

func visitHits(entry string) []string {
	switch entry {
	case "入口导航", "前置导航":
		return []string{"关闭召唤窗", "点契灵之境", "点地图路牌", "展开探索入口", "庭院点探索"}
	case "查看契忆":
		return []string{"点契忆商店"}
	case "返回地图":
		return []string{"关闭召唤窗", "退出挑战"}
	case "寻找可挑战":
		return []string{"关闭召唤窗"}
	case "回庭院":
		return []string{"关闭召唤窗", "退出挑战", "退出地图", "退出探索"}
	case "试一场":
		return []string{"战斗中点准备", "退出战斗界面", "继续低式盘挑战"}
	default:
		return nil
	}
}

func (m maaUI) capture() image.Image {
	t := m.ctx.GetTasker()
	if t.Stopping() {
		return nil
	}
	c := t.GetController()
	if !c.PostScreencap().Wait().Success() {
		return nil
	}
	img, _ := c.CacheImage()
	return img
}

func (m maaUI) recognize(frame image.Image, node string) *maa.RecognitionDetail {
	if frame == nil {
		return nil
	}
	d, err := m.ctx.RunRecognition(prefix+node, frame)
	if err != nil || d == nil || !d.Hit {
		return nil
	}
	return d
}

type counter struct {
	node string
	cap  int // 0: an unadorned quantity, bounded by available stone capacity
}

func (m maaUI) counters(page string, fields ...counter) ([]int, bool) {
	frame := m.capture()
	if page == quantityModal && m.recognize(frame, "召唤确认文案") != nil {
		return nil, false
	}
	if page == ready {
		for _, modal := range []string{lowDiscs, quantityModal, "召唤确认文案", "在结契设置"} {
			if m.recognize(frame, modal) != nil {
				return nil, false
			}
		}
	}
	if m.recognize(frame, page) == nil {
		return nil, false
	}
	values := make([]int, 0, len(fields))
	for _, c := range fields {
		n, ok := m.read(frame, c)
		if !ok {
			return nil, false
		}
		values = append(values, n)
	}
	return values, true
}

func (m maaUI) selectedTier() discTier {
	if _, ok := m.counters(ready); !ok {
		return unknownTier
	}
	if m.step("打开结契设置").last != "在结契设置" {
		return unknownTier
	}
	frame := m.capture()
	tier := unknownTier
	if m.recognize(frame, "在结契设置") != nil && m.recognize(frame, "自动结契已开") != nil {
		for i, names := range [][2]string{{"式盘一已勾", "式盘一未勾"}, {"式盘二已勾", "式盘二未勾"}, {"式盘三已勾", "式盘三未勾"}} {
			on := m.recognize(frame, names[0]) != nil
			off := m.recognize(frame, names[1]) != nil
			if on == off || (on && tier != unknownTier) {
				tier = unknownTier
				break
			}
			if on {
				tier = discTier(i + 1)
			}
		}
	}
	// No toggles are touched. Close even when the tier couldn't be read.
	if m.step("关闭结契设置").last != ready {
		return unknownTier
	}
	return tier
}

func (m maaUI) resumeWarning(expected discTier) stepResult {
	frame := m.capture()
	if m.recognize(frame, lowDiscs) == nil || m.recognize(frame, "召唤确认文案") != nil {
		return stepResult{}
	}
	var tier discTier
	var name string
	texts := ocrtext.Texts(m.recognize(frame, "低式盘档次"))
	// Only map labels with corroborated historic evidence; don't infer an
	// unfamiliar label from the quantity or from the other balances.
	for _, candidate := range []struct {
		tier discTier
		name string
	}{{firstTier, "百物式盘"}, {thirdTier, "千咒式盘"}} {
		for _, text := range texts {
			if strings.Contains(text, candidate.name) {
				if tier != unknownTier && tier != candidate.tier {
					return stepResult{}
				}
				tier, name = candidate.tier, candidate.name
			}
		}
	}
	if tier == unknownTier || (expected != unknownTier && tier != expected) {
		return stepResult{}
	}
	n, read := m.read(frame, boardDiscs[tier-1])
	remaining, known := m.read(frame, counter{"盘上剩余", 99})
	if !read || n == 0 || !known || remaining == 0 {
		return stepResult{}
	}
	// The actual Click rechecks the same tier and a nonzero balance on
	// its own recognition frame. OCR captures the button, not the board.
	return m.runStep("继续低式盘挑战", map[string]any{
		prefix + "继续低式盘挑战": map[string]any{
			"enabled": true,
			"all_of": []string{
				prefix + lowDiscs, prefix + "继续挑战按钮", prefix + "低式盘档次",
				prefix + name + "正库存",
			},
		},
		prefix + "低式盘档次": map[string]any{"expected": []string{"自动结契所需.*" + name}},
	})
}

func (m maaUI) read(frame image.Image, c counter) (int, bool) {
	return parseCounter(ocrtext.Texts(m.recognize(frame, c.node)), c.cap)
}

func (m maaUI) stock(before int) (int, bool) {
	last := -1
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) && !m.ctx.GetTasker().Stopping() {
		frame := m.capture()
		now, ok := 0, false
		switch {
		case m.recognize(frame, "召唤确认文案") != nil:
			// Confirmation still pending; background counters aren't receipts.
		case m.recognize(frame, quantityModal) != nil:
			now, ok = m.read(frame, counter{stoneCounter, 30})
		case m.recognize(frame, mapPage) != nil:
			now, ok = m.read(frame, counter{"地图石库存", 30})
		}
		if ok && now != before {
			if now == last {
				return now, true
			}
			last = now
		} else {
			last = -1
		}
		time.Sleep(250 * time.Millisecond)
	}
	return 0, false
}

func parseCounter(texts []string, capacity int) (int, bool) {
	value := -1
	for _, text := range texts {
		s := strings.Map(func(r rune) rune {
			switch {
			case r >= '０' && r <= '９':
				return r - '０' + '0'
			case r == '／':
				return '/'
			default:
				return r
			}
		}, strings.TrimSpace(text))
		if capacity == 99 {
			s = strings.TrimSpace(strings.TrimPrefix(s, "剩余"))
		}
		limit := capacity
		if capacity > 0 {
			n, cap, found := strings.Cut(s, "/")
			if !found || strings.TrimSpace(cap) != strconv.Itoa(capacity) {
				return 0, false
			}
			s = strings.TrimSpace(n)
		} else {
			limit = 30
		}
		if s == "" || strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
			return 0, false
		}
		n, err := strconv.Atoi(s)
		if err != nil || n > limit || (value >= 0 && value != n) {
			return 0, false
		}
		value = n
	}
	return value, value >= 0
}
