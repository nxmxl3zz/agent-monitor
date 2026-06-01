package core

import "testing"

func TestInferBalanceSemantics(t *testing.T) {
	spec := ProviderSpec{
		CreditMetrics: map[string]BalanceSemantics{
			"credit_balance": BalanceCumulative,
			"spend_limit":    BalanceLimit,
		},
	}
	cases := []struct {
		name      string
		metricKey string
		window    string
		want      BalanceSemantics
		wantOK    bool
	}{
		{"explicit cumulative wins over window", "credit_balance", "current", BalanceCumulative, true},
		{"explicit limit", "spend_limit", "billing-cycle", BalanceLimit, true},
		{"undeclared lifetime window infers cumulative", "other", "lifetime", BalanceCumulative, true},
		{"undeclared all-time infers cumulative", "other", "all-time", BalanceCumulative, true},
		{"undeclared current infers balance", "other", "current", BalancePoint, true},
		{"undeclared opaque window not inferable", "other", "30d", "", false},
		{"undeclared empty window not inferable", "other", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := spec.InferBalanceSemantics(tc.metricKey, tc.window)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("InferBalanceSemantics(%q,%q) = (%q,%v), want (%q,%v)",
					tc.metricKey, tc.window, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
