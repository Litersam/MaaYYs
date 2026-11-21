package breakthrough

import (
	"testing"

	"maa-yys-agent/internal/runstate"
)

func TestRealmClearsAllNineBeforeRefresh(t *testing.T) {
	p, err := parsePolicy(`{"mode":"realm","limit_count":12,"keep_tickets":3,"strategy":"full_clear"}`)
	if err != nil {
		t.Fatal(err)
	}
	var won [realmSlots]bool
	for i := range won {
		move := p.realmMove(uint64(i), 20-i, won, 0)
		if move != (decision{kind: attack, slot: i}) {
			t.Fatalf("after %d wins: got %+v, want attack slot %d", i, move, i)
		}
		won[i] = true
	}
	if got := p.realmMove(9, 11, won, 0).kind; got != refresh {
		t.Fatalf("after clearing the nine-win board: got %v", got)
	}
}

func TestRealmRetreatsOnlyBeforeFinalWin(t *testing.T) {
	p, err := parsePolicy(`{"mode":"realm","strategy":"retreat_four"}`)
	if err != nil {
		t.Fatal(err)
	}
	// A resumed board may have slot nine cleared; protection follows the last
	// unbroken target, not a hard-coded card or player level.
	won := [realmSlots]bool{true, true, true, false, true, false, true, true, true}
	if got := p.realmMove(0, 10, won, 0); got != (decision{kind: attack, slot: 3}) {
		t.Fatalf("with two targets left: got %+v", got)
	}
	won[3] = true
	for exits := uint64(0); exits < 4; exits++ {
		if got := p.realmMove(1, 9, won, exits); got != (decision{kind: retreat, slot: 5}) {
			t.Fatalf("after %d retreats: got %+v", exits, got)
		}
	}
	if got := p.realmMove(1, 9, won, 4); got != (decision{kind: attack, slot: 5}) {
		t.Fatalf("after four retreats: got %+v", got)
	}
}

func TestBreakthroughBudgets(t *testing.T) {
	p, err := parsePolicy(`{"mode":"realm","limit_count":2,"keep_tickets":5}`)
	if err != nil {
		t.Fatal(err)
	}
	won := [realmSlots]bool{true, true, true, true, true, true, true, true, false}
	if got := p.realmMove(1, 5, won, 0).kind; got != stopQuota {
		t.Fatalf("ticket reserve must stop even a protective retreat, got %v", got)
	}
	if got := p.realmMove(2, 6, won, 0).kind; got != stopLimit {
		t.Fatalf("attempt cap must stop even a protective retreat, got %v", got)
	}
	p, err = parsePolicy(`{"mode":"guild","limit_count":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if p.budget(0, 1) != attack || p.budget(1, 6) != stopLimit || p.budget(0, 0) != stopQuota {
		t.Fatal("guild should use its own remaining attempts and configured cap")
	}
	for _, raw := range []string{
		`{"mode":"guild","keep_tickets":1}`,
		`{"mode":"guild","strategy":"retreat_four"}`,
		`{"mode":"realm","keep_tickets":31}`,
		`{"mode":"realm","limit_count":-1}`,
		`{"mode":"realm","strategy":"unknown"}`,
	} {
		if _, err := parsePolicy(raw); err == nil {
			t.Fatalf("accepted invalid settings %s", raw)
		}
	}
}

func TestBreakthroughBudgetStopStatus(t *testing.T) {
	for _, tc := range []struct {
		name     string
		params   string
		attempts uint64
		left     int
		want     runstate.Status
	}{
		{"realm disabled", `{"mode":"realm","limit_count":0}`, 0, 10, runstate.Skipped},
		{"guild disabled even with no quota", `{"mode":"guild","limit_count":0}`, 0, 0, runstate.Skipped},
		{"no personal tickets", `{"mode":"realm"}`, 0, 0, runstate.Skipped},
		{"at personal ticket reserve", `{"mode":"realm","keep_tickets":5}`, 0, 5, runstate.Skipped},
		{"reserve reached after an attack", `{"mode":"realm","keep_tickets":5}`, 1, 5, runstate.Completed},
		{"guild quota not yet available", `{"mode":"guild"}`, 0, 0, runstate.Deferred},
		{"guild quota used", `{"mode":"guild"}`, 2, 0, runstate.Completed},
		{"configured limit reached", `{"mode":"realm","limit_count":1}`, 1, 10, runstate.Completed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := parsePolicy(tc.params)
			if err != nil {
				t.Fatal(err)
			}
			reason := p.budget(tc.attempts, tc.left)
			if reason == attack {
				t.Fatal("budget allowed an attack")
			}
			if got := p.stopStatus(reason, tc.attempts); got != tc.want {
				t.Errorf("stop status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestQuotaReadFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		text     string
		capacity int
		want     int
		valid    bool
	}{
		{" 5 ／ 30 ", 30, 5, true},
		{"0/30", 30, 0, true},
		{"6/6", 6, 6, true},
		{"7/6", 6, 0, false},
		{"5/6", 30, 0, false},
		{"5/30", 6, 0, false},
		{"5/30 or 8/30", 30, 0, false},
		{"未知", 30, 0, false},
	} {
		got, valid := readQuota(tc.text, tc.capacity)
		if valid != tc.valid || (valid && got != tc.want) {
			t.Errorf("quota %q (capacity %d) = (%d,%v), want (%d,%v)", tc.text, tc.capacity, got, valid, tc.want, tc.valid)
		}
	}
}
