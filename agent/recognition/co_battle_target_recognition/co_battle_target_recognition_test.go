package co_battle_target_recognition

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestSameMonsterIsNotACoBattleTargetOnTheNextTask(t *testing.T) {
	const monster = "涂壁"
	first := targets{TaskID: 42, Names: []string{monster}}
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(loadTargets(string(raw), 42).Names, monster) {
		t.Fatal("same task lost its target")
	}
	if slices.Contains(loadTargets(string(raw), 43).Names, monster) {
		t.Fatal("second task must not skip a personal bounty against the same monster")
	}
}
