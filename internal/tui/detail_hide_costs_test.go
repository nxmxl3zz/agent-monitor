package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/janekbaraniewski/openusage/internal/core"
)

// snapshotWithCosts returns a snapshot that exercises the dollar-amount paths:
// today_api_cost, 5h_block_cost, and a burn_rate-derived cost summary.
func snapshotWithCosts() core.UsageSnapshot {
	today := 1.23
	block := 0.45
	return core.UsageSnapshot{
		ProviderID: "claude_code",
		AccountID:  "claude-code",
		Status:     core.StatusOK,
		Timestamp:  time.Now(),
		Metrics: map[string]core.Metric{
			"today_api_cost": {Used: &today, Unit: "USD", Window: "today"},
			"5h_block_cost":  {Used: &block, Unit: "USD", Window: "5h"},
		},
		Raw: map[string]string{"subscription": "active"},
	}
}

func TestRenderDetailContent_HideCostsSuppressesDollars(t *testing.T) {
	snap := snapshotWithCosts()

	shown := RenderDetailContent(snap, time.Now(), 120, 0.20, 0.05, 0, core.TimeWindow30d, false)
	hidden := RenderDetailContent(snap, time.Now(), 120, 0.20, 0.05, 0, core.TimeWindow30d, true)

	if !strings.Contains(shown, "$1.23") {
		t.Errorf("expected $1.23 in shown render, missing")
	}
	if strings.Contains(hidden, "$1.23") {
		t.Errorf("expected $1.23 suppressed when hideCosts=true")
	}
	// The Spending and Forecast cards should not appear when hideCosts is on.
	if strings.Contains(hidden, "Spending") {
		t.Errorf("Spending card should be suppressed")
	}
	if strings.Contains(hidden, "Forecast") {
		t.Errorf("Forecast card should be suppressed")
	}
}

func TestResolveHideCosts_ModelIntegration(t *testing.T) {
	// Subscription claude_code account: auto policy hides costs by default.
	subSnap := core.UsageSnapshot{
		ProviderID: "claude_code",
		AccountID:  "claude-code",
		Raw:        map[string]string{"subscription": "active"},
	}
	m := Model{}
	if !m.resolveHideCosts(subSnap) {
		t.Errorf("default auto policy should hide costs for subscription claude_code")
	}

	// Per-account override beats auto.
	show := false
	m.hideCostsByAccount = map[string]*bool{"claude-code": &show}
	if m.resolveHideCosts(subSnap) {
		t.Errorf("per-account override false should show costs")
	}

	// Global override beats auto when per-account is nil.
	m2 := Model{}
	hide := true
	m2.hideCostsGlobal = &hide
	apiSnap := core.UsageSnapshot{ProviderID: "openai", AccountID: "openai-key"}
	if !m2.resolveHideCosts(apiSnap) {
		t.Errorf("global=true should hide costs for openai")
	}
}
