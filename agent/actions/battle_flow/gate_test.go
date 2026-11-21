package battle_flow

import (
	"testing"

	"maa-yys-agent/internal/runstate"
)

func TestSoulCountGate(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		clear  bool
		status runstate.Status
	}{
		{`{"count":10}`, false, ""},
		{`{"count":0}`, true, runstate.Skipped},
		{`{"count":0}`, false, runstate.Failed},
		{`{"count":-1}`, false, runstate.Failed},
		{`{}`, false, runstate.Failed},
		{`{"count":null}`, false, runstate.Failed},
	} {
		cleared := false
		var status runstate.Status
		got := gateSoulCount(tc.raw, func() bool { cleared = tc.clear; return cleared },
			func(s runstate.Status, _ string) bool { status = s; return s != runstate.Failed })
		if got != (tc.status != runstate.Failed) || status != tc.status || cleared != tc.clear {
			t.Errorf("%s: got success=%t status=%s cleared=%t", tc.raw, got, status, cleared)
		}
	}
}
