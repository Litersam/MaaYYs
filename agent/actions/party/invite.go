package party

import (
	"encoding/json"
	"image"
	"slices"
	"strings"
	"time"

	"maa-yys-agent/internal/vision"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type CreateAndInvite struct{}
type WaitAndAccept struct{}

type inviteUI struct{ ctx *maa.Context }

func (u inviteUI) frame() image.Image {
	tasker := u.ctx.GetTasker()
	c := tasker.GetController()
	if tasker.Stopping() || c == nil || !c.PostScreencap().Wait().Success() {
		return nil
	}
	img, _ := c.CacheImage()
	return img
}

func (u inviteUI) click(box maa.Rect) bool {
	if u.ctx.GetTasker().Stopping() {
		return false
	}
	detail, err := u.ctx.RunAction("组队管控-点识别框", box, "")
	return err == nil && detail != nil && detail.Success
}

func (u inviteUI) hit(node string, img image.Image) *maa.Rect {
	if img != nil {
		detail, err := u.ctx.RunRecognition(node, img)
		if err == nil && detail != nil && detail.Hit {
			return &detail.Box
		}
	}
	return nil
}

func (u inviteUI) until(timeout time.Duration, check func(image.Image) bool) bool {
	for end := time.Now().Add(timeout); time.Now().Before(end); {
		img := u.frame()
		if img == nil {
			return false
		}
		if check(img) {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}

type tab struct {
	label string
	roi   maa.Rect
}

func (u inviteUI) tabs(img image.Image) []tab {
	rois := []maa.Rect{{335, 74, 110, 84}, {446, 74, 116, 84}, {566, 74, 115, 84}, {685, 74, 94, 84}}
	var tabs []tab
	for _, roi := range rois {
		for _, item := range vision.OCRItems(u.ctx, img, roi) {
			switch strings.TrimSpace(item.Text) {
			case "好友", "跨区", "跨区好友", "最近", "最近组队":
				tabs = append(tabs, tab{item.Text, item.Box})
			}
		}
	}
	return tabs
}

func tabSelected(img image.Image, roi maa.Rect) bool {
	return colorFraction(img, maa.Rect{roi.X() - 2, roi.Y() - 3, roi.Width() + 4, roi.Height() + 5}, func(r, g, b int) bool {
		return r >= 210 && g >= 170 && g <= 217 && b >= 110 && b <= 175 && r > g+12 && g > b+17
	}) > .36
}

func (u inviteUI) selectTab(t tab) bool {
	if !u.click(t.roi) {
		return false
	}
	return u.until(2500*time.Millisecond, func(img image.Image) bool {
		for _, active := range u.tabs(img) {
			if active.label == t.label && tabSelected(img, active.roi) {
				return true
			}
		}
		return false
	})
}

func exactName(items []maa.OCRResult, name string) *maa.Rect {
	var found *maa.Rect
	for _, row := range items {
		if strings.TrimSpace(row.Text) == name {
			if found != nil {
				return nil
			}
			box := row.Box
			found = &box
		}
	}
	return found
}

func (u inviteUI) pick(img image.Image, name string) (bool, bool) {
	var items []maa.OCRResult
	for _, roi := range []maa.Rect{{434, 185, 189, 345}, {729, 184, 196, 346}} {
		items = append(items, vision.OCRItems(u.ctx, img, roi)...)
	}
	box := exactName(items, name)
	if box == nil {
		return false, true
	}
	if !u.click(*box) {
		return false, false
	}
	// The card's check is near the right edge, farther down than the name.
	x := 595
	if box.X() >= 700 {
		x = 890
	}
	check := maa.Rect{x, box.Y() - 27, 42, 106}
	picked := u.until(1200*time.Millisecond, func(after image.Image) bool {
		return colorFraction(after, check, func(r, g, b int) bool {
			return r < 80 && g >= 105 && b >= 130 && b > g+11 && g > r+40
		}) > .015
	})
	return picked, picked
}

func (*CreateAndInvite) Run(ctx *maa.Context, _ *maa.CustomActionArg) bool {
	s, ok := load(ctx)
	if !ok || s.Role != "captain" || len(s.Names) == 0 {
		return false
	}
	u := inviteUI{ctx}
	img := u.frame()
	if img == nil {
		return false
	}
	tabs := u.tabs(img)
	for _, family := range []string{"好友", "跨区", "最近"} {
		for _, t := range tabs {
			if !strings.HasPrefix(t.label, family) || !u.selectTab(t) {
				continue
			}
			for _, name := range s.Names {
				if slices.Contains(s.Selected, name) {
					continue
				}
				img = u.frame()
				if img == nil {
					return false
				}
				found, safe := u.pick(img, name)
				if !safe {
					return false
				}
				if found {
					s.Selected = append(s.Selected, name)
				}
			}
			if len(s.Selected) == len(s.Names) {
				return save(ctx, s)
			}
		}
	}
	s.End = "party_teammate_not_found"
	return save(ctx, s)
}

func invitationSender(items []maa.OCRResult, allow []string) string {
	var sender string
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		for _, suffix := range []string{"邀请你加入队伍", "邀请您加入队伍", "邀请你组队", "邀请您组队", "邀请你", "邀请您"} {
			if name, ok := strings.CutSuffix(text, suffix); ok {
				text = strings.TrimSpace(strings.Trim(name, "「」“”"))
				break
			}
		}
		if slices.Contains(allow, text) {
			if sender != "" && sender != text {
				return ""
			}
			sender = text
		}
	}
	return sender
}

func (*WaitAndAccept) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	s, ok := load(ctx)
	if !ok || s.Role != "member" || s.Fights >= s.LimitCount || arg == nil || arg.Box.Width() == 0 {
		return false
	}
	u := inviteUI{ctx}
	img := u.frame()
	box := u.hit("组队管控-收到邀请", img)
	if box == nil {
		return true // The card may have been withdrawn between callbacks.
	}
	items := vision.OCRItems(ctx, img, maa.Rect{10, max(0, box.Y()-105), 302, 105})
	sender := invitationSender(items, s.Names)
	button := "组队管控-邀请拒绝"
	if sender != "" {
		button = "组队管控-邀请普通接受"
		if s.AutoAccept {
			button = "组队管控-邀请默认接受"
		}
	}
	target := u.hit(button, img)
	if target == nil || target.Y() < box.Y()-18 || target.Y() > box.Y()+18 {
		return true // Never click an unverified column or an adjacent card.
	}
	if !u.click(*target) {
		return false
	}
	if sender == "" {
		return true
	}
	s.AcceptedPeer = sender
	s.AcceptedUntil = time.Now().Add(12 * time.Second)
	s.MemberJoined = false
	return save(ctx, s)
}

type GlyphRecognition struct{}

func (*GlyphRecognition) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	var p struct {
		Kind string   `json:"kind"`
		ROI  maa.Rect `json:"box"`
	}
	if arg == nil || arg.Img == nil || json.Unmarshal([]byte(arg.CustomRecognitionParam), &p) != nil {
		return nil, false
	}
	box := p.ROI
	var match bool
	switch p.Kind {
	case "private_checked":
		match = colorFraction(arg.Img, box, func(r, g, b int) bool { return b > 145 && b > r+60 && b > g+60 }) > .075
	case "private_unchecked":
		match = colorFraction(arg.Img, box, func(r, g, b int) bool { return r < 105 && g < 80 && b < 44 && r > b+15 }) > .42
	case "default_off":
		match = colorFraction(arg.Img, box, func(r, g, b int) bool { return r < 106 && g < 80 && b < 72 && r > g+7 }) > .26
	case "default_on":
		match = colorFraction(arg.Img, box, func(r, g, b int) bool { return r >= 195 && g >= 176 && b >= 144 && r > g }) > .38
	case "challenge_enabled":
		match = colorFraction(arg.Img, box, func(r, g, b int) bool {
			return r > 155 && r > g+15 && g > b+14 && r > b+45
		}) > .025
	case "invite_accept", "invite_accept_default":
		box, match = findInviteAccept(arg.Img, box, p.Kind == "invite_accept_default")
	case "empty":
		box, match = findPlus(arg.Img, box)
	case "full":
		s, ok := load(ctx)
		if !ok || s.Role != "captain" {
			return nil, false
		}
		u := inviteUI{ctx}
		regular := u.hit("组队管控-标准房间", arg.Img) != nil
		sea := u.hit("组队管控-永生房间", arg.Img) != nil
		switch {
		case regular && !sea && s.Dungeon != "eternity_sea":
			match = seatsOccupied(arg.Img, middleRoomSlot, rightRoomSlot)
		case sea && !regular && s.Dungeon == "eternity_sea":
			match = seatsOccupied(arg.Img, seaRoomSlot)
		}
	}
	if !match {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: box}, true
}

func colorFraction(img image.Image, roi maa.Rect, pred func(r, g, b int) bool) float64 {
	if img == nil {
		return 0
	}
	b := vision.ImageRect(roi).Intersect(img.Bounds())
	if b.Empty() {
		return 0
	}
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if pred(int(r>>8), int(g>>8), int(b>>8)) {
				n++
			}
		}
	}
	return float64(n) / float64(b.Dx()*b.Dy())
}

var rightRoomSlot = maa.Rect{970, 151, 193, 220}
var middleRoomSlot = maa.Rect{569, 196, 186, 161}
var seaRoomSlot = maa.Rect{836, 231, 100, 100}

func findPlus(img image.Image, roi maa.Rect) (maa.Rect, bool) {
	r := vision.ImageRect(roi).Intersect(img.Bounds())
	bright := func(x, y int) bool {
		r, g, b, _ := img.At(x, y).RGBA()
		return r >= 210<<8 && g >= 205<<8 && b >= 201<<8 && max(r, g, b)-min(r, g, b) < 28<<8
	}
	for y := r.Min.Y + 22; y < r.Max.Y-22; y += 2 {
		for x := r.Min.X + 22; x < r.Max.X-22; x += 2 {
			if !bright(x, y) || !bright(x-17, y) || !bright(x+17, y) || !bright(x, y-17) || !bright(x, y+17) ||
				bright(x-12, y-12) || bright(x+12, y+12) {
				continue
			}
			return maa.Rect{x - 20, y - 20, 40, 40}, true
		}
	}
	return maa.Rect{}, false
}

func seatsOccupied(img image.Image, slots ...maa.Rect) bool {
	for _, slot := range slots {
		if _, empty := findPlus(img, slot); empty {
			return false
		}
		// Failure to see a plus is not proof that the avatar loaded.
		if colorFraction(img, slot, func(r, g, b int) bool { return max(r, g, b)-min(r, g, b) > 67 && max(r, g, b) > 110 }) < .018 {
			return false
		}
	}
	return len(slots) > 0
}
