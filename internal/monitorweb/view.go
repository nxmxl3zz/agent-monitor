package monitorweb

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/janekbaraniewski/openusage/internal/core"
)

type View struct {
	UpdatedAt time.Time       `json:"updated_at"`
	Window    core.TimeWindow `json:"window"`
	Providers []Provider      `json:"providers"`
	Sessions  []Session       `json:"sessions"`
	Warnings  []string        `json:"warnings,omitempty"`
}

const sessionsWarning = "Some session data is incomplete."

// Dashboard is the chart page payload. It carries quotas and token usage only;
// session rows stay on /api/snapshot because the page no longer renders them.
type Dashboard struct {
	UpdatedAt time.Time             `json:"updated_at"`
	Providers []Provider            `json:"providers"`
	Warnings  []string              `json:"warnings,omitempty"`
	Analytics core.MonitorAnalytics `json:"analytics"`
}

func BuildDashboard(view View, analytics core.MonitorAnalytics) Dashboard {
	for i := range analytics.Points {
		analytics.Points[i].Model = safeText(analytics.Points[i].Model)
	}
	dashboard := Dashboard{UpdatedAt: view.UpdatedAt, Providers: view.Providers, Analytics: analytics}
	for _, warning := range view.Warnings {
		if warning != sessionsWarning {
			dashboard.Warnings = append(dashboard.Warnings, warning)
		}
	}
	return dashboard
}

type Provider struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Status    core.Status `json:"status"`
	Detail    string      `json:"detail,omitempty"` // "rate_limited" when the quota API is throttled
	UpdatedAt *time.Time  `json:"updated_at"`
	Quotas    []Quota     `json:"quotas"`
}

type Quota struct {
	Label       string     `json:"label"`
	UsedPercent *float64   `json:"used_percent"`
	ResetsAt    *time.Time `json:"resets_at"`
}

type Session struct {
	ID             string            `json:"id"`
	Provider       string            `json:"provider"`
	Project        string            `json:"project"`
	Model          string            `json:"model"`
	State          core.SessionState `json:"state"`
	LastActivityAt time.Time         `json:"last_activity_at"`
	Tool           string            `json:"tool"`
	Tokens         *int64            `json:"tokens"`
}

func safeText(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(value))
	return string([]rune(value)[:min(160, len([]rune(value)))])
}

// Project deliberately exposes only the basename; provider metadata and log paths
// never enter the web response.
func BuildView(frame core.SessionActivityFrame, snapshots map[string]core.UsageSnapshot) View {
	view := View{UpdatedAt: frame.UpdatedAt, Window: frame.Window, Providers: []Provider{}, Sessions: []Session{}}
	if len(frame.Warnings) > 0 {
		view.Warnings = append(view.Warnings, sessionsWarning)
	}
	for _, spec := range []struct{ id, name, primary, secondary string }{
		{"codex", "Codex", "rate_limit_primary", "rate_limit_secondary"},
		{"claude_code", "Claude", "usage_five_hour", "usage_seven_day"},
	} {
		var snapshot core.UsageSnapshot
		// Deterministic choice if the profile later contains multiple accounts.
		var ids []string
		for id, candidate := range snapshots {
			if candidate.ProviderID == spec.id {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		if len(ids) > 0 {
			snapshot = snapshots[ids[0]]
		}
		status := snapshot.Status
		switch status {
		case core.StatusOK, core.StatusNearLimit, core.StatusLimited, core.StatusAuth, core.StatusError:
		default:
			status = core.StatusUnknown
		}
		detail := ""
		if reason := snapshot.Diagnostics["usage_api_error"]; reason != "" {
			status = core.StatusError
			if reason == "rate_limited" {
				detail = reason
			}
		}
		provider := Provider{ID: spec.id, Name: spec.name, Status: status, Detail: detail, Quotas: []Quota{}}
		if !snapshot.Timestamp.IsZero() {
			at := snapshot.Timestamp
			provider.UpdatedAt = &at
		}
		for _, item := range []struct{ key, label string }{{spec.primary, "5 hours"}, {spec.secondary, "Weekly"}} {
			quota := Quota{Label: item.label}
			if metric, ok := snapshot.Metrics[item.key]; ok && metric.Used != nil && !math.IsNaN(*metric.Used) && !math.IsInf(*metric.Used, 0) {
				used := min(100.0, max(0.0, *metric.Used))
				quota.UsedPercent = &used
			}
			if at, ok := snapshot.Resets[item.key]; ok && !at.IsZero() {
				quota.ResetsAt = &at
			}
			provider.Quotas = append(provider.Quotas, quota)
		}
		view.Providers = append(view.Providers, provider)
	}
	for _, row := range frame.Sessions {
		if row.ProviderID != "codex" && row.ProviderID != "claude_code" {
			continue
		}
		state := row.State
		switch state {
		case core.SessionWorking, core.SessionWaiting:
			if frame.UpdatedAt.Sub(row.LastActivityAt) > 2*time.Minute {
				state = core.SessionUnknown
			}
		case core.SessionFinished, core.SessionInterrupted:
		default:
			state = core.SessionUnknown
		}
		project := ""
		if row.Project != "" {
			project = safeText(filepath.Base(row.Project))
		}
		tool := row.ActiveTool
		if tool == "" {
			tool = row.LastTool
		}
		session := Session{ID: safeText(row.SessionID), Provider: row.ProviderID, Project: project, Model: safeText(row.Model), State: state, LastActivityAt: row.LastActivityAt, Tool: safeText(tool)}
		if row.TokensKnown {
			tokens := row.TotalTokens
			session.Tokens = &tokens
		}
		view.Sessions = append(view.Sessions, session)
	}
	return view
}
