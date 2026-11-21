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
	if arg.Img == nil {
		return nil, false
	}
	p := struct {
		MaxPrice int `json:"max_price"`
	}{85}
	if arg.CustomRecognitionParam != "" && json.Unmarshal([]byte(arg.CustomRecognitionParam), &p) != nil {
		return nil, false
	}
	// Read price and quantity from the SAME frame, anchored to the jade icon.
	jade, err := ctx.RunRecognition("秘魂屋-确认按钮勾玉", arg.Img)
	if err != nil || jade == nil || !jade.Hit {
		return nil, false
	}
	qty, err := ctx.RunRecognition("秘魂屋-购买数量", arg.Img)
	if err != nil || qty == nil || !qty.Hit {
		return nil, false
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
		!allowPurchase(ocrtext.Texts(price), ocrtext.Texts(qty), p.MaxPrice) {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: jade.Box, Detail: "{}"}, true
}

func allowPurchase(prices, quantities []string, maxPrice int) bool {
	price, priceOK := exactNumber(prices)
	quantity, qtyOK := exactNumber(quantities)
	// Prices below 40 are implausible for blue tickets (often truncated OCR).
	return priceOK && qtyOK && quantity == 1 && price >= 40 && price <= maxPrice
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
