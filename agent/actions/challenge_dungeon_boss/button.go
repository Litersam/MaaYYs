package challenge_dungeon_boss

import (
	"encoding/json"
	"image"
	"sort"

	"maa-yys-agent/internal/vision"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type ChallengeButton struct{}

type buttonParams struct {
	Node  string `json:"node"`
	Index int    `json:"index"`
}

func (*ChallengeButton) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	var p buttonParams
	if json.Unmarshal([]byte(arg.CustomRecognitionParam), &p) != nil || p.Node == "" || p.Index < 0 {
		return nil, false
	}
	detail, err := ctx.RunRecognition(p.Node, arg.Img)
	if err != nil || detail == nil || !detail.Hit {
		return nil, false
	}
	var boxes []maa.Rect
	if detail.Results != nil {
		for _, item := range detail.Results.Filtered {
			if item != nil {
				if match, ok := item.AsTemplateMatch(); ok && match != nil {
					boxes = append(boxes, match.Box)
				}
			}
		}
	}
	if len(boxes) == 0 {
		boxes = append(boxes, detail.Box)
	}
	box, ok := challengeButton(arg.Img, boxes, p.Index)
	if !ok {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: box, Detail: "{}"}, true
}

func challengeButton(img image.Image, boxes []maa.Rect, index int) (maa.Rect, bool) {
	if img == nil || index < 0 || index >= len(boxes) {
		return maa.Rect{}, false
	}
	sort.SliceStable(boxes, func(i, j int) bool { return boxes[i][1] < boxes[j][1] })
	box := boxes[index]
	r := vision.ImageRect(box)
	// The in-repo enabled list and detail crops average about .39 and .27 respectively.
	return box, !r.Empty() && r.In(img.Bounds()) && vision.MeanSaturation(vision.Crop(img, r)) >= 0.17
}
