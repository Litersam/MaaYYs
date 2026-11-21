package vision

import (
	"image"
	"time"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func Screencap(tasker *maa.Tasker, settle time.Duration) image.Image {
	if tasker == nil || tasker.Stopping() {
		return nil
	}
	if settle > 0 {
		time.Sleep(settle)
		if tasker.Stopping() {
			return nil
		}
	}
	ctrl := tasker.GetController()
	if ctrl == nil || !ctrl.PostScreencap().Wait().Success() {
		return nil
	}
	img, err := ctrl.CacheImage()
	if err != nil {
		return nil
	}
	return img
}
