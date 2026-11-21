package auto_foster

import (
	"encoding/json"
	"fmt"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type AutoFoster struct{}

type FosterParams struct {
	FosterTarget          int  `json:"FosterTarget"`
	DirectSelectThreshold int  `json:"DirectSelectThreshold"`
	LegacySelectOnly      bool `json:"legacy_select_only"`
}

func (*AutoFoster) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	p := FosterParams{FosterTarget: 1, DirectSelectThreshold: 76}
	if arg.CustomActionParam != "" {
		if err := json.Unmarshal([]byte(arg.CustomActionParam), &p); err != nil {
			fmt.Printf("寄养参数无效: %v\n", err)
			return false
		}
	}
	if err := runRankedFoster(ctx, p); err != nil {
		fmt.Printf("寄养扫描失败: %v\n", err)
		return false
	}
	return true
}
