package tmux

import (
	"strings"
	"testing"

	"github.com/janekbaraniewski/openusage/internal/core"
)

// metricsCtx builds a minimal Context for one provider with the given numeric
// metrics. ColorMode is None so assertions can match the plain text without
// stepping over `#[fg=...]` tokens.
func metricsCtx(provider string, glyphs GlyphTier, metrics map[string]float64) Context {
	m := map[string]core.Metric{}
	for k, v := range metrics {
		vv := v
		m[k] = core.Metric{Used: &vv}
	}
	snap := core.UsageSnapshot{ProviderID: provider, AccountID: "default", Metrics: m}
	return Context{
		Provider:  provider,
		Account:   "default",
		Snapshot:  snap,
		ColorMode: ColorModeNone,
		Glyphs:    glyphs,
	}
}

func TestSnapshotHasPrimaryMetric(t *testing.T) {
	used := func(v float64) *float64 { return &v }

	cc := core.UsageSnapshot{
		ProviderID: "claude_code",
		Metrics:    map[string]core.Metric{"today_api_cost": {Used: used(4.2)}},
	}
	if !snapshotHasPrimaryMetric(cc) {
		t.Fatal("claude_code with today_api_cost should have a primary metric")
	}

	// Ollama exposes no cost/quota/plan alias, so it must never be chosen as
	// the active tool — this is the fix for the blank-llama flicker.
	ol := core.UsageSnapshot{
		ProviderID: "ollama",
		Metrics:    map[string]core.Metric{"running_models": {Used: used(2)}},
	}
	if snapshotHasPrimaryMetric(ol) {
		t.Fatal("ollama without cost/quota metrics should NOT have a primary metric")
	}

	if snapshotHasPrimaryMetric(core.UsageSnapshot{}) {
		t.Fatal("empty snapshot should have no primary metric")
	}
}

func TestCompactPresetLabels(t *testing.T) {
	p, err := SamplePreset("compact")
	if err != nil {
		t.Fatalf("SamplePreset: %v", err)
	}

	// Claude Code: labeled 5h block gauge + today cost, no plan label.
	cc := metricsCtx("claude_code", GlyphTierUnicode, map[string]float64{
		"usage_five_hour": 15,
		"today_api_cost":  6.79,
	})
	out, err := Render(p.Format, cc)
	if err != nil {
		t.Fatalf("render claude: %v", err)
	}
	for _, want := range []string{"5h", "15%", "$6.79/today"} {
		if !strings.Contains(out, want) {
			t.Errorf("claude compact %q missing %q", out, want)
		}
	}
	if strings.Contains(out, "plan") {
		t.Errorf("claude compact should not show plan label: %q", out)
	}

	// Cursor: labeled plan gauge + today cost, no 5h label.
	cur := metricsCtx("cursor", GlyphTierUnicode, map[string]float64{
		"plan_auto_percent_used": 42,
		"today_cost":             3.40,
	})
	out, err = Render(p.Format, cur)
	if err != nil {
		t.Fatalf("render cursor: %v", err)
	}
	for _, want := range []string{"plan", "42%", "$3.40/today"} {
		if !strings.Contains(out, want) {
			t.Errorf("cursor compact %q missing %q", out, want)
		}
	}
	if strings.Contains(out, "5h") {
		t.Errorf("cursor compact should not show 5h label: %q", out)
	}

	// Cost-only provider: no gauge label, just the cost.
	or := metricsCtx("openrouter", GlyphTierUnicode, map[string]float64{
		"today_cost": 1.23,
	})
	out, err = Render(p.Format, or)
	if err != nil {
		t.Fatalf("render openrouter: %v", err)
	}
	if !strings.Contains(out, "$1.23/today") {
		t.Errorf("openrouter compact missing cost: %q", out)
	}
	if strings.Contains(out, "5h") || strings.Contains(out, "plan") {
		t.Errorf("openrouter compact should have no gauge label: %q", out)
	}
}
