//go:build !windows

package main

import (
	"testing"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func newOfflineController(t *testing.T) *maa.Controller {
	t.Helper()
	t.Skip("synthetic native controller fixture requires Windows")
	return nil
}
