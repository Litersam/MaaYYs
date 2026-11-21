package bondling

import (
	"testing"

	"maa-yys-agent/internal/runstate"
)

type replay struct {
	page                        string
	memory, discs, stones       int
	tier                        discTier
	emptyMap, emptyRefill       bool
	warn                        bool
	board                       bool
	quantity, debit             int
	submits, battles, returning int
	stockReads, shops, slots    int
	confirms, courtyard         bool
	bond, readable, debitRead   bool
}

func (r *replay) step(node string) stepResult {
	switch node {
	case "入口导航":
		if r.page == "" {
			r.page = mapPage
		}
	case "前置导航", "退出商店", "返回地图":
		r.page = mapPage
	case "查看契忆":
		r.shops++
		r.page = "在契忆商店"
	case "寻找可挑战", "复核手选目标":
		// After a summon animation the map is shown, not the challenge.
	case "盘位一":
		r.slots++
		r.page = quantityModal
		if r.board {
			r.page = ready
		}
	case "盘位二":
		r.slots++
		r.page = quantityModal
		if !r.emptyMap {
			r.page = ready
		}
	case "盘位三", "盘位四":
		r.slots++
		r.page = quantityModal
	case "数量减一":
		r.quantity--
		return stepResult{last: node}
	case "数量加一":
		r.quantity++
		return stepResult{last: node}
	case "唤起单次确认":
		if !r.confirms || r.quantity != 1 {
			return stepResult{}
		}
		r.page = confirm
	case "确认只用一石":
		if r.page != confirm {
			return stepResult{}
		}
		r.submits++
		r.stones -= r.debit
		r.board, r.page = true, mapPage
		return stepResult{last: node}
	case "试一场":
		if r.warn || r.page == lowDiscs {
			return stepResult{last: lowDiscs}
		}
		r.battles++
		r.board, r.page = false, mapPage
		return stepResult{last: "结算已返回", bond: r.bond, abandoned: !r.bond}
	case "回庭院":
		r.returning++
		if r.courtyard {
			return stepResult{last: "在庭院"}
		}
		return stepResult{}
	}
	return stepResult{last: r.page}
}

func (r *replay) counters(page string, _ ...counter) ([]int, bool) {
	if !r.readable {
		return nil, false
	}
	switch page {
	case "在契忆商店":
		if r.emptyRefill && r.shops > 1 {
			return []int{r.memory, 0, 52, 35}, true
		}
		return []int{r.memory, r.discs, 52, 35}, true
	case ready:
		if r.board {
			return []int{1, r.discs}, true
		}
		return []int{0, r.discs}, true
	default:
		r.stockReads++
		return []int{r.stones, r.quantity}, true
	}
}

func (r *replay) selectedTier() discTier { return r.tier }

func (r *replay) resumeWarning(_ discTier) stepResult {
	if r.discs == 0 {
		return stepResult{}
	}
	r.page, r.warn = ready, false
	return r.step("试一场")
}

func (r *replay) stock(int) (int, bool) {
	r.stockReads++
	return r.stones, r.debitRead
}

func TestBondlingVerifiedOutcome(t *testing.T) {
	for _, c := range []struct {
		name                            string
		edit                            func(*replay)
		budget, submits, battles, spent int
		status                          runstate.Status
		reason                          string
	}{
		{"default clears only existing board", func(r *replay) { r.board = true }, 0, 0, 1, 0, runstate.Completed, "bonds_verified"},
		{"existing preferred even when summon is allowed", func(r *replay) { r.board = true }, 1, 1, 2, 1, runstate.Completed, "bonds_verified"},
		{"no board and no budget returns home", func(*replay) {}, 0, 0, 0, 0, runstate.Skipped, "no_eligible_board"},
		{"zero stones returns home", func(r *replay) { r.stones = 0 }, 2, 0, 0, 0, runstate.Skipped, "no_eligible_board"},
		{"skip requires courtyard proof", func(r *replay) { r.courtyard = false }, 0, 0, 0, 0, runstate.Failed, "courtyard_unconfirmed"},
		{"memory limit blocks spending", func(r *replay) { r.memory = 2000 }, 2, 0, 0, 0, runstate.Skipped, "memory_limit"},
		{"selected tier zero blocks spending despite other balances", func(r *replay) { r.discs = 0 }, 2, 0, 0, 0, runstate.Failed, "selected_discs_insufficient"},
		{"rechecked tier zero blocks spending", func(r *replay) { r.emptyRefill = true }, 2, 0, 0, 0, runstate.Failed, "selected_discs_insufficient"},
		{"no evidence for active tier blocks spending", func(r *replay) { r.emptyMap = true }, 2, 0, 0, 0, runstate.Failed, "selected_tier_unconfirmed"},
		{"unknown inventory is not empty", func(r *replay) { r.readable = false }, 2, 0, 0, 0, runstate.Failed, "inventory_unconfirmed"},
		{"verified single debits reach run cap", func(r *replay) { r.quantity = 3 }, 2, 2, 2, 2, runstate.Completed, "bonds_verified"},
		{"missing approval never clicks confirm", func(r *replay) { r.confirms = false }, 4, 0, 0, 0, runstate.Failed, "summon_confirmation_unconfirmed"},
		{"stock unchanged never retries confirm", func(r *replay) { r.debit = 0 }, 4, 1, 0, 0, runstate.Failed, "summon_debit_mismatch"},
		{"unreadable debit never retries confirm", func(r *replay) { r.debitRead = false }, 4, 1, 0, 0, runstate.Failed, "summon_debit_unconfirmed"},
		{"wrong debit within budget still stops", func(r *replay) { r.debit = 2 }, 4, 1, 0, 2, runstate.Failed, "summon_debit_mismatch"},
		{"stock delta exceeds cap and is reported", func(r *replay) { r.debit = 2 }, 1, 1, 0, 2, runstate.Failed, "summon_budget_exceeded"},
		{"returns after abandon without fake completion", func(r *replay) { r.board, r.bond = true, false }, 0, 0, 1, 0, runstate.Failed, "no_verified_bonds"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := replay{tier: firstTier, discs: 9, stones: 15, quantity: 1, debit: 1, confirms: true, courtyard: true, bond: true, readable: true, debitRead: true}
			c.edit(&r)
			f := flow{ui: &r, max: c.budget, attempts: 3}
			got := f.run()
			bonds := 0
			if r.bond {
				bonds = c.battles
			}
			if got != (outcome{c.status, prefixReason(c.reason), c.spent, bonds}) || r.submits != c.submits || r.battles != c.battles {
				t.Fatalf("outcome %+v, submits %d, battles %d", got, r.submits, r.battles)
			}
			if r.submits != 0 && r.stockReads < 3*r.submits {
				t.Fatalf("each confirm needs pre-selection, pre-confirm and post-confirm stock reads: %d", r.stockReads)
			}
			if c.status == runstate.Skipped && r.returning != 1 {
				t.Fatalf("skipped without returning to courtyard")
			}
		})
	}
}

func TestBondlingPreservesManualTarget(t *testing.T) {
	for _, entry := range []string{ready, lowDiscs} {
		r := replay{page: entry, tier: firstTier, discs: 9, board: true, bond: true, readable: true}
		f := flow{ui: &r, max: 5, attempts: 3}
		if got := f.run(); got.status != runstate.Completed || got.bonds != 1 || r.shops != 0 || r.slots != 0 || r.submits != 0 {
			t.Fatalf("entry %s: %+v, replay %+v", entry, got, r)
		}
	}
}

func prefixReason(s string) string { return "bondling_" + s }

func TestBondlingCounterRejectsAmbiguousText(t *testing.T) {
	for _, c := range []struct {
		text  []string
		cap   int
		n     int
		valid bool
	}{
		{[]string{"１５ ／ ３０"}, 30, 15, true},
		{[]string{"剩余4/99"}, 99, 4, true},
		{[]string{"0/200"}, 200, 0, true},
		{[]string{"31/30"}, 30, 0, false},
		{[]string{"15"}, 30, 0, false},
		{[]string{"15/30", "5/30"}, 30, 0, false},
		{[]string{"1/3"}, 0, 0, false},
		{[]string{"1石"}, 0, 0, false},
	} {
		got, ok := parseCounter(c.text, c.cap)
		if ok != c.valid || ok && got != c.n {
			t.Errorf("parseCounter(%v, %d) = (%d, %v)", c.text, c.cap, got, ok)
		}
	}
}
