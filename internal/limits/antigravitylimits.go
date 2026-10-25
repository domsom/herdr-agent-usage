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
// Antigravity tracks a short rolling window ("gemini-5h") and a weekly
// window ("gemini-weekly") for Gemini, third-party allotments ("3p-5h",
// "3p-weekly"), and an Argon quota ("gemini-argon-wtu-weekly").
// gemini-5h is shown as Primary (short window) and gemini-weekly as Secondary
// (weekly window), matching the 5h / 7d display used by Claude and Codex.
// When Argon or 3p quota is present, Tertiary carries the Argon weekly quota
// (or 3p-weekly if Argon is absent).
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
	// Primary is Gemini's native quota (gemini-5h if present, else gemini-weekly).
	if w, ok := snap.Quota["gemini-5h"]; ok {
		pl.Primary = quotaLimitWindow(w, antigravity5HourWindowMinutes)
	} else if w, ok := snap.Quota["gemini-weekly"]; ok {
		pl.Primary = quotaLimitWindow(w, antigravityWeeklyWindowMinutes)
	}

	// Secondary is the mid/weekly window or third-party quota.
	// If gemini-5h took Primary, gemini-weekly takes Secondary.
	// Otherwise, 3p-weekly (or 3p-5h) takes Secondary.
	if pl.Primary != nil && pl.Primary.WindowMinutes != nil && *pl.Primary.WindowMinutes == antigravity5HourWindowMinutes {
		if w, ok := snap.Quota["gemini-weekly"]; ok {
			pl.Secondary = quotaLimitWindow(w, antigravityWeeklyWindowMinutes)
		} else if w, ok := snap.Quota["3p-weekly"]; ok {
			pl.Secondary = quotaLimitWindow(w, antigravityWeeklyWindowMinutes)
		}
	} else {
		if w, ok := snap.Quota["3p-weekly"]; ok {
			pl.Secondary = quotaLimitWindow(w, antigravityWeeklyWindowMinutes)
		} else if w, ok := snap.Quota["3p-5h"]; ok {
			pl.Secondary = quotaLimitWindow(w, antigravity5HourWindowMinutes)
		}
	}

	// Tertiary carries Argon WTU weekly quota or 3p-weekly.
	if w, ok := snap.Quota["gemini-argon-wtu-weekly"]; ok {
		pl.Tertiary = quotaLimitWindow(w, antigravityWeeklyWindowMinutes)
	} else if _, hasGeminiWeekly := snap.Quota["gemini-weekly"]; hasGeminiWeekly {
		// When both gemini-5h and gemini-weekly took Primary & Secondary,
		// 3p-weekly can occupy Tertiary.
		if w, ok := snap.Quota["3p-weekly"]; ok && pl.Secondary != nil && pl.Secondary.WindowMinutes != nil && *pl.Secondary.WindowMinutes == antigravityWeeklyWindowMinutes {
			pl.Tertiary = quotaLimitWindow(w, antigravityWeeklyWindowMinutes)
		}
	}
	return pl
}

// CollectAntigravityGroupedLimits returns one ProviderLimits per quota bucket
// family present in the snapshot, grouped under the shared "Antigravity" heading.
// Families:
//  - "Gemini": gemini-5h (Primary) and gemini-weekly (Secondary)
//  - "Claude & GPT": 3p-5h (Primary) and 3p-weekly (Secondary)
//  - "Gemini Argon": gemini-argon-wtu-weekly (Secondary / 7d)
//
// If only the Gemini buckets are present, a single ungrouped ProviderLimits
// is returned to keep a compact, clean layout.
func CollectAntigravityGroupedLimits(nowMs int64, opts CollectAntigravityLimitsOptions) []ProviderLimits {
	const group = "Antigravity"
	const baseProviderID = "agy"

	sessionsDir := opts.SessionsDir
	if sessionsDir == "" {
		stateDir := antigravity.StateDir()
		if stateDir == "" {
			note := "no home directory"
			return []ProviderLimits{{ProviderID: baseProviderID, Label: group, Source: "none", FetchedAtMs: nowMs, Note: &note}}
		}
		sessionsDir = antigravity.SessionsDir(stateDir)
	}

	snap, ok := antigravity.LatestFreshSnapshot(sessionsDir, nowMs)
	if !ok {
		note := "no statusLine observation yet — see docs/antigravity-contract.md"
		return []ProviderLimits{{ProviderID: baseProviderID, Label: group, Source: "none", FetchedAtMs: nowMs, Note: &note}}
	}

	var plan *string
	if snap.PlanTier != "" {
		p := snap.PlanTier
		plan = &p
	}

	hasGemini := false
	var geminiLimits ProviderLimits
	if w5, ok5 := snap.Quota["gemini-5h"]; ok5 {
		hasGemini = true
		geminiLimits.Primary = quotaLimitWindow(w5, antigravity5HourWindowMinutes)
	}
	if w7, ok7 := snap.Quota["gemini-weekly"]; ok7 {
		hasGemini = true
		if geminiLimits.Primary == nil {
			geminiLimits.Primary = quotaLimitWindow(w7, antigravityWeeklyWindowMinutes)
		} else {
			geminiLimits.Secondary = quotaLimitWindow(w7, antigravityWeeklyWindowMinutes)
		}
	}

	has3P := false
	var threePLimits ProviderLimits
	if w5, ok5 := snap.Quota["3p-5h"]; ok5 {
		has3P = true
		threePLimits.Primary = quotaLimitWindow(w5, antigravity5HourWindowMinutes)
	}
	if w7, ok7 := snap.Quota["3p-weekly"]; ok7 {
		has3P = true
		if threePLimits.Primary == nil {
			threePLimits.Primary = quotaLimitWindow(w7, antigravityWeeklyWindowMinutes)
		} else {
			threePLimits.Secondary = quotaLimitWindow(w7, antigravityWeeklyWindowMinutes)
		}
	}

	hasArgon := false
	var argonLimits ProviderLimits
	if w7, ok7 := snap.Quota["gemini-argon-wtu-weekly"]; ok7 {
		hasArgon = true
		argonLimits.Secondary = quotaLimitWindow(w7, antigravityWeeklyWindowMinutes)
	}

	totalFamilies := 0
	if hasGemini {
		totalFamilies++
	}
	if has3P {
		totalFamilies++
	}
	if hasArgon {
		totalFamilies++
	}

	if totalFamilies <= 1 {
		return []ProviderLimits{CollectAntigravityLimits(nowMs, opts)}
	}

	var out []ProviderLimits
	if hasGemini {
		geminiLimits.ProviderID = baseProviderID
		geminiLimits.Label = group
		geminiLimits.GroupLabel = group
		geminiLimits.AccountLabel = "Gemini"
		geminiLimits.PlanType = plan
		geminiLimits.Source = "antigravity statusLine"
		geminiLimits.FetchedAtMs = nowMs
		out = append(out, geminiLimits)
	}
	if has3P {
		threePLimits.ProviderID = baseProviderID + "-3p"
		threePLimits.Label = "Claude & GPT"
		threePLimits.GroupLabel = group
		threePLimits.AccountLabel = "Claude & GPT"
		threePLimits.Source = "antigravity statusLine"
		threePLimits.FetchedAtMs = nowMs
		out = append(out, threePLimits)
	}
	if hasArgon {
		argonLimits.ProviderID = baseProviderID + "-argon"
		argonLimits.Label = "Gemini Argon"
		argonLimits.GroupLabel = group
		argonLimits.AccountLabel = "Gemini Argon"
		argonLimits.Source = "antigravity statusLine"
		argonLimits.FetchedAtMs = nowMs
		out = append(out, argonLimits)
	}
	return out
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
