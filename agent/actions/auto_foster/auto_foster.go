package auto_foster

import (
	"encoding/json"
	"fmt"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

type AutoFoster struct{}

type FosterParams struct {
	FosterTarget          int    `json:"FosterTarget"`
	DirectSelectThreshold int    `json:"DirectSelectThreshold"`
	LegacySelectOnly      bool   `json:"legacy_select_only"`
	Mode                  string `json:"mode"`
}

const (
	selectOnly   = "select_only"
	rankedSubmit = "ranked_submit"
)

func parseParams(raw string) (FosterParams, error) {
	p := FosterParams{FosterTarget: 1, Mode: selectOnly}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return p, err
		}
	}
	if p.Mode != selectOnly && p.Mode != rankedSubmit {
		return p, fmt.Errorf("未知模式 %q", p.Mode)
	}
	if p.LegacySelectOnly {
		p.Mode = selectOnly
	}
	return p, nil
}

func (*AutoFoster) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	p, err := parseParams(arg.CustomActionParam)
	if err != nil {
		fmt.Printf("寄养参数无效: %v\n", err)
		return false
	}
	if err := runFoster(ctx, p); err != nil {
		fmt.Printf("寄养扫描失败: %v\n", err)
		return false
	}
	return true
}
