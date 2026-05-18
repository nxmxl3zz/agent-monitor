package kiro

import (
	"github.com/janekbaraniewski/openusage/internal/core"
	"github.com/janekbaraniewski/openusage/internal/providers/providerbase"
)

func dashboardWidget() core.DashboardWidget {
	cfg := providerbase.CodingToolDashboard(
		providerbase.WithColorRole(core.DashboardColorRoleYellow),
		providerbase.WithGaugePriority(
			"total_conversations", "total_tokens",
		),
		providerbase.WithCompactRows(
			core.DashboardCompactRow{
				Label:       "Conversations",
				Keys:        []string{"total_conversations", "conversations_with_tokens"},
				MaxSegments: 3,
			},
			core.DashboardCompactRow{
				Label:       "Tokens",
				Keys:        []string{"total_tokens", "total_input_tokens", "total_output_tokens"},
				MaxSegments: 3,
			},
		),
		providerbase.WithMetricLabels(map[string]string{
			"total_conversations":       "Conversations",
			"conversations_with_tokens": "With Tokens",
			"total_tokens":              "Total Tokens",
			"total_input_tokens":        "Input Tokens",
			"total_output_tokens":       "Output Tokens",
			"total_messages":            "Messages",
		}),
		providerbase.WithCompactLabels(map[string]string{
			"total_conversations":       "all",
			"conversations_with_tokens": "tok",
			"total_tokens":              "total",
			"total_input_tokens":        "in",
			"total_output_tokens":       "out",
		}),
	)
	return cfg
}

func detailWidget() core.DetailWidget {
	return core.CodingToolDetailWidget(false)
}
