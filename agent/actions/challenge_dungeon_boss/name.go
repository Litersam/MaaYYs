package challenge_dungeon_boss

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"maa-yys-agent/ocrtext"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type BossName struct{}

func (*BossName) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	if ctx == nil || arg == nil || arg.Img == nil {
		return nil, false
	}
	// Maa 5.14's native OCR regex does not interpret Unicode character classes.
	detail, err := ctx.RunRecognition("地鬼-鬼王名原始OCR", arg.Img)
	if err != nil || detail == nil || !detail.Hit {
		return nil, false
	}
	name := bossName(ocrtext.Texts(detail))
	if name == "" {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: detail.Box, Detail: name}, true
}

func bossName(texts []string) string {
	var name string
	for _, text := range texts {
		text = strings.TrimSpace(text)
		if n := utf8.RuneCountInString(text); n < 2 || n > 15 {
			continue
		}
		runes := []rune(text)
		valid := true
		for i, r := range runes {
			if !unicode.Is(unicode.Han, r) && !(r == '·' && i > 0 && i+1 < len(runes) && runes[i-1] != '·') {
				valid = false
				break
			}
		}
		if valid {
			if name != "" && name != text {
				return ""
			}
			name = text
		}
	}
	return name
}

func recognizedBossName(detail *maa.RecognitionDetail) string {
	if detail == nil || !detail.Hit || detail.Results == nil || detail.Results.Best == nil {
		return ""
	}
	if best, ok := detail.Results.Best.AsCustom(); ok && best != nil {
		return bossName([]string{best.Detail})
	}
	return ""
}
