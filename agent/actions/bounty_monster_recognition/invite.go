package bounty_monster_recognition

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/MaaXYZ/maa-framework-go/v4"
)

type InviteWhitelist struct{}

var _ maa.CustomRecognitionRunner = (*InviteWhitelist)(nil)

func parseWhitelist(raw string) []string {
	var p struct {
		Whitelist json.RawMessage `json:"whitelist"`
	}
	if json.Unmarshal([]byte(raw), &p) != nil || len(p.Whitelist) == 0 {
		return nil
	}
	var names []string
	if json.Unmarshal(p.Whitelist, &names) != nil {
		var text string
		if json.Unmarshal(p.Whitelist, &text) != nil {
			return nil
		}
		names = strings.FieldsFunc(text, func(r rune) bool {
			return r == ',' || r == '，' || r == '\r' || r == '\n'
		})
	}
	for i, name := range names {
		names[i] = strings.TrimSpace(name)
	}
	return slices.DeleteFunc(names, func(s string) bool { return s == "" })
}

func allowedSender(names, observed []string) bool {
	return len(observed) == 1 && observed[0] != "" && slices.Contains(names, observed[0])
}

func (*InviteWhitelist) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	if ctx == nil || arg == nil || arg.Img == nil {
		return nil, false
	}
	names := parseWhitelist(arg.CustomRecognitionParam)
	if len(names) == 0 {
		return nil, false
	}
	sender, err := ctx.RunRecognition("悬赏-协作邀请发送方", arg.Img)
	if err != nil || sender == nil || !sender.Hit || sender.Results == nil {
		return nil, false
	}
	var observed []string
	for _, candidate := range sender.Results.Filtered {
		text, ok := candidate.AsOCR()
		if !ok {
			return nil, false
		}
		observed = append(observed, text.Text)
	}
	if !allowedSender(names, observed) {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: sender.Box}, true
}
