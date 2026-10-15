/**
 * Tests for Antigravity limit collection.
 */
package limits

import (
	"path/filepath"
	"testing"

	"github.com/senna-lang/herdr-agent-usage/internal/providers/antigravity"
)

func TestCollectAntigravityLimits_NoSnapshot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	got := CollectAntigravityLimits(1000, CollectAntigravityLimitsOptions{SessionsDir: dir})
	if got.ProviderID != "agy" || got.Source != "none" {
		t.Fatalf("got %+v", got)
	}
	if got.Note == nil {
		t.Fatal("expected a note explaining the missing observation")
	}
	if got.Primary != nil || got.Secondary != nil {
		t.Fatalf("got %+v, want no windows", got)
	}
}

func TestCollectAntigravityLimits_ReadsFreshestSnapshot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := antigravity.WriteSnapshot(dir, antigravity.Snapshot{
		SessionID: "a",
		PlanTier:  "Antigravity Starter Quota",
		Quota: map[string]antigravity.QuotaWindow{
			"gemini-5h":     {RemainingFraction: 0.80, ResetTime: "2026-09-22T08:06:55Z"},
			"gemini-weekly": {RemainingFraction: 0.75, ResetTime: "2026-09-22T10:06:55Z"},
			"3p-weekly":     {RemainingFraction: 1, ResetTime: "2026-09-22T10:11:18Z"},
		},
		UpdatedAtMs: 1000,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := CollectAntigravityLimits(1000, CollectAntigravityLimitsOptions{SessionsDir: dir})
	if got.ProviderID != "agy" || got.Label != "Antigravity" {
		t.Fatalf("got %+v", got)
	}
	if got.PlanType == nil || *got.PlanType != "Antigravity Starter Quota" {
		t.Fatalf("PlanType = %v", got.PlanType)
	}
	if got.Primary == nil || got.Primary.UsedPercentage != 20 {
		t.Fatalf("Primary (gemini-5h) = %+v", got.Primary)
	}
	if got.Primary.WindowMinutes == nil || *got.Primary.WindowMinutes != 300 {
		t.Fatalf("Primary.WindowMinutes = %v, want 300", got.Primary.WindowMinutes)
	}
	if got.Secondary == nil || got.Secondary.UsedPercentage != 25 {
		t.Fatalf("Secondary (gemini-weekly) = %+v", got.Secondary)
	}
	if got.Secondary.WindowMinutes == nil || *got.Secondary.WindowMinutes != 10080 {
		t.Fatalf("Secondary.WindowMinutes = %v, want 10080", got.Secondary.WindowMinutes)
	}
	wantReset := int64(1790071615) // 2026-09-22T10:06:55Z
	if got.Secondary.ResetsAt == nil || *got.Secondary.ResetsAt != wantReset {
		t.Fatalf("Secondary.ResetsAt = %v, want %d", got.Secondary.ResetsAt, wantReset)
	}
}

// Quota is billed per Google account, not per conversation: the freshest
// observation from any session speaks for the whole account, and an older
// session's snapshot must not shadow it.
func TestCollectAntigravityLimits_PicksNewestAcrossSessions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := antigravity.WriteSnapshot(dir, antigravity.Snapshot{
		SessionID:   "old",
		Quota:       map[string]antigravity.QuotaWindow{"gemini-weekly": {RemainingFraction: 0.1, ResetTime: "2026-09-22T10:06:55Z"}},
		UpdatedAtMs: 1,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := antigravity.WriteSnapshot(dir, antigravity.Snapshot{
		SessionID:   "new",
		Quota:       map[string]antigravity.QuotaWindow{"gemini-weekly": {RemainingFraction: 0.9, ResetTime: "2026-09-22T10:06:55Z"}},
		UpdatedAtMs: 2000,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got := CollectAntigravityLimits(2000, CollectAntigravityLimitsOptions{SessionsDir: dir})
	if got.Primary == nil {
		t.Fatal("got nil Primary")
	}
	if diff := got.Primary.UsedPercentage - 10; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("got %+v, want the newer session's 10%% used", got.Primary)
	}
}

func TestCollectAntigravityLimits_StaleSnapshotYieldsNone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := antigravity.WriteSnapshot(dir, antigravity.Snapshot{
		SessionID:   "old",
		Quota:       map[string]antigravity.QuotaWindow{"gemini-weekly": {RemainingFraction: 0.5, ResetTime: "2026-09-22T10:06:55Z"}},
		UpdatedAtMs: 0,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got := CollectAntigravityLimits(antigravity.SnapshotFreshnessMs+1000, CollectAntigravityLimitsOptions{SessionsDir: dir})
	if got.Source != "none" || got.Primary != nil {
		t.Fatalf("got %+v, want no usable observation", got)
	}
}

// A missing gemini-weekly bucket (a plan without a native quota, for
// example) must not synthesize a fake window.
func TestCollectAntigravityLimits_MissingBucketLeavesWindowNil(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := antigravity.WriteSnapshot(dir, antigravity.Snapshot{
		SessionID:   "a",
		Quota:       map[string]antigravity.QuotaWindow{"3p-weekly": {RemainingFraction: 1, ResetTime: "2026-09-22T10:11:18Z"}},
		UpdatedAtMs: 1000,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got := CollectAntigravityLimits(1000, CollectAntigravityLimitsOptions{SessionsDir: dir})
	if got.Primary != nil {
		t.Fatalf("Primary = %+v, want nil when gemini-weekly is absent", got.Primary)
	}
	if got.Secondary == nil {
		t.Fatal("Secondary = nil, want the present 3p-weekly bucket")
	}
}
