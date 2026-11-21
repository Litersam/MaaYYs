package party

import (
	"encoding/json"
	"image"
	"strings"

	"maa-yys-agent/internal/vision"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const (
	ordinaryInviteButton = "组队管控-邀请普通接受"
	defaultInviteButton  = "组队管控-邀请默认接受"
	rejectInviteButton   = "组队管控-邀请拒绝"
	inviteRowPitch       = 110
	inviteButtonSlop     = 18
	inviteButtonBody     = 58
)

type inviteAcceptHits struct{ Buttons []maa.Rect }

type invitationCard struct {
	header                          image.Rectangle
	ordinary, defaultAccept, reject *maa.Rect
	controls                        []image.Rectangle
	ambiguous                       bool
}

func (u inviteUI) invitationCard(img image.Image) *invitationCard {
	if img == nil {
		return nil
	}
	buttons := [3][]maa.Rect{
		u.invitationButtons(img, ordinaryInviteButton),
		u.invitationButtons(img, defaultInviteButton),
		u.invitationButtons(img, rejectInviteButton),
	}
	anchors := buttons[0]
	if len(anchors) == 0 {
		anchors = buttons[1]
	}
	if len(anchors) == 0 {
		return nil
	}

	// The 1280x720 layout has aligned button tops and a 110 px row pitch.
	// The sender is above the controls, never in the previous row's button
	// band. A button box has padding, so use its top edge to identify its row.
	y := anchors[0].Y()
	card := &invitationCard{header: image.Rect(10, y-72, 312, y)}
	buttonBand := image.Rect(5, y-inviteButtonSlop, 281, y+92)
	targets := [3]**maa.Rect{&card.ordinary, &card.defaultAccept, &card.reject}
	for i, column := range buttons {
		var row []maa.Rect
		for _, button := range column {
			b := vision.ImageRect(button)
			dy := b.Min.Y - y
			card.controls = append(card.controls, b)
			switch {
			case dy <= inviteButtonSlop && b.In(buttonBand):
				row = append(row, button)
			case dy > -(inviteRowPitch-inviteButtonSlop) && dy < inviteRowPitch-inviteButtonSlop:
				card.ambiguous = true // Neither the same row nor a separate card.
			case dy < 0:
				card.header.Min.Y = max(card.header.Min.Y, b.Min.Y+inviteButtonBody)
			}
		}
		if len(row) == 1 {
			*targets[i] = &row[0]
		} else if len(row) > 1 {
			card.ambiguous = true
		}
	}
	return card
}

func (u inviteUI) invitationButtons(img image.Image, node string) []maa.Rect {
	detail, err := u.ctx.RunRecognition(node, img)
	if err != nil || detail == nil || !detail.Hit || detail.Results == nil {
		return nil
	}
	if node != rejectInviteButton {
		if result := detail.Results.Best; result != nil {
			if best, ok := result.AsCustom(); ok && best != nil {
				var hits inviteAcceptHits
				if json.Unmarshal([]byte(best.Detail), &hits) == nil {
					return hits.Buttons
				}
			}
		}
		return nil
	}
	var boxes []maa.Rect
	for _, result := range detail.Results.Filtered {
		if result == nil {
			continue
		}
		if match, ok := result.AsTemplateMatch(); ok && match != nil {
			boxes = append(boxes, match.Box)
		}
	}
	return boxes
}

func (u inviteUI) invitationTexts(img image.Image, card *invitationCard) []maa.OCRResult {
	if card.ambiguous || card.header.Empty() {
		return nil
	}
	// Read past the boundary so a clipped line cannot become an exact match.
	roi := card.header.Inset(-22).Intersect(img.Bounds())
	detail, err := u.ctx.RunRecognitionDirect(maa.RecognitionTypeOCR, &maa.OCRParam{
		ROI: maa.NewTargetRect(maa.Rect{roi.Min.X, roi.Min.Y, roi.Dx(), roi.Dy()}),
	}, img)
	if err != nil || detail == nil || detail.Results == nil {
		return nil
	}
	var texts []maa.OCRResult
	for _, result := range detail.Results.All {
		if result == nil {
			return nil
		}
		item, ok := result.AsOCR()
		if !ok || item == nil {
			return nil
		}
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		b := vision.ImageRect(item.Box)
		if b.Empty() {
			return nil
		}
		if !b.Overlaps(card.header) {
			continue
		}
		center := b.Min.Add(b.Size().Div(2))
		if inviteControlContains(card.controls, center) {
			if center.In(card.header) {
				return nil // This OCR line could be button artwork or sender text.
			}
			continue // Reject/accept artwork can be read as X or punctuation.
		}
		if !b.In(card.header) {
			return nil // This text is not uniquely inside the sender window.
		}
		texts = append(texts, *item)
	}
	return texts
}

func inviteControlContains(controls []image.Rectangle, pt image.Point) bool {
	for _, b := range controls {
		if pt.In(b) {
			return true
		}
	}
	return false
}

func recognizeInviteAccept(img image.Image, roi maa.Rect, defaultAccept bool) (*maa.CustomRecognitionResult, bool) {
	buttons := findInviteAccepts(img, roi, defaultAccept)
	if len(buttons) == 0 {
		return nil, false
	}
	data, err := json.Marshal(inviteAcceptHits{Buttons: buttons})
	if err != nil {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: buttons[0], Detail: string(data)}, true
}

// The ordinary accept button is a full-size green check on a brown disk;
// the default-accept gear only has a small check in its lower-right corner.
func findInviteAccepts(img image.Image, roi maa.Rect, defaultAccept bool) []maa.Rect {
	bounds := vision.ImageRect(roi).Intersect(img.Bounds())
	if bounds.Empty() {
		return nil
	}
	var buttons []maa.Rect
	green := image.NewGray(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if g >= 95<<8 && int(g)-int(r) >= 34<<8 && int(g)-int(b) >= 25<<8 {
				green.Pix[green.PixOffset(x, y)] = 1
			}
		}
	}
	var queue []image.Point
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			seed := image.Pt(x, y)
			if green.Pix[green.PixOffset(x, y)] != 1 {
				continue
			}
			green.Pix[green.PixOffset(x, y)] = 2
			queue = append(queue[:0], seed)
			mark := image.Rectangle{Min: seed, Max: seed.Add(image.Pt(1, 1))}
			for i := 0; i < len(queue); i++ {
				p := queue[i]
				mark = mark.Union(image.Rectangle{Min: p, Max: p.Add(image.Pt(1, 1))})
				for _, d := range [...]image.Point{{X: 1}, {X: -1}, {Y: 1}, {Y: -1}} {
					n := p.Add(d)
					if n.In(bounds) && green.Pix[green.PixOffset(n.X, n.Y)] == 1 {
						green.Pix[green.PixOffset(n.X, n.Y)] = 2
						queue = append(queue, n)
					}
				}
			}
			if defaultAccept {
				if mark.Dx() < 27 || mark.Dx() > 38 || mark.Dy() < 18 || mark.Dy() > 31 {
					continue
				}
			} else if mark.Dx() < 43 || mark.Dx() > 58 || mark.Dy() < 31 || mark.Dy() > 44 {
				continue
			}
			density := 100 * len(queue) / (mark.Dx() * mark.Dy())
			if density < 33 || density > 57 || !inviteCheckOnDisk(img, green, mark, defaultAccept) {
				continue
			}
			button := image.Rect(mark.Min.X-3, mark.Min.Y-11, mark.Max.X+9, mark.Max.Y+23)
			if defaultAccept {
				button = image.Rect(mark.Min.X-23, mark.Min.Y-32, mark.Max.X+5, mark.Max.Y+11)
			}
			if button.In(bounds) {
				buttons = append(buttons, maa.Rect{button.Min.X, button.Min.Y, button.Dx(), button.Dy()})
			}
		}
	}
	return buttons
}

func inviteCheckOnDisk(img image.Image, mask *image.Gray, mark image.Rectangle, defaultAccept bool) bool {
	at := func(x, y int) image.Point {
		return image.Pt(mark.Min.X+(mark.Dx()-1)*x/100, mark.Min.Y+(mark.Dy()-1)*y/100)
	}
	for _, sample := range [...]image.Point{{X: 15, Y: 50}, {X: 42, Y: 72}, {X: 80, Y: 30}, {X: 45, Y: 89}, {X: 74, Y: 55}} {
		p := at(sample.X, sample.Y)
		if mask.Pix[mask.PixOffset(p.X, p.Y)] == 0 {
			return false
		}
	}
	is := func(x, y int, match func(int, int, int) bool) bool {
		p := at(x, y)
		if !p.In(img.Bounds()) {
			return false
		}
		r, g, b, _ := img.At(p.X, p.Y).RGBA()
		return match(int(r>>8), int(g>>8), int(b>>8))
	}
	brown := func(r, g, b int) bool { return r >= 95 && r <= 170 && r-g >= 12 && g-b >= 12 }
	if !is(2, 95, brown) || !is(95, 70, brown) {
		return false
	}
	if defaultAccept {
		gold := func(r, g, b int) bool { return r >= 185 && g >= 145 && r-g >= 20 && g-b >= 28 }
		for _, sample := range [...]image.Point{{X: -45, Y: -45}, {X: -10, Y: -30}, {X: 75, Y: -33}, {X: 37, Y: 12}} {
			if !is(sample.X, sample.Y, gold) {
				return false
			}
		}
		return true
	}
	return is(37, 12, brown) && is(50, -17, brown) && is(75, 116, brown)
}
