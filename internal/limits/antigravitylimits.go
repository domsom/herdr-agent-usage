/**
 * Antigravity CLI limit collection.
 *
 * Antigravity keeps no local limits file the way Claude's statusLine cache
 * does; its statusLine payload is the only local surface, exactly like this
 * provider's context resolution. The collector therefore reads the newest
 * fresh snapshot across every session under sessionsDir rather than one keyed
 * by pane or session: quota is billed per Google account, not per
 * conversation, so whichever pane last reported the account's usage speaks
 * for the whole account.
 */
package limits

import (
	"time"

	"github.com/senna-lang/herdr-agent-usage/internal/providers/antigravity"
)

// CollectAntigravityLimitsOptions injects the sessions directory for tests.
type CollectAntigravityLimitsOptions struct {
	SessionsDir string
}

// antigravity5HourWindowMinutes is the duration for short-window quotas (5 hours).
const antigravity5HourWindowMinutes = 300

// antigravityWeeklyWindowMinutes is the duration Antigravity reports
// for weekly quota buckets (a rolling week).
const antigravityWeeklyWindowMinutes = 10080

// CollectAntigravityLimits builds a ProviderLimits from the freshest
// Antigravity statusLine snapshot across every known session.
//
// Antigravity tracks both a short rolling window ("gemini-5h") and a weekly
// window ("gemini-weekly") for Gemini, plus third-party allotments ("3p-5h",
// "3p-weekly").
// gemini-5h is shown as Primary (short window) and gemini-weekly as Secondary
// (weekly window), matching the 5h / 7d display used by Claude and Codex.
func CollectAntigravityLimits(nowMs int64, opts CollectAntigravityLimitsOptions) ProviderLimits {
	const providerID = "agy"
	const label = "Antigravity"

	sessionsDir := opts.SessionsDir
	if sessionsDir == "" {
		stateDir := antigravity.StateDir()
		if stateDir == "" {
			note := "no home directory"
			return ProviderLimits{ProviderID: providerID, Label: label, Source: "none", FetchedAtMs: nowMs, Note: &note}
		}
		sessionsDir = antigravity.SessionsDir(stateDir)
	}

	snap, ok := antigravity.LatestFreshSnapshot(sessionsDir, nowMs)
	if !ok {
		note := "no statusLine observation yet — see docs/antigravity-contract.md"
		return ProviderLimits{ProviderID: providerID, Label: label, Source: "none", FetchedAtMs: nowMs, Note: &note}
	}

	pl := ProviderLimits{
		ProviderID:  providerID,
		Label:       label,
		Source:      "antigravity statusLine",
		FetchedAtMs: nowMs,
	}
	if snap.PlanTier != "" {
		plan := snap.PlanTier
		pl.PlanType = &plan
	}
	// Primary is the short window (5h). Fall back to weekly if 5h is absent.
	if w, ok := snap.Quota["gemini-5h"]; ok {
		pl.Primary = quotaLimitWindow(w, antigravity5HourWindowMinutes)
	} else if w, ok := snap.Quota["3p-5h"]; ok {
		pl.Primary = quotaLimitWindow(w, antigravity5HourWindowMinutes)
	} else if w, ok := snap.Quota["gemini-weekly"]; ok {
		pl.Primary = quotaLimitWindow(w, antigravityWeeklyWindowMinutes)
	} else if w, ok := snap.Quota["3p-weekly"]; ok {
		pl.Primary = quotaLimitWindow(w, antigravityWeeklyWindowMinutes)
	}

	// Secondary is the weekly window when 5h was present for Primary.
	if pl.Primary != nil && (pl.Primary.WindowMinutes == nil || *pl.Primary.WindowMinutes == antigravity5HourWindowMinutes) {
		if w, ok := snap.Quota["gemini-weekly"]; ok {
			pl.Secondary = quotaLimitWindow(w, antigravityWeeklyWindowMinutes)
		} else if w, ok := snap.Quota["3p-weekly"]; ok {
			pl.Secondary = quotaLimitWindow(w, antigravityWeeklyWindowMinutes)
		}
	}
	return pl
}

// quotaLimitWindow converts one statusLine quota bucket into a LimitWindow.
// ResetsAt is parsed from the bucket's own absolute reset_time rather than
// derived from a relative "seconds remaining" figure, which would only be
// accurate at the instant it was captured.
func quotaLimitWindow(w antigravity.QuotaWindow, windowMinutes int) *LimitWindow {
	used := (1 - w.RemainingFraction) * 100
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	window := LimitWindow{UsedPercentage: used}
	minutes := windowMinutes
	window.WindowMinutes = &minutes
	if t, err := time.Parse(time.RFC3339, w.ResetTime); err == nil {
		resetsAt := t.Unix()
		window.ResetsAt = &resetsAt
	}
	return &window
}
