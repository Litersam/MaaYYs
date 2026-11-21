package kun28

import (
	"image"
	"regexp"
	"strconv"
	"time"

	"maa-yys-agent/internal/runstate"
	"maa-yys-agent/ocrtext"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type ReserveGuard struct{}

const reserveCounter = "困28-候补计数"

var reserveFraction = regexp.MustCompile(`^\s*(0|[1-9]|[1-4][0-9]|50)\s*[/／]\s*50\s*$`)

// reserveCount returns -1 when OCR is missing or ambiguous.
func reserveCount(texts []string) int {
	if len(texts) == 1 {
		if m := reserveFraction.FindStringSubmatch(texts[0]); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return -1
}

func inspectReserve(read func() int) (count int, replenish bool) {
	first := read()
	if first > 0 {
		return first, false
	}
	second := read()
	return second, first == 0 && second == 0
}

func (*ReserveGuard) Run(ctx *maa.Context, _ *maa.CustomActionArg) bool {
	controller := ctx.GetTasker().GetController()
	first := true
	count, replenish := inspectReserve(func() int {
		if !first {
			// Let the next rendered frame arrive before issuing a second capture.
			time.Sleep(250 * time.Millisecond)
		}
		first = false
		// Each sample is a separate completed capture; never re-read a failed capture's cache.
		if !controller.PostScreencap().Wait().Status().Success() {
			return -1
		}
		img, err := controller.CacheImage()
		if err != nil || !reserveHit(ctx, img, "困28-候补设置标题") {
			return -1
		}
		detail, err := ctx.RunRecognition(reserveCounter, img)
		if err != nil || detail == nil || !detail.Hit {
			return -1
		}
		return reserveCount(ocrtext.Texts(detail))
	})
	if count >= 0 {
		runstate.SetMetric(ctx, "kun28_reserve_before", count)
	}
	if replenish {
		result, err := ctx.RunTask("困28-补充候补狗粮")
		// An empty/unreadable inventory or a press that did not add a card is
		// not a prerequisite for exploration. The parent still has to confirm
		// the settings panel before closing it and the exploration screen after.
		if err != nil || result == nil || !result.Status.Success() {
			runstate.SetMetric(ctx, "kun28_reserve_replenished", false)
		} else {
			runstate.SetMetric(ctx, "kun28_reserve_replenished", true)
		}
	}
	return true
}

func reserveHit(ctx *maa.Context, img image.Image, node string) bool {
	detail, err := ctx.RunRecognition(node, img)
	return err == nil && detail != nil && detail.Hit
}
