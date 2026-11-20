//go:build !windows

package switch_account

import "errors"

func sendMuMuText(mumuIndex *int, adbPath, adbAddress, text string) error {
	return errors.New("Win32 WM_CHAR password input is only supported on Windows")
}
