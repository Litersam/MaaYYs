package bondling

const (
	stoneCounter = "召唤窗石库存"
	quantity     = "召唤数量"
	confirm      = "单石确认窗"
)

func (f *flow) summon() (bool, string) {
	if f.stones >= f.max {
		return false, ""
	}
	read := func(page string) ([]int, bool) {
		return f.ui.counters(page, counter{stoneCounter, 30}, counter{quantity, 0})
	}
	pre, ok := read(quantityModal)
	if !ok {
		return false, "summon_stock_unconfirmed"
	}
	before, n := pre[0], pre[1]
	if f.stones > 0 && before != f.lastStock {
		return false, "summon_stock_changed"
	}
	if before == 0 {
		return false, ""
	}
	if n > before {
		return false, "summon_quantity_unconfirmed"
	}
	// Never use the maximum button. Normalize quantities one change at a time
	// and require the subsequent screen to show precisely the requested change.
	for n != 1 {
		change, expected := "数量减一", n-1
		if n == 0 {
			change, expected = "数量加一", 1
		}
		if f.ui.step(change).last != change {
			return false, "summon_quantity_unconfirmed"
		}
		got, ok := read(quantityModal)
		if !ok || got[0] != before || got[1] != expected {
			return false, "summon_quantity_unconfirmed"
		}
		n = expected
	}
	if f.ui.step("唤起单次确认").last != confirm {
		return false, "summon_confirmation_unconfirmed"
	}
	approved, ok := read(confirm)
	if !ok || approved[0] != before || approved[1] != 1 {
		return false, "summon_confirmation_unconfirmed"
	}
	submitted := f.ui.step("确认只用一石").last == "确认只用一石"
	// A failed action can still have reached the UI. Always reconcile, never
	// press confirm again or start another summon on an unverified receipt.
	after, ok := f.ui.stock(before)
	if !ok {
		return false, "summon_debit_unconfirmed"
	}
	if delta := before - after; delta > 0 {
		f.stones += delta
		f.lastStock = after
		if f.stones > f.max {
			return false, "summon_budget_exceeded"
		}
		if submitted && delta == 1 {
			return true, ""
		}
	}
	return false, "summon_debit_mismatch"
}
