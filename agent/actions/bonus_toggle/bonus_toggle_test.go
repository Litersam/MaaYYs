package bonus_toggle

import (
	"image"
	"testing"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestZeroCountOnlyAllowsDeactivation(t *testing.T) {
	got, err := parse(`{"exp_50%":true,"gold_100%":false,"remaining":0}`)
	if err != nil || len(got.toggles) != 1 || got.toggles["gold_100%"] {
		t.Fatalf("bad zero-count toggles: %+v, %v", got, err)
	}
	for _, bad := range []string{`null`, `{"remaining":-1}`, `{"exp_50%":null}`, `{"typo":true}`} {
		if _, err := parse(bad); err == nil {
			t.Errorf("accepted invalid bonus params %s", bad)
		}
	}
}

func TestKnobAndCleanupScope(t *testing.T) {
	bounds := image.Rect(0, 0, 1280, 720)
	got := knob(maa.Rect{774, 299, 13, 18}, bounds)
	if got == nil || *got != (maa.Rect{864, 308, 1, 1}) || knob(maa.Rect{1250, 299, 13, 18}, bounds) != nil {
		t.Fatalf("incorrect timer-bar to knob offset: %v", got)
	}

	s := &Scope{owned: make(map[string]bool)}
	s.set("gold_50%", true)
	s.set("exp_50%", true)
	s.set("exp_50%", false)
	if err := s.Cleanup(nil, func(_ *maa.Tasker, entry string, override map[string]any) bool {
		params := override["通用加成-关闭本轮加成"].(map[string]any)["custom_action_param"].(map[string]bool)
		if entry != "通用加成-归位" || len(params) != 1 || params["gold_50%"] {
			t.Errorf("cleanup would change unrelated bonuses: %v", params)
		}
		return true
	}); err == nil {
		t.Fatal("cleanup cannot succeed without an off-state confirmation")
	}
}

type fixtureAnchors map[string]string

func (a fixtureAnchors) GetAnchor(name string) (string, error) { return a[name], nil }
func (a fixtureAnchors) SetAnchor(name, value string) error {
	a[name] = value
	return nil
}

func TestAutomaticCleanupUsesOnlyThisTasksOwnedSwitches(t *testing.T) {
	gui := ownership{anchors: fixtureAnchors{}}
	const own, other = "exp_50%", "gold_50%"
	if gui.contains(own) {
		t.Fatal("cleanup without a record could disable a player's pre-existing bonus")
	}
	if err := gui.set(own, true); err != nil {
		t.Fatal(err)
	}
	if !gui.contains(own) || gui.contains(other) || (ownership{anchors: fixtureAnchors{}}).contains(own) {
		t.Fatal("ownership escaped the switch or task that established it")
	}
	if err := gui.set(own, false); err != nil || gui.contains(own) {
		t.Fatalf("confirmed off switch stayed owned: %v", err)
	}
}
