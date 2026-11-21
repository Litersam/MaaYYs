//go:build windows

package main

import (
	"encoding/json"
	"image"
	"image/draw"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"maa-yys-agent/internal/vision"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestNativePartyInviteCardBinding(t *testing.T) {
	dir := partyHistoryDir(t)
	glyphs := syntheticGhostFrame(t)
	const init = "组队管控-初始化"
	const invite = "组队管控-接邀判定"
	const received = "组队管控-收到邀请"
	const state = "party.private.state"
	type authorization struct {
		AcceptedPeer   string
		AcceptedUntil  time.Time
		ContinuityPeer string
	}

	name := func(frame draw.Image, x, y int) {
		draw.Draw(frame, image.Rect(x, y, x+80, y+30), glyphs, image.Pt(145, 52), draw.Src)
	}

	// The upper card has a gear but no ordinary button, so the receipt
	// recognizer selects the lower ordinary button. This recreates the
	// review's mixed-row setup without mocking OCR or button recognition.
	stack := func(t *testing.T, top, bottom int, lowerReject, topBleed bool) *image.RGBA {
		pieces := []partyHistoryCrop{
			{"gi_i_accept_default.png", 205, top - 2},
			{"gi_i_reject.png", 12, top + 1},
			{"gi_i_accept.png", 113, bottom},
			{"gi_i_accept_default.png", 205, bottom - 2},
		}
		if lowerReject {
			pieces = append(pieces, partyHistoryCrop{"gi_i_reject.png", 12, bottom + 1})
		}
		if !topBleed {
			return partyHistoryFrame(t, dir, pieces...)
		}
		frame := blankPartyFrame()
		draw.Draw(frame, image.Rect(5, top-8, 301, top+68), image.White, image.Point{}, draw.Src)
		for _, piece := range pieces {
			crop, err := loadPNG(filepath.Join(dir, piece.file))
			if err != nil {
				t.Fatal(err)
			}
			pasteCrop(frame, crop, image.Pt(piece.x, piece.y))
		}
		return frame
	}

	input := &partyFrameController{frame: blankPartyFrame()}
	withPartyFrameReplay(t, input, func(_ string, res *maa.Resource, tasker *maa.Tasker) {
		const failClick = "Batch3FailInviteReject"
		rejectAttempted := false
		registerPartyProbe(t, res, failClick, func(ctx *maa.Context, _ *maa.CustomActionArg) bool {
			rejectAttempted = true
			raw, err := ctx.GetAnchor(state)
			var got authorization
			if err != nil || json.Unmarshal([]byte(raw), &got) != nil || got != (authorization{}) {
				t.Errorf("reject tried before authority was cleared: %s, %v", raw, err)
			}
			return false
		})
		for _, tc := range []struct {
			label        string
			upper, lower int
			firstAllowed bool
			besideGear   bool
			extraSender  bool
			lowerReject  bool
			failReject   bool
			autoAccept   bool
			topBleed     bool
			wantClick    image.Rectangle
			wantPeer     string
		}{
			{label: "allowlisted above stranger: reject lower", upper: 225, lower: 335, firstAllowed: true, lowerReject: true,
				wantClick: image.Rect(12, 336, 76, 397)},
			{label: "stranger above allowlisted: ordinary accept lower", upper: 225, lower: 335, lowerReject: true,
				wantClick: image.Rect(113, 335, 176, 407), wantPeer: "涂壁"},
			{label: "stranger above allowlisted: sender box y276", upper: 220, lower: 330, lowerReject: true, topBleed: true,
				wantClick: image.Rect(113, 330, 176, 402), wantPeer: "涂壁"},
			{label: "stranger above allowlisted: ordinary at y445 with OCR top bleed", upper: 335, lower: 445, lowerReject: true, topBleed: true,
				wantClick: image.Rect(113, 445, 176, 517), wantPeer: "涂壁"},
			{label: "stranger above allowlisted: default accept lower at edge", upper: 335, lower: 445, lowerReject: true, autoAccept: true,
				topBleed: true, wantClick: image.Rect(204, 444, 267, 513), wantPeer: "涂壁"},
			{label: "review repro: upper name beside gear cannot authorize lower", upper: 220, lower: 330, firstAllowed: true, besideGear: true, lowerReject: true,
				wantClick: image.Rect(12, 331, 76, 392)},
			{label: "conflicting names in the same header cannot authorize", upper: 225, lower: 335, extraSender: true, lowerReject: true,
				wantClick: image.Rect(12, 336, 76, 397)},
			{label: "no same-card reject: do not borrow upper or keep authority", upper: 225, lower: 335, firstAllowed: true},
			{label: "failed reject cannot retain authority", upper: 225, lower: 335, firstAllowed: true, lowerReject: true, failReject: true},
		} {
			t.Run(tc.label, func(t *testing.T) {
				frame := stack(t, tc.upper, tc.lower, tc.lowerReject, tc.topBleed)
				if tc.firstAllowed {
					if tc.besideGear {
						name(frame, 107, tc.upper+19)
					} else if tc.topBleed {
						drawPartyCaption(t, frame, "陌生人邀请你组队", 11, tc.upper-43)
						name(frame, 20, tc.lower-43)
					} else {
						name(frame, 20, tc.upper-48)
					}
					drawPartyCaption(t, frame, "好友", 18, tc.lower-46)
				} else {
					drawPartyCaption(t, frame, "好友", 18, tc.upper-46)
					name(frame, 20, tc.lower-43)
				}
				if tc.extraSender {
					drawPartyCaption(t, frame, "好友", 178, tc.lower-46)
				}

				runPartyHistoryProbe(t, res, tasker, func(ctx *maa.Context) {
					d, err := ctx.RunAction(init, maa.Rect{}, "", map[string]any{
						init: map[string]any{"custom_action_param": map[string]any{
							"role": "member", "captain_names": "涂壁", "auto_accept": tc.autoAccept,
						}},
					})
					if err != nil || d == nil || !d.Success {
						t.Errorf("initialize party: %v, %v", d, err)
						return
					}
					// Seed stale authority, then verify that even a no-click
					// rejection clears it. Success must replace it with 涂壁.
					raw, err := ctx.GetAnchor(state)
					var current map[string]any
					if err != nil || json.Unmarshal([]byte(raw), &current) != nil {
						t.Errorf("read party state: %q, %v", raw, err)
						return
					}
					current["AcceptedPeer"] = "旧邀请"
					current["AcceptedUntil"] = time.Now().Add(time.Minute)
					current["ContinuityPeer"] = "旧邀请"
					data, err := json.Marshal(current)
					if err != nil || ctx.SetAnchor(state, string(data)) != nil {
						t.Errorf("seed party state: %v", err)
						return
					}

					input.set(frame)
					seen, err := ctx.RunRecognition(received, frame)
					if err != nil || seen == nil || !seen.Hit || seen.Box.Y() != tc.lower {
						t.Errorf("expected received box for lower row at %d: %v, %v", tc.lower, seen, err)
						return
					}
					if tc.topBleed {
						// The recorded frame exposes the sender at y=276 / y=391,
						// three pixels above the unpadded card header.
						items := vision.OCRItems(ctx, frame, maa.Rect{0, tc.lower - 73, 334, 95})
						if !slices.ContainsFunc(items, func(item maa.OCRResult) bool {
							return item.Text == "涂壁" && item.Box.Y() == tc.lower-54
						}) {
							t.Errorf("fixture must expose the exact padded sender box: %v", items)
						}
					}
					if tc.extraSender || tc.besideGear {
						// Assert the native OCR actually sees the conflicting
						// lines; a fixture unreadable to OCR is not a regression.
						items := vision.OCRItems(ctx, frame, maa.Rect{10, tc.lower - 105, 302, 105})
						var sawAllowed, sawOther bool
						for _, item := range items {
							sawAllowed = sawAllowed || item.Text == "涂壁"
							sawOther = sawOther || item.Text == "好友"
						}
						if !sawAllowed || !sawOther {
							t.Errorf("fixture must expose both names to the previous OCR window: %v", items)
						}
					}
					start := len(input.recorded())
					var override map[string]any
					if tc.failReject {
						override = map[string]any{
							"组队管控-点识别框": map[string]any{"action": "Custom", "custom_action": failClick},
						}
					}
					d, err = ctx.RunAction(invite, seen.Box, seen.DetailJson, override)
					clicks := input.recorded()[start:]
					if err != nil || d == nil || d.Success == tc.failReject || rejectAttempted != tc.failReject ||
						tc.wantClick.Empty() && len(clicks) != 0 ||
						!tc.wantClick.Empty() && (len(clicks) != 1 || !clicks[0].In(tc.wantClick)) {
						t.Errorf("want click in %v (empty means none), got %v, failed reject attempted=%t, action=%v err=%v",
							tc.wantClick, clicks, rejectAttempted, d, err)
					}
					raw, err = ctx.GetAnchor(state)
					var got authorization
					if err != nil || json.Unmarshal([]byte(raw), &got) != nil ||
						got.AcceptedPeer != tc.wantPeer || got.ContinuityPeer != "" || got.AcceptedUntil.IsZero() != (tc.wantPeer == "") {
						t.Errorf("want unconsumed invitation for %q, got %s (err=%v)", tc.wantPeer, raw, err)
					}
				})
			})
		}
	})
}
