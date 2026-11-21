package auto_foster

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type host struct {
	tab, name string
}

type candidate struct {
	host
	stars int
	RewardInfo
}

type RewardInfo struct {
	YieldType  string
	YieldValue int
}

const DefaultStaminaDirectThreshold = 130

var rewardTextPattern = regexp.MustCompile(`^([^\d+]+?)\s*\+?\s*(\d+)$`)

func parseRewardText(text string) (string, int) {
	parts := rewardTextPattern.FindStringSubmatch(strings.ReplaceAll(strings.TrimSpace(text), "＋", "+"))
	if len(parts) != 3 {
		return "", 0
	}
	value, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", 0
	}
	return strings.TrimSpace(parts[1]), value
}

func getDirectSelectThreshold(preferred string, override int) int {
	switch preferred {
	case "勾玉":
		return max(0, override)
	case "体力":
		return DefaultStaminaDirectThreshold
	default:
		return 0
	}
}

func shouldDirectSelect(reward RewardInfo, preferred string, threshold int) bool {
	return threshold > 0 && reward.YieldType == preferred && reward.YieldValue >= threshold
}

func rankedCandidates(candidates []candidate, preferred string, excluded map[host]bool) []candidate {
	var available []candidate
	for _, c := range candidates {
		if !excluded[c.host] && c.stars >= 4 && c.stars <= 6 && c.YieldValue > 0 &&
			(c.YieldType == "勾玉" || c.YieldType == "体力") {
			available = append(available, c)
		}
	}
	sort.SliceStable(available, func(i, j int) bool {
		a, b := available[i], available[j]
		if (a.YieldType == preferred) != (b.YieldType == preferred) {
			return a.YieldType == preferred
		}
		return a.YieldValue > b.YieldValue
	})
	return available
}
