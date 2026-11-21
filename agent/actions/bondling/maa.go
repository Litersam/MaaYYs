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
	var out stepResult
	if m.ctx.GetTasker().Stopping() {
		return out
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
	result, err := m.ctx.RunTask(route, map[string]any{
		route: map[string]any{"next": next},
	})
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
	if page == ready && m.recognize(frame, "低式盘提示") != nil {
		return nil, false
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
