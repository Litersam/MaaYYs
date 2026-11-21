//go:build windows

package main

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"maa-yys-agent/internal/runstate"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestNativeAwakeningZeroBudget(t *testing.T) {
	withNativeBattleFixture(t, func(root string, res *maa.Resource, tasker *maa.Tasker) {
		_, tasks, err := loadTaskDefinitions(root)
		if err != nil {
			t.Fatal(err)
		}
		task, ok := tasks["觉醒"]
		if !ok {
			t.Fatal("awakening task is missing")
		}
		const unexpected = "AwakeningUnexpectedPrepare"
		probe := &unexpectedSoulNavigation{}
		if err := res.RegisterCustomAction(unexpected, probe); err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"solo", "captain", "member"} {
			t.Run(role, func(t *testing.T) {
				overrides := resolveTaskOverride(task, map[string]any{
					"party_role": role, "limit_count": 0, "switch_soul": true, "drop_awake": true,
				})
				prepare, ok := overrides["组队管控-准备"].(map[string]any)
				if !ok {
					t.Fatal("opt-in preset did not override preparation")
				}
				prepare["action"], prepare["custom_action"] = "Custom", unexpected
				collector, release := runstate.Bind(tasker)
				defer release()
				job := tasker.PostTask(task.Entry, overrides).Wait()
				if outcome := collector.Outcome(); !job.Success() || outcome.Status != runstate.Skipped || probe.entered {
					t.Fatalf("want immediate skipped, got job=%v outcome=%+v entered_prepare=%t", job.Status(), outcome, probe.entered)
				}
			})
		}
	})
}

func TestNativeAwakeningHistoricalCrops(t *testing.T) {
	ref := os.Getenv("MAAYYS_AWAKE_REFERENCE")
	if ref == "" {
		t.Skip("set MAAYYS_AWAKE_REFERENCE to the local yys-automation checkout at 00252fc4")
	}
	withNativeBattleFixture(t, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		const probe = "AwakeningCropProbe"
		if err := res.RegisterCustomAction(probe, offlineAction(func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
			cards := []struct {
				name, file string
				pos        image.Point
			}{
				{"觉醒-火麒麟", "o_fire_kirin.png", image.Pt(104, 120)},
				{"觉醒-风麒麟", "o_wind_kirin.png", image.Pt(423, 116)},
				{"觉醒-水麒麟", "o_water_kirin.png", image.Pt(728, 115)},
				{"觉醒-雷麒麟", "o_lightning_kirin.png", image.Pt(1016, 99)},
			}
			base := filepath.Join(ref, "tasks", "EvoZone", "o")
			for _, card := range cards {
				crop, err := loadPNG(filepath.Join(base, card.file))
				if err != nil {
					t.Error(err)
					return false
				}
				frame := drawReviewCrop(crop, card.pos)
				for _, recognizer := range cards {
					// These local crops contain the card but not the tower title.
					d, err := ctx.RunRecognition(recognizer.name, frame, map[string]any{
						"导航-觉醒之塔-识别是否位于觉醒之塔": map[string]any{"recognition": "DirectHit"},
					})
					if hit := d != nil && d.Hit; err != nil || hit != (recognizer.name == card.name) {
						t.Errorf("card %s on %s: hit=%t detail=%+v err=%v", recognizer.name, card.name, hit, d, err)
					}
				}
			}
			for _, button := range []struct {
				name, file string
				pos        image.Point
			}{
				{"觉醒-组队文字", "o_form_team.png", image.Pt(959, 580)},
				{"觉醒-挑战文字", "o_evozone_fire.png", image.Pt(1127, 568)},
			} {
				crop, err := loadPNG(filepath.Join(base, button.file))
				if err != nil {
					t.Error(err)
					return false
				}
				d, err := ctx.RunRecognition(button.name, drawReviewCrop(crop, button.pos))
				if d == nil || !d.Hit || err != nil {
					t.Errorf("button %s not recognized: detail=%+v err=%v", button.name, d, err)
				}
			}
			return !t.Failed()
		})); err != nil {
			t.Fatal(err)
		}
		job := tasker.PostTask(probe, map[string]any{
			probe: map[string]any{"action": "Custom", "custom_action": probe},
		}).Wait()
		if !job.Success() {
			t.Fatal("native awakening crop replay failed")
		}
	})
}
