package dokan

import (
	"testing"
	"time"
)

func TestOpeningDeadlineIncludesNavigation(t *testing.T) {
	b, err := newBudget(actionParams{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Initial navigation precedes the first positive closed-state observation.
	const firstClosed = 20 * time.Second
	if got := b.waitUntil(firstClosed); got != firstClosed+15*time.Second {
		t.Fatalf("first refresh scheduled at %v", got)
	}
	for i := 1; i <= 19; i++ {
		due := firstClosed + time.Duration(i)*15*time.Second
		if b.refresh(due-time.Millisecond) || !b.refresh(due) {
			t.Fatalf("refresh %d did not respect its 15s boundary", i)
		}
	}
	if capWait := b.waitUntil(firstClosed + 292*time.Second); capWait != firstClosed+300*time.Second {
		t.Fatalf("navigation moved deadline to %v", capWait)
	}
	if b.refresh(firstClosed+300*time.Second) || b.permits("entry", firstClosed+300*time.Second) {
		t.Fatal("refresh or re-entry permitted at the deadline")
	}
}

func TestOpeningBudgetCapDoesNotAllowMakeupAttempts(t *testing.T) {
	attempts, seconds := 1, 60
	b, err := newBudget(actionParams{WaitSeconds: &seconds, RefreshAttempts: &attempts}, 0)
	if err != nil {
		t.Fatal(err)
	}
	b.waitUntil(0)
	if !b.refresh(29 * time.Second) {
		t.Fatal("could not charge delayed attempt before leaving")
	}
	if b.refresh(44*time.Second) || b.waitUntil(48*time.Second) != 60*time.Second {
		t.Fatal("failed navigation was refunded or the cap extended the deadline")
	}
}

func TestOpeningZeroAndInvalidOptions(t *testing.T) {
	zero := 0
	b, err := newBudget(actionParams{WaitSeconds: &zero, RefreshAttempts: &zero}, 0)
	if err != nil {
		t.Fatal(err)
	}
	const now = time.Second
	if b.waitUntil(now) != now || !b.permits("expired", now) || b.refresh(now) {
		t.Fatal("zero window should expire immediately, without refreshing")
	}
	tooLong, negative, tooMany := 301, -1, 20
	for _, p := range []actionParams{
		{WaitSeconds: &tooLong}, {WaitSeconds: &negative}, {RefreshAttempts: &tooMany}, {RefreshAttempts: &negative},
	} {
		if _, err := newBudget(p, 0); err == nil {
			t.Fatalf("invalid options accepted: %+v", p)
		}
	}
}
