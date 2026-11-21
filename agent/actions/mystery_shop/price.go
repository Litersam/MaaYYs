package mystery_shop

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"maa-yys-agent/ocrtext"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type Price struct{}

var digits = regexp.MustCompile(`^[1-9][0-9]*$`)

func (*Price) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	if ctx == nil || arg == nil || arg.Img == nil {
		return nil, false
	}
	p := struct {
		MaxPrice int `json:"max_price"`
	}{85}
	if arg.CustomRecognitionParam != "" && json.Unmarshal([]byte(arg.CustomRecognitionParam), &p) != nil {
		return nil, false
	}
	// Read the window layout, price and (only in a quantity selector) quantity
	// on one frame. The two layouts put the jade confirmation at different
	// known heights and only the quantity window has a max-quantity control.
	jade, err := ctx.RunRecognition("秘魂屋-确认按钮勾玉", arg.Img)
	if err != nil || jade == nil || !jade.Hit {
		return nil, false
	}
	plus, err := ctx.RunRecognition("秘魂屋-订单数量控件", arg.Img)
	if err != nil || plus == nil {
		return nil, false
	}
	window := purchaseWindow(jade.Box, plus.Hit)
	if window == unknownWindow {
		return nil, false
	}
	var quantities []string
	if window == quantityWindow {
		qty, err := ctx.RunRecognition("秘魂屋-购买数量", arg.Img)
		if err != nil || qty == nil || !qty.Hit {
			return nil, false
		}
		quantities = ocrtext.Texts(qty)
	}
	x, y, w, h := jade.Box[0], jade.Box[1], jade.Box[2], jade.Box[3]
	bounds := arg.Img.Bounds()
	if w <= 0 || h <= 0 || x < bounds.Min.X || y < bounds.Min.Y || x+w+60 > bounds.Max.X || y+h > bounds.Max.Y {
		return nil, false
	}
	price, err := ctx.RunRecognition("秘魂屋-确认按钮单价", arg.Img, map[string]any{
		"秘魂屋-确认按钮单价": map[string]any{"roi": []int{x + w, y, 60, h}},
	})
	if err != nil || price == nil || !price.Hit ||
		!allowPurchase(window, ocrtext.Texts(price), quantities, p.MaxPrice) {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: jade.Box, Detail: "{}"}, true
}

type orderWindow uint8

const (
	unknownWindow orderWindow = iota
	singleWindow
	quantityWindow
)

func purchaseWindow(jade maa.Rect, plus bool) orderWindow {
	centerX, centerY := jade[0]+jade[2]/2, jade[1]+jade[3]/2
	if jade[2] <= 0 || jade[3] <= 0 || centerX < 600 || centerX > 650 {
		return unknownWindow
	}
	switch {
	case !plus && centerY >= 506 && centerY <= 542:
		return singleWindow
	case plus && centerY >= 549 && centerY <= 588:
		return quantityWindow
	default:
		return unknownWindow
	}
}

func allowPurchase(window orderWindow, prices, quantities []string, maxPrice int) bool {
	price, priceOK := exactNumber(prices)
	// Prices below 40 are implausible for blue tickets (often truncated OCR).
	if !priceOK || price < 40 || price > maxPrice {
		return false
	}
	switch window {
	case singleWindow:
		return true
	case quantityWindow:
		quantity, qtyOK := exactNumber(quantities)
		return qtyOK && quantity == 1
	default:
		return false
	}
}

func exactNumber(texts []string) (int, bool) {
	if len(texts) != 1 {
		return 0, false
	}
	text := strings.TrimSpace(texts[0])
	if !digits.MatchString(text) {
		return 0, false
	}
	n, err := strconv.Atoi(text)
	return n, err == nil
}
