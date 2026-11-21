package bond

import (
	"encoding/json"
	"image"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const (
	stateKey     = "bond.private.state"
	partyEntry   = "组队管控-开局"
	partyInit    = "组队管控-初始化"
	partyReceipt = "同心之兰-组队后"
)

type Control struct{}
type Probe struct{}

type progress struct{ Current, Target int }

type state struct {
	Role, Peer string
	Limit      int
	UsedTarget int
	Battles    int
	Pending    progress
}

func load(ctx *maa.Context) (state, bool) {
	var s state
	raw, err := ctx.GetAnchor(stateKey)
	ok := err == nil && json.Unmarshal([]byte(raw), &s) == nil && s.Role != ""
	return s, ok
}

func save(ctx *maa.Context, s state) bool {
	raw, err := json.Marshal(s)
	return err == nil && ctx.SetAnchor(stateKey, string(raw)) == nil
}

func partner(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") && json.Valid([]byte(value)) {
		return "", false
	}
	return value, value != "" && !strings.ContainsAny(value, ",，") && strings.IndexFunc(value, unicode.IsControl) < 0
}

func (s state) budget(p progress) int {
	if p.Target <= s.UsedTarget || p.Target != 10 && p.Target != 20 || p.Current < p.Target-10 || p.Current >= p.Target {
		return 0
	}
	return min(s.Limit, p.Target-p.Current)
}

func (*Control) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	var p struct {
		Op            string `json:"op"`
		Role          string `json:"role"`
		TeammateNames string `json:"teammate_names"`
		CaptainNames  string `json:"captain_names"`
		Limit         *int   `json:"limit_count"`
	}
	if arg == nil || json.Unmarshal([]byte(arg.CustomActionParam), &p) != nil {
		return false
	}
	if p.Op == "" {
		s := state{Role: p.Role, Limit: 10}
		if p.Limit != nil {
			s.Limit = *p.Limit
		}
		var names string
		switch s.Role {
		case "captain":
			names = p.TeammateNames
		case "member":
			names = p.CaptainNames
		default:
			return false
		}
		if s.Limit < 0 {
			return false
		}
		if s.Limit > 0 {
			var valid bool
			s.Peer, valid = partner(names)
			if !valid {
				return false
			}
		}
		return save(ctx, s)
	}

	s, ok := load(ctx)
	if !ok {
		return false
	}
	switch p.Op {
	case "prepare":
		d := arg.RecognitionDetail
		if d == nil || d.Results == nil || d.Results.Best == nil {
			return false
		}
		r, ok := d.Results.Best.AsCustom()
		var observation progress
		if !ok || r == nil || json.Unmarshal([]byte(r.Detail), &observation) != nil || s.budget(observation) == 0 {
			return false
		}
		s.Pending = observation
		return save(ctx, s)
	case "party":
		count := s.budget(s.Pending)
		if count == 0 {
			return false
		}
		s.UsedTarget = s.Pending.Target
		if !save(ctx, s) {
			return false
		}
		params := map[string]any{"role": s.Role, "limit_count": count, "flow": "bond"}
		if s.Role == "captain" {
			params["teammate_names"] = s.Peer
		} else {
			params["captain_names"] = s.Peer
		}
		d, err := ctx.RunTask(partyEntry, map[string]any{partyInit: map[string]any{"custom_action_param": params}})
		if err != nil || d == nil || !d.Status.Success() || ctx.GetTasker().Stopping() {
			return false
		}
		for _, n := range d.NodeDetails {
			if n != nil && n.Name == partyReceipt && n.RunCompleted {
				s.Battles += count
				if !save(ctx, s) {
					return false
				}
				runstate.SetMetric(ctx, "bond_battles", s.Battles)
				return true
			}
		}
	}
	return false
}

// On the gift icon the decorative rim can be recognized as a leading dash.
var growth = regexp.MustCompile(`^(?:成长值?[:：]?)?[-—]?([0-9]{1,2})[/／](10|20)$`)

func compact(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
}

func readProgress(texts []string) (progress, bool) {
	var p progress
	for _, text := range texts {
		m := growth.FindStringSubmatch(compact(text))
		if m == nil {
			continue
		}
		current, _ := strconv.Atoi(m[1])
		target, _ := strconv.Atoi(m[2])
		candidate := progress{current, target}
		if current < target-10 || current > target || p.Target != 0 && p != candidate {
			return progress{}, false
		}
		p = candidate
	}
	return p, p.Target != 0
}

func recognize(ctx *maa.Context, img image.Image, node string, roi *maa.Rect) *maa.RecognitionDetail {
	var overrides map[string]any
	if roi != nil {
		overrides = map[string]any{node: map[string]any{"roi": *roi}}
	}
	d, err := ctx.RunRecognition(node, img, overrides)
	if err != nil || d == nil || !d.Hit || d.Results == nil {
		return nil
	}
	return d
}

// v5.14.2 ignores a child's pipeline inverse inside And/Or. Negate the
// positive OCR result on the same frame here, without treating errors as misses.
func misses(ctx *maa.Context, img image.Image, node string) bool {
	d, err := ctx.RunRecognition(node, img)
	return err == nil && d != nil && !d.Hit
}

func exactName(d *maa.RecognitionDetail, name string) (maa.Rect, bool) {
	var box maa.Rect
	n := 0
	if d != nil {
		for _, result := range d.Results.Filtered {
			if r, ok := result.AsOCR(); ok && r != nil && strings.TrimSpace(r.Text) == name {
				box = r.Box
				n++
			}
		}
	}
	return box, n == 1
}

func (*Probe) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	var p struct {
		Kind   string `json:"kind"`
		Target int    `json:"target"`
	}
	if arg == nil || arg.Img == nil || json.Unmarshal([]byte(arg.CustomRecognitionParam), &p) != nil {
		return nil, false
	}
	hit := func(box maa.Rect) (*maa.CustomRecognitionResult, bool) {
		return &maa.CustomRecognitionResult{Box: box}, true
	}
	img := arg.Img
	switch p.Kind {
	case "courtyard":
		if misses(ctx, img, "同心之兰-好友页") && misses(ctx, img, "同心之兰-面板标题") {
			if d, err := ctx.RunRecognition("寮活动-庭院", img); err == nil && d != nil && d.Hit {
				return hit(d.Box)
			}
		}
		return nil, false
	case "non_panel":
		if misses(ctx, img, "同心之兰-面板标题") {
			return hit(maa.Rect{0, 0, 1, 1})
		}
		return nil, false
	}
	s, ok := load(ctx)
	if !ok {
		return nil, false
	}
	switch p.Kind {
	case "zero", "captain", "member":
		if p.Kind == "zero" && s.Limit == 0 || p.Kind == s.Role {
			return hit(maa.Rect{0, 0, 1, 1})
		}
	case "row", "header":
		node := "同心之兰-好友名录OCR"
		if p.Kind == "header" {
			node = "同心之兰-好友标题昵称OCR"
		}
		if box, unique := exactName(recognize(ctx, img, node, nil), s.Peer); unique {
			return hit(box)
		}
	case "expand", "accept":
		node := "同心之兰-绑定邀请文字"
		if p.Kind == "accept" {
			node = "同心之兰-接受图标"
		}
		if box, unique := invitation(ctx, img, node, s.Peer, p.Kind == "accept"); unique {
			return hit(box)
		}
	case "active", "ready":
		// Only this verified friend's gift header / panel can supply growth.
		if _, unique := exactName(recognize(ctx, img, "同心之兰-好友标题昵称OCR", nil), s.Peer); !unique {
			break
		}
		node := "同心之兰-礼袋成长OCR"
		if recognize(ctx, img, "同心之兰-面板标题", nil) != nil {
			node = "同心之兰-面板成长OCR"
		}
		d := recognize(ctx, img, node, nil)
		if d == nil {
			break
		}
		var texts []string
		for _, candidate := range d.Results.Filtered {
			if r, ok := candidate.AsOCR(); ok && r != nil {
				texts = append(texts, r.Text)
			}
		}
		pg, valid := readProgress(texts)
		if valid && (p.Target == 0 || p.Target == pg.Target) &&
			(p.Kind == "ready" && pg.Current == pg.Target || p.Kind == "active" && pg.Current < pg.Target) {
			raw, _ := json.Marshal(pg)
			return &maa.CustomRecognitionResult{Box: d.Box, Detail: string(raw)}, true
		}
	}
	return nil, false
}

func invitation(ctx *maa.Context, img image.Image, node, name string, accept bool) (maa.Rect, bool) {
	d := recognize(ctx, img, node, nil)
	if d == nil {
		return maa.Rect{}, false
	}
	var box maa.Rect
	count := 0
	for _, candidate := range d.Results.Filtered {
		b := maa.Rect{}
		if accept {
			if r, ok := candidate.AsTemplateMatch(); ok && r != nil {
				b = r.Box
			}
		} else if r, ok := candidate.AsOCR(); ok && r != nil {
			b = r.Box
		}
		if b.Width() == 0 {
			continue
		}
		// Invitation ribbons can stack. Bind title and sender to this control's
		// row rather than matching unrelated text elsewhere on the screen.
		top := max(0, b.Y()-38)
		roi := maa.Rect{188, top, 310, min(720-top, b.Height()+76)}
		if _, unique := exactName(recognize(ctx, img, "同心之兰-好友名录OCR", &roi), name); !unique {
			continue
		}
		if accept && recognize(ctx, img, "同心之兰-绑定邀请文字", &roi) == nil {
			continue
		}
		box, count = b, count+1
	}
	return box, count == 1
}
