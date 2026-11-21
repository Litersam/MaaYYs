//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"maa-yys-agent/internal/runstate"
	"maa-yys-agent/ocrtext"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

const contract = "契灵结契-"

func TestBondlingReviewCatalogLimits(t *testing.T) {
	list, byName, err := loadTaskDefinitions(testRepoRoot(t))
	if err != nil || len(list) != 52 {
		t.Fatalf("catalog: tasks=%d, err=%v", len(list), err)
	}
	task := byName["契灵结契"]
	const option = "契灵结契-设置挑战次数"
	for _, c := range []struct {
		name   string
		params map[string]any
		want   int
	}{
		{"default", nil, 5},
		{"public", map[string]any{"limit_count": float64(1)}, 1},
		{"legacy", map[string]any{"挑战次数": "2"}, 2},
		{"conflicting", map[string]any{"limit_count": 4, "挑战次数": 2, option: map[string]any{"挑战次数": 1}}, 1},
		{"explicit zero", map[string]any{"limit_count": 0, "挑战次数": 3}, 0},
		{"valid beats malformed", map[string]any{"limit_count": "invalid", "挑战次数": 2}, 2},
		{"invalid-only fails closed", map[string]any{"limit_count": 1.5}, -1},
		{"null alone fails closed", map[string]any{"limit_count": nil}, -1},
	} {
		t.Run(c.name, func(t *testing.T) {
			override := resolveTaskOverride(task, c.params)
			count := override[contract+"计数"].(map[string]any)["custom_recognition_param"].(map[string]any)["target_count"]
			if count != c.want || task.OptionsMap[option].Inputs[0].Name != "挑战次数" {
				t.Fatalf("target_count=%v, want %d; raw GUI input must stay unchanged", count, c.want)
			}
		})
	}
}

func TestNativeBondlingReview(t *testing.T) {
	root := testRepoRoot(t)
	lib := resolveMaaLibDir(root)
	if _, err := os.Stat(filepath.Join(lib, "MaaFramework.dll")); err != nil {
		t.Skipf("native runtime unavailable: %v", err)
	}
	logDir := filepath.Join(root, "debug", fmt.Sprintf("bondling-review-%d", os.Getpid()))
	defer os.RemoveAll(logDir)
	if err := maa.Init(maa.WithLibDir(lib), maa.WithLogDir(logDir), maa.WithStdoutLevel(maa.LoggingLevelOff)); err != nil {
		t.Fatal(err)
	}
	defer maa.Release()
	t.Logf("MaaFramework %s", maa.Version())
	if maa.Version() != "v5.14.2" {
		t.Skip("the review replay is pinned to MaaFramework v5.14.2")
	}
	res, err := loadAndRegisterResource([]string{filepath.Join(root, "resource_pack", "base")})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Destroy()
	frames := bondlingReviewFrames(t, root)
	r := func(x, y, w, h int) image.Rectangle { return image.Rect(x, y, x+w, y+h) }
	shop := func(page string) bondlingClick { return bondlingClick{"enter shop", r(222, 646, 140, 66), page} }
	back := func(page string) bondlingClick { return bondlingClick{"back arrow", r(0, 0, 110, 92), page} }
	slot := func(x, y int, page string) bondlingClick { return bondlingClick{"slot", r(x, y, 12, 15), page} }
	closeQty := bondlingClick{"close quantity", r(921, 62, 24, 24), "map15"}
	openSettings := func(page string) bondlingClick { return bondlingClick{"open settings", r(48, 427, 123, 63), page} }
	closeSettings := func(page string) bondlingClick { return bondlingClick{"close settings", r(652, 601, 153, 77), page} }
	confirm := bondlingClick{"confirm fictional one stone", r(655, 407, 192, 65), "map14"}
	trial := bondlingClick{"challenge", r(1080, 544, 200, 108), "prepare"}
	prepare := bondlingClick{"prepare", r(1080, 544, 200, 108), "result"}
	result := func(page string) bondlingClick { return bondlingClick{"bond receipt", r(257, 509, 275, 167), page} }
	continueWarning := bondlingClick{"continue warning", r(668, 410, 168, 59), "prepare"}
	run := func(t *testing.T, start string, attempts int, clicks []bondlingClick, want runstate.Status, reason string, spent, bonds int) {
		t.Helper()
		runBondlingReview(t, root, res, frames, start, attempts, clicks, want, reason, spent, bonds)
	}

	// All visits execute as production child graphs of a single BondlingRun.
	// The controller only changes pixels after the expected real Click.
	scan := []bondlingClick{
		shop("shop"), back("map15"),
		slot(235, 525, "quantity"), closeQty,
		slot(446, 546, "quantity"), closeQty,
		slot(694, 526, "depleted"), openSettings("settings"), closeSettings("depleted"), back("map15"),
		slot(950, 516, "quantity"), closeQty,
	}
	paid := append(append([]bondlingClick{}, scan...), shop("shop"), back("map15"),
		slot(235, 525, "quantity"), bondlingClick{"open one-stone confirmation", r(598, 613, 161, 67), "confirm"},
		confirm, slot(235, 525, "board"), openSettings("settings"), closeSettings("board"), trial, prepare, result("board"),
		openSettings("settings"), closeSettings("board"), trial, prepare, result("map14"))
	t.Run("two shop visits, consecutive vacant closes, and two battles", func(t *testing.T) {
		run(t, "map15", 2, paid, runstate.Completed, "bonds_verified", 1, 2)
	})
	t.Run("second shop shows zero in the selected tier", func(t *testing.T) {
		clicks := append(append([]bondlingClick{}, scan...), shop("shopZero"), back("map15"))
		run(t, "map15", 2, clicks, runstate.Failed, "selected_discs_insufficient", 0, 0)
	})
	t.Run("shop click is still capped within a visit", func(t *testing.T) {
		run(t, "map15", 1, []bondlingClick{shop("map15")}, runstate.Failed, "memory_shop_unconfirmed", 0, 0)
	})
	t.Run("no visible active tier", func(t *testing.T) {
		clicks := []bondlingClick{shop("shop"), back("map15"), slot(235, 525, "board"), openSettings("ambiguous"), closeSettings("board")}
		run(t, "map15", 2, clicks, runstate.Failed, "selected_tier_unconfirmed", 0, 0)
	})
	t.Run("pre-opened target does not leave to shop or select another slot", func(t *testing.T) {
		run(t, "board", 3,
			[]bondlingClick{openSettings("settings"), closeSettings("board"), trial, prepare, result("map14")},
			runstate.Completed, "bonds_verified", 0, 1)
	})
	t.Run("zero battle budget does not touch any page", func(t *testing.T) {
		run(t, "map15", 0, nil, runstate.Skipped, "budget_zero", 0, 0)
	})
	t.Run("warning cannot use a different tier's positive stock", func(t *testing.T) {
		run(t, "warningZero", 1, nil, runstate.Failed, "low_disc_continuation_unconfirmed", 0, 0)
	})
	t.Run("historical warning and balances authorize exactly one continue", func(t *testing.T) {
		dir := os.Getenv("MAAYYS_BONDLING_REFERENCE_FIXTURES")
		if dir == "" {
			t.Skip("set MAAYYS_BONDLING_REFERENCE_FIXTURES to the offline historical fixtures")
		}
		frames["historicalWarning"] = decodeBondlingPNG(t, filepath.Join(dir, "bondling_low_discs_notice_A_20261011.png"))
		run(t, "historicalWarning", 3, []bondlingClick{continueWarning, prepare, result("map14")},
			runstate.Completed, "bonds_verified", 0, 1)
		t.Run("no repeat if the warning remains", func(t *testing.T) {
			stuck := continueWarning
			stuck.next = "historicalWarning"
			run(t, "historicalWarning", 1, []bondlingClick{stuck}, runstate.Failed, "low_disc_continuation_unconfirmed", 0, 0)
		})
	})
	t.Run("historical tier controls", func(t *testing.T) {
		dir := os.Getenv("MAAYYS_BONDLING_TIER_FIXTURES")
		if dir == "" {
			t.Skip("set MAAYYS_BONDLING_TIER_FIXTURES to the historical BondlingFairyland capture directory")
		}
		for i, name := range []string{"low", "midum", "high"} {
			panel := image.NewRGBA(image.Rect(0, 0, 1280, 720))
			draw.Draw(panel, panel.Bounds(), frames["panelText"], image.Point{}, draw.Src)
			pasteBondlingCrop(panel, decodeBondlingPNG(t, filepath.Join(dir, "capture_c_auto_true.png")), image.Pt(765, 200))
			for j, disc := range []struct {
				name    string
				on, off image.Point
			}{
				{"low", image.Pt(523, 284), image.Pt(521, 282)},
				{"midum", image.Pt(663, 283), image.Pt(662, 283)},
				{"high", image.Pt(800, 283), image.Pt(801, 283)},
			} {
				suffix, point := "_false.png", disc.off
				if i == j {
					suffix, point = "_true.png", disc.on
				}
				pasteBondlingCrop(panel, decodeBondlingPNG(t, filepath.Join(dir, "capture_c_"+disc.name+suffix)), point)
			}
			frames[name] = panel
			board := fmt.Sprintf("singleTier%d", i+1)
			t.Run(name, func(t *testing.T) {
				run(t, board, 1,
					[]bondlingClick{openSettings(name), closeSettings(board), trial, prepare, result("map14")},
					runstate.Completed, "bonds_verified", 0, 1)
			})
		}
	})
}

type bondlingClick struct {
	name string
	box  image.Rectangle
	next string
}

func runBondlingReview(t *testing.T, root string, res *maa.Resource, frames map[string]image.Image, start string, attempts int,
	clicks []bondlingClick, want runstate.Status, reason string, spent, bonds int) {
	t.Helper()
	page, bad, i := start, "", 0
	c := &partyFrameController{frame: frames[page]}
	c.step = func(p image.Point) image.Image {
		if i >= len(clicks) || !p.In(clicks[i].box) {
			if bad == "" {
				bad = fmt.Sprintf("unexpected click %v on %s (event %d)", p, page, i)
				if i < len(clicks) {
					bad += "; expected " + clicks[i].name
				}
			}
			return nil
		}
		page = clicks[i].next
		i++
		return frames[page]
	}
	ctrl, destroy := newMaa514FrameController(t, root, c)
	defer destroy()
	if !ctrl.PostConnect().Wait().Success() {
		t.Fatal("offline memory controller failed to connect")
	}
	tasker, err := maa.NewTasker()
	if err != nil {
		t.Fatal(err)
	}
	defer tasker.Destroy()
	if err := tasker.BindResource(res); err != nil {
		t.Fatal(err)
	}
	if err := tasker.BindController(ctrl); err != nil {
		t.Fatal(err)
	}
	runBondlingReviewTask(t, res, tasker, frames, start, attempts, want, reason, spent, bonds)
	got := c.recorded()
	if bad != "" || len(got) != len(clicks) {
		t.Fatalf("page=%s, clicks=%d/%d (%v), %s", page, len(got), len(clicks), got, bad)
	}
}

func runBondlingReviewTask(t *testing.T, res *maa.Resource, tasker *maa.Tasker, frames map[string]image.Image, start string,
	attempts int, want runstate.Status, reason string, spent, bonds int) {
	t.Helper()
	override := map[string]any{
		contract + "开始": map[string]any{"custom_action_param": map[string]any{"max_summon_stones": 1}, "timeout": 500},
		contract + "计数": map[string]any{"custom_recognition_param": map[string]any{"target_count": attempts}},
	}
	// Only timing is overridden. OCR, colors, actions, routing and hit caps
	// remain production definitions, including on the second visit.
	for _, node := range []string{"执行子图", "入口导航", "查看契忆", "退出商店", "检查契灵盘", "寻找可挑战", "返回地图",
		"打开结契设置", "关闭结契设置", "复核手选目标", "试一场", "挑战可点击", "点击挑战", "结契成功",
		"继续低式盘挑战", "唤起单次确认", "确认只用一石", "战斗中点准备", "点契忆商店", "关闭召唤窗", "退出挑战"} {
		override[contract+node] = map[string]any{"timeout": 1500, "post_delay": 25}
	}

	probe := t.Name() + "/probe"
	if err := res.RegisterCustomAction(probe, offlineAction(func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
		known := frames[start]
		if start == "historicalWarning" {
			for node, text := range map[string]string{"低式盘档次": "百物式盘", "盘上剩余": "剩余4/99", "式盘一库存": "9/200"} {
				d, err := ctx.RunRecognition(contract+node, known)
				if d == nil || !d.Hit || err != nil || !strings.Contains(strings.Join(ocrtext.Texts(d), " "), text) {
					t.Errorf("%s didn't prove %s: %v, %v", node, text, ocrtext.Texts(d), err)
				}
			}
		}
		d, err := ctx.RunTask(contract + "开始")
		return err == nil && d != nil && d.Status.Success()
	})); err != nil {
		t.Fatal(err)
	}
	override[probe] = map[string]any{"recognition": "DirectHit", "action": "Custom", "custom_action": probe, "timeout": 500, "on_error": []string{}}
	col, release := runstate.Bind(tasker)
	defer release()
	job := tasker.PostTask(probe, override).Wait()
	got := col.Outcome()
	metric := func(key string) string {
		value, _ := got.Metrics[key].(json.RawMessage)
		return string(value)
	}
	if got.Status != want || !got.Reported || job.Success() != (want != runstate.Failed) ||
		metric("task_reason") != fmt.Sprintf(`"bondling_%s"`, reason) ||
		metric("bondling_stones_spent") != fmt.Sprint(spent) ||
		metric("bondling_verified_bonds") != fmt.Sprint(bonds) {
		t.Errorf("outcome=%+v, job=%v; want %v %s (%d/%d)", got, job.Status(), want, reason, spent, bonds)
	}
}

func decodeBondlingPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func pasteBondlingCrop(dst draw.Image, crop image.Image, p image.Point) {
	draw.Draw(dst, image.Rectangle{Min: p, Max: p.Add(crop.Bounds().Size())}, crop, crop.Bounds().Min, draw.Over)
}

func bondlingReviewFrames(t *testing.T, root string) map[string]image.Image {
	t.Helper()
	// Render original synthetic frames with Windows' regular CJK font; no
	// fixture files or renderer packages are added to the repo.
	const render = `
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
Add-Type -AssemblyName System.Drawing
$font = New-Object System.Drawing.Font('Microsoft YaHei', 27, [Drawing.FontStyle]::Regular, [Drawing.GraphicsUnit]::Pixel)
function out-frame($name, $labels) {
  $b = New-Object System.Drawing.Bitmap(1280,720)
  $g = [Drawing.Graphics]::FromImage($b)
  $g.Clear([Drawing.Color]::FromArgb(48,48,48))
  $g.TextRenderingHint = [Drawing.Text.TextRenderingHint]::AntiAliasGridFit
  foreach ($label in $labels) {
    $g.DrawString($label[2], $font, [Drawing.Brushes]::White, [single]$label[0], [single]$label[1])
  }
  $s = New-Object IO.MemoryStream
  $b.Save($s,[Drawing.Imaging.ImageFormat]::Png)
  Write-Output ($name + ':' + [Convert]::ToBase64String($s.ToArray()))
  $s.Dispose(); $g.Dispose(); $b.Dispose()
}
$map=@( @(148,12,'契灵之境'), @(227,654,'契忆商店') )
out-frame map15 ($map+@(,@(852,16,'15/30')))
out-frame map14 ($map+@(,@(852,16,'14/30')))
$shop=@( @(1137,12,'12/4000'), @(558,12,'52/200'), @(761,12,'35/200') )
out-frame shop ($shop+@(,@(373,12,'9/200')))
out-frame shopZero ($shop+@(,@(373,12,'0/200')))
$qty=@( @(490,59,'请选择鸣契的契灵数量'), @(1137,10,'15/30'), @(585,546,'1'), @(638,621,'1鸣契') )
out-frame quantity $qty
out-frame confirm ($qty+@( @(437,297,'是否消耗1个鸣契石召唤镇墓兽'), @(706,421,'确定') ))
$board=@( @(150,14,'契灵结契'), @(54,438,'结契设置'), @(535,12,'9/200'), @(735,12,'52/200'), @(931,12,'35/200'), @(1153,573,'挑战') )
out-frame board ($board+@(,@(1146,663,'剩余4/99')))
out-frame depleted ($board+@(,@(1146,663,'剩余0/99')))
$singleTier=@( @(150,14,'契灵结契'), @(54,438,'结契设置'), @(1153,573,'挑战'), @(1146,663,'剩余4/99') )
out-frame singleTier1 ($singleTier+@( @(535,12,'9/200'), @(735,12,'0/200'), @(931,12,'0/200') ))
out-frame singleTier2 ($singleTier+@( @(535,12,'0/200'), @(735,12,'9/200'), @(931,12,'0/200') ))
out-frame singleTier3 ($singleTier+@( @(535,12,'0/200'), @(735,12,'0/200'), @(931,12,'9/200') ))
out-frame prepare @(,@(1153,573,'准备'))
out-frame result @(,@(869,566,'升至10级解锁'))
out-frame panelText @( @(480,187,'自动结契'), @(696,616,'确定') )
out-frame warningZero @( @(150,14,'契灵结契'), @(535,12,'0/200'), @(735,12,'52/200'), @(931,12,'35/200'), @(1153,573,'挑战'), @(1146,663,'剩余4/99'), @(425,251,'自动结契所需百物式盘较少'), @(686,414,'继续挑战') )
`
	var script bytes.Buffer
	for _, v := range utf16.Encode([]rune(render)) {
		_ = binary.Write(&script, binary.LittleEndian, v)
	}
	data, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand",
		base64.StdEncoding.EncodeToString(script.Bytes())).Output()
	if err != nil {
		t.Fatalf("render synthetic Windows frames: %v", err)
	}
	frames := make(map[string]image.Image)
	for _, line := range strings.Fields(string(data)) {
		key, encoded, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("unexpected renderer output: %.60s", line)
		}
		rgb, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(rgb))
		if err != nil {
			t.Fatal(err)
		}
		frames[key] = img
	}
	arrow := decodeBondlingPNG(t, filepath.Join(root, "resource_pack", "base", "image", "通用图标", "返回_黄色.png"))
	for _, page := range []string{"map15", "map14", "shop", "shopZero", "board", "depleted"} {
		pasteBondlingCrop(frames[page].(draw.Image), arrow, image.Pt(10, 12))
	}
	for _, panel := range []string{"settings", "ambiguous"} {
		img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
		draw.Draw(img, img.Bounds(), frames["panelText"], image.Point{}, draw.Src)
		fill := func(rect image.Rectangle, c color.Color) {
			draw.Draw(img, rect, image.NewUniform(c), image.Point{}, draw.Src)
		}
		fill(image.Rect(765, 200, 789, 224), color.RGBA{219, 163, 59, 255})
		fill(image.Rect(521, 282, 555, 318), color.RGBA{67, 82, 190, 255})
		fill(image.Rect(662, 282, 696, 317), color.RGBA{85, 55, 22, 255})
		fill(image.Rect(800, 282, 835, 320), color.RGBA{85, 55, 22, 255})
		if panel == "ambiguous" {
			fill(image.Rect(662, 282, 696, 317), color.RGBA{67, 82, 190, 255})
		}
		frames[panel] = img
	}
	return frames
}
