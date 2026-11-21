package switch_soul

import (
	"errors"
	"reflect"
	"testing"

	"github.com/MaaXYZ/maa-framework-go/v4"
)

func TestEquipRequiresSuccessfulSubtask(t *testing.T) {
	runErr := errors.New("subtask timed out")
	for _, tc := range []struct {
		name   string
		detail *maa.TaskDetail
		err    error
	}{
		{"success", &maa.TaskDetail{Status: maa.StatusSuccess}, nil},
		{"explicit failure", &maa.TaskDetail{Status: maa.StatusFailure}, nil},
		{"invalid status", &maa.TaskDetail{Status: maa.StatusInvalid}, nil},
		{"missing detail", nil, nil},
		{"framework error", nil, runErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := equip(func(entry string, override ...any) (*maa.TaskDetail, error) {
				want := []any{map[string]any{
					"装备御魂-识别目标分组": map[string]any{"expected": "^御魂$"},
					"装备御魂-识别队伍名称": map[string]any{"expected": `^A\+B$`},
				}}
				if entry != "装备御魂" || !reflect.DeepEqual(override, want) {
					t.Fatalf("unexpected subtask: %q %v", entry, override)
				}
				return tc.detail, tc.err
			}, " 御魂 ", "A+B")
			wantErr := tc.name != "success"
			if (err != nil) != wantErr || tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("equip error = %v, want error: %t", err, wantErr)
			}
		})
	}
}

func TestEquipRejectsEmptyPresetBeforeNavigation(t *testing.T) {
	for _, names := range [][2]string{{"", "魂十"}, {"御魂", " "}} {
		err := equip(func(string, ...any) (*maa.TaskDetail, error) {
			t.Fatal("unsafe navigation for an empty preset name")
			return nil, nil
		}, names[0], names[1])
		if err == nil {
			t.Fatalf("equip accepted empty preset: %q", names)
		}
	}
}
