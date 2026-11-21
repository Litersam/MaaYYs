package dokan

import (
	"encoding/json"
	"fmt"
	"time"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const (
	defaultWaitSeconds = 300
	maxRefreshAttempts = 19
	refreshInterval    = 15 * time.Second
	combatLimit        = 30 * time.Minute
	budgetAnchor       = "dokan.private.budget"
)

// Store monotonic offsets, not wall-clock timestamps, in the task-local context.
var epoch = time.Now()

type BudgetAction struct {
	now   func() time.Duration
	sleep func(time.Duration)
}

type BudgetGate struct {
	now func() time.Duration
}

type actionParams struct {
	Op              string `json:"op"`
	WaitSeconds     *int   `json:"wait_seconds"`
	RefreshAttempts *int   `json:"refresh_attempts"`
	ClickNode       string `json:"click_node"`
}

type budget struct {
	Window      time.Duration
	MaxAttempts int
	CombatUntil time.Duration
	Waiting     bool
	OpeningEnd  time.Duration
	NextRefresh time.Duration
	Attempts    int
}

func newBudget(p actionParams, now time.Duration) (budget, error) {
	seconds, attempts := defaultWaitSeconds, maxRefreshAttempts
	if p.WaitSeconds != nil {
		seconds = *p.WaitSeconds
	}
	if p.RefreshAttempts != nil {
		attempts = *p.RefreshAttempts
	}
	if seconds < 0 || seconds > defaultWaitSeconds || attempts < 0 || attempts > maxRefreshAttempts {
		return budget{}, fmt.Errorf("道馆等待秒数须为 0–300，刷新次数须为 0–19")
	}
	return budget{Window: time.Duration(seconds) * time.Second, MaxAttempts: attempts, CombatUntil: now + combatLimit}, nil
}

func (b *budget) waitUntil(now time.Duration) time.Duration {
	if !b.Waiting {
		b.Waiting = true
		b.OpeningEnd = now + b.Window
		b.NextRefresh = now + refreshInterval
	}
	if b.Attempts >= b.MaxAttempts {
		return b.OpeningEnd
	}
	return min(b.OpeningEnd, b.NextRefresh)
}

func (b *budget) refresh(now time.Duration) bool {
	if !b.permits("opening", now) || now < b.NextRefresh || b.Attempts >= b.MaxAttempts {
		return false
	}
	b.Attempts++ // Charge before leaving, even if a page transition later fails.
	b.NextRefresh = now + refreshInterval
	return true
}

func (b budget) permits(phase string, now time.Duration) bool {
	switch phase {
	case "entry":
		return !b.Waiting || now < b.OpeningEnd
	case "opening":
		return b.Waiting && now < b.OpeningEnd
	case "expired":
		return b.Waiting && now >= b.OpeningEnd
	case "combat":
		return now < b.CombatUntil
	default:
		return false
	}
}

func (a *BudgetAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	var p actionParams
	if arg == nil || json.Unmarshal([]byte(arg.CustomActionParam), &p) != nil {
		return false
	}
	if (p.Op == "enter") != (p.ClickNode != "") {
		return false
	}
	if p.Op == "init" {
		b, err := newBudget(p, monotonicNow(a.now))
		if err != nil {
			fmt.Println(err)
			return false
		}
		return saveBudget(ctx, b)
	}
	if p.WaitSeconds != nil || p.RefreshAttempts != nil {
		return false
	}
	b, ok := loadBudget(ctx)
	if !ok {
		return false
	}
	switch p.Op {
	case "enter":
		// Recognition can finish before the deadline but dispatch after it.
		if arg.RecognitionDetail == nil || !b.permits("entry", monotonicNow(a.now)) {
			return false
		}
		detail, err := ctx.RunAction(p.ClickNode, arg.Box, arg.RecognitionDetail.DetailJson)
		return err == nil && detail != nil && detail.Success
	case "wait":
		until := b.waitUntil(monotonicNow(a.now))
		if !saveBudget(ctx, b) {
			return false
		}
		tasker := ctx.GetTasker()
		sleep := a.sleep
		if sleep == nil {
			sleep = time.Sleep
		}
		for !tasker.Stopping() {
			remaining := until - monotonicNow(a.now)
			if remaining <= 0 {
				return true
			}
			sleep(min(remaining, 100*time.Millisecond))
		}
		return false
	case "refresh":
		return b.refresh(monotonicNow(a.now)) && saveBudget(ctx, b)
	default:
		return false
	}
}

func (g *BudgetGate) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	var p struct {
		Phase string `json:"phase"`
	}
	if arg == nil || json.Unmarshal([]byte(arg.CustomRecognitionParam), &p) != nil {
		return nil, false
	}
	b, ok := loadBudget(ctx)
	if !ok || !b.permits(p.Phase, monotonicNow(g.now)) {
		return nil, false
	}
	return &maa.CustomRecognitionResult{Box: arg.Roi}, true
}

func monotonicNow(clock func() time.Duration) time.Duration {
	if clock != nil {
		return clock()
	}
	return time.Since(epoch)
}

func loadBudget(ctx *maa.Context) (budget, bool) {
	var b budget
	value, err := ctx.GetAnchor(budgetAnchor)
	if err != nil || json.Unmarshal([]byte(value), &b) != nil || b.CombatUntil <= 0 {
		return budget{}, false
	}
	return b, true
}

func saveBudget(ctx *maa.Context, b budget) bool {
	raw, err := json.Marshal(b)
	return err == nil && ctx.SetAnchor(budgetAnchor, string(raw)) == nil
}
