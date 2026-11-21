//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestNativeBatch3OCRExpressions(t *testing.T) {
	withNativeBattleFixture(t, func(root string, res *maa.Resource, tasker *maa.Tasker) {
		t.Logf("MaaFramework %s", maa.Version())
		var patterns []string
		seen := map[string]bool{}
		var walk func(any)
		walk = func(v any) {
			switch v := v.(type) {
			case map[string]any:
				for key, val := range v {
					if key == "expected" {
						add := func(s string) {
							if !seen[s] {
								seen[s], patterns = true, append(patterns, s)
							}
						}
						switch p := val.(type) {
						case string:
							add(p)
						case []any:
							for _, raw := range p {
								if text, ok := raw.(string); ok {
									add(text)
								}
							}
						}
					}
					walk(val)
				}
			case []any:
				for _, val := range v {
					walk(val)
				}
			}
		}
		for _, f := range []string{
			"resource_pack/base/pipeline/战斗/组队管控.json", "resource_pack/base/pipeline/战斗/组队刷本.json",
			"resource_pack/base/pipeline/战斗/觉醒.json", "resource_pack/base/pipeline/战斗/同心之兰.json",
			"resource_pack/base/pipeline/战斗/契灵结契.json", "tasks/standalone/组队刷本.json", "tasks/standalone/觉醒.json",
			"tasks/standalone/同心之兰.json", "tasks/standalone/契灵结契.json",
		} {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
			if err != nil {
				t.Fatal(err)
			}
			var def any
			if err := json.Unmarshal(data, &def); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			walk(def)
		}
		slices.Sort(patterns)
		if len(patterns) == 0 {
			t.Fatal("no expressions found to exercise")
		}
		const node = "fixture-batch3-regex"
		img := syntheticGhostFrame(t)
		var misses []string
		if err := res.RegisterCustomAction(node, offlineAction(func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
			for _, expr := range patterns {
				// Pass each pattern as a single expression so a syntax error
				// invalidates its known-matching alternative too.
				joined := "(?:" + expr + ")|^涂壁$"
				d, err := ctx.RunRecognitionDirect(maa.RecognitionTypeOCR, &maa.OCRParam{Expected: []string{joined}}, img)
				if err != nil || d == nil || !d.Hit {
					misses = append(misses, fmt.Sprintf("%q (%v)", expr, err))
				}
			}
			return len(misses) == 0
		})); err != nil {
			t.Fatal(err)
		}
		job := tasker.PostTask(node, map[string]any{node: map[string]any{"action": "Custom", "custom_action": node}}).Wait()
		if !job.Success() {
			t.Fatalf("native OCR pattern misses: %v", misses)
		}
		t.Logf("%d pipeline/option expressions ran through native OCR", len(patterns))
	})
}
