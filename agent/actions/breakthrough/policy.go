package breakthrough

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	realm        = "realm"
	guild        = "guild"
	fullClear    = "full_clear"
	retreatFour  = "retreat_four"
	realmSlots   = 9
	retreatLimit = 4
)

type policy struct {
	mode        string
	limitCount  int
	keepTickets int
	strategy    string
}

func parsePolicy(raw string) (policy, error) {
	var params struct {
		Mode        string `json:"mode"`
		LimitCount  *int   `json:"limit_count"`
		KeepTickets *int   `json:"keep_tickets"`
		Strategy    string `json:"strategy"`
	}
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return policy{}, err
	}
	p := policy{mode: params.Mode}
	switch p.mode {
	case realm:
		p.limitCount = 30
		p.strategy = retreatFour
		if params.KeepTickets != nil {
			p.keepTickets = *params.KeepTickets
		}
		if params.Strategy != "" {
			p.strategy = params.Strategy
		}
		if p.keepTickets < 0 || p.keepTickets > 30 || (p.strategy != fullClear && p.strategy != retreatFour) {
			return policy{}, fmt.Errorf("invalid realm breakthrough policy")
		}
	case guild:
		p.limitCount = 50
		if params.KeepTickets != nil || params.Strategy != "" {
			return policy{}, fmt.Errorf("guild breakthrough uses its own challenge quota, not realm tickets or rank protection")
		}
	default:
		return policy{}, fmt.Errorf("invalid breakthrough mode")
	}
	if params.LimitCount != nil {
		p.limitCount = *params.LimitCount
	}
	if p.limitCount < 0 {
		return policy{}, fmt.Errorf("negative breakthrough limit")
	}
	return p, nil
}

// The personal board shows tickets out of 30; guild uses the separate 6-attempt quota.
var quotaPattern = regexp.MustCompile(`^([0-9]{1,2})\s*[/／]\s*([0-9]{1,2})$`)

func readQuota(text string, capacity int) (int, bool) {
	m := quotaPattern.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return 0, false
	}
	left, _ := strconv.Atoi(m[1])
	total, _ := strconv.Atoi(m[2])
	return left, total == capacity && left <= total
}

type moveKind int

const (
	stopLimit moveKind = iota
	stopQuota
	attack
	retreat
	refresh
)

type decision struct {
	kind moveKind
	slot int
}

func (p policy) budget(attempts uint64, left int) moveKind {
	if attempts >= uint64(p.limitCount) {
		return stopLimit
	}
	if left <= p.keepTickets {
		return stopQuota
	}
	return attack
}

func (p policy) realmMove(attempts uint64, tickets int, won [realmSlots]bool, retreats uint64) decision {
	if budget := p.budget(attempts, tickets); budget != attack {
		return decision{kind: budget}
	}
	pending, slot := 0, 0
	for i, broken := range won {
		if !broken {
			if pending == 0 {
				slot = i
			}
			pending++
		}
	}
	if pending == 0 {
		return decision{kind: refresh}
	}
	if pending == 1 && p.strategy == retreatFour && retreats < retreatLimit {
		return decision{kind: retreat, slot: slot}
	}
	return decision{kind: attack, slot: slot}
}
