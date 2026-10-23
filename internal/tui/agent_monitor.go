package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/janekbaraniewski/openusage/internal/core"
)

type monitorTick time.Time
type monitorActivityMsg struct {
	frame  core.SessionActivityFrame
	err    error
	window core.TimeWindow
}
type monitorQuotaMsg struct {
	snapshots map[string]core.UsageSnapshot
	err       error
	window    core.TimeWindow
}

type AgentMonitorModel struct {
	activityLoader                  func(context.Context, core.TimeWindow) (core.SessionActivityFrame, error)
	quotaLoader                     func(context.Context, core.TimeWindow) (map[string]core.UsageSnapshot, error)
	activity                        core.SessionActivityFrame
	snapshots                       map[string]core.UsageSnapshot
	activityErr, quotaErr           error
	activityLoading, quotaLoading   bool
	lastQuota                       time.Time
	window                          core.TimeWindow
	now                             time.Time
	width, height, cursor, provider int
	filter                          string
	filtering                       bool
	OnWindow                        func(core.TimeWindow)
	OnTheme                         func(string)
}

func NewAgentMonitorModel(
	activity func(context.Context, core.TimeWindow) (core.SessionActivityFrame, error),
	quotas func(context.Context, core.TimeWindow) (map[string]core.UsageSnapshot, error),
	window core.TimeWindow,
) *AgentMonitorModel {
	return &AgentMonitorModel{activityLoader: activity, quotaLoader: quotas, window: window, now: time.Now(), width: 100, height: 30, snapshots: make(map[string]core.UsageSnapshot)}
}

func monitorTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return monitorTick(t) })
}

func (m *AgentMonitorModel) loadActivity() tea.Cmd {
	m.activityLoading = true
	window, load := m.window, m.activityLoader
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		frame, err := load(ctx, window)
		return monitorActivityMsg{frame, err, window}
	}
}

func (m *AgentMonitorModel) loadQuotas() tea.Cmd {
	m.quotaLoading = true
	window, load := m.window, m.quotaLoader
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		data, err := load(ctx, window)
		return monitorQuotaMsg{data, err, window}
	}
}

func (m *AgentMonitorModel) Init() tea.Cmd {
	return tea.Batch(monitorTickCmd(), m.loadActivity(), m.loadQuotas())
}

func (m *AgentMonitorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case monitorActivityMsg:
		m.activityLoading = false
		if msg.window == m.window {
			m.activityErr = msg.err
			if msg.err == nil {
				m.activity = msg.frame
			}
		}
	case monitorQuotaMsg:
		m.quotaLoading = false
		m.lastQuota = m.now
		if msg.window == m.window {
			m.quotaErr = msg.err
			if msg.err == nil {
				m.snapshots = msg.snapshots
			}
		}
	case monitorTick:
		m.now = time.Time(msg)
		cmds := []tea.Cmd{monitorTickCmd()}
		if !m.activityLoading {
			cmds = append(cmds, m.loadActivity())
		}
		if !m.quotaLoading && m.now.Sub(m.lastQuota) >= 15*time.Second {
			cmds = append(cmds, m.loadQuotas())
		}
		return m, tea.Batch(cmds...)
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.filtering {
			switch msg.String() {
			case "esc":
				m.filtering = false
				m.filter = ""
			case "enter":
				m.filtering = false
			case "backspace", "ctrl+h":
				r := []rune(m.filter)
				if len(r) > 0 {
					m.filter = string(r[:len(r)-1])
				}
			default:
				if msg.Type == tea.KeyRunes && len([]rune(m.filter)) < 100 {
					m.filter += string(msg.Runes)
				}
			}
			m.cursor = 0
			return m, nil
		}
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "j", "down":
			m.cursor = min(m.cursor+1, max(0, len(m.visibleSessions())-1))
		case "k", "up":
			m.cursor = max(0, m.cursor-1)
		case "tab":
			m.provider = (m.provider + 1) % 3
			m.cursor = 0
		case "/":
			m.filtering = true
			m.cursor = 0
		case "esc":
			m.filter = ""
			m.cursor = 0
		case "r":
			m.lastQuota = time.Time{}
			return m, nil
		case "h":
			m.window = core.NextTimeWindow(m.window)
			m.cursor = 0
			m.activity = core.SessionActivityFrame{}
			m.snapshots = make(map[string]core.UsageSnapshot)
			m.lastQuota = time.Time{}
			if m.OnWindow != nil {
				m.OnWindow(m.window)
			}
		case "t":
			CycleTheme()
			if m.OnTheme != nil {
				m.OnTheme(ActiveTheme().Name)
			}
		}
	}
	return m, nil
}

func monitorSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}

func monitorFit(s string, width int) string {
	s = ansi.Truncate(monitorSafe(s), max(1, width), "…")
	return s + strings.Repeat(" ", max(0, width-lipgloss.Width(s)))
}

func monitorAge(at, now time.Time) string {
	if at.IsZero() {
		return "no data"
	}
	d := max(time.Duration(0), now.Sub(at))
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func monitorTokenLabel(row core.SessionActivity) string {
	if !row.TokensKnown {
		return "—"
	}
	if row.TotalTokens >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(row.TotalTokens)/1_000_000)
	}
	if row.TotalTokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(row.TotalTokens)/1000)
	}
	return fmt.Sprint(row.TotalTokens)
}

func monitorWindow(tw core.TimeWindow) string {
	switch tw {
	case core.TimeWindow1d:
		return "Today"
	case core.TimeWindow3d:
		return "3 days"
	case core.TimeWindow7d:
		return "7 days"
	case core.TimeWindow30d:
		return "30 days"
	default:
		return "All"
	}
}

func monitorReset(d time.Duration) string {
	minutes := int(d.Minutes())
	if minutes >= 24*60 {
		return fmt.Sprintf("%dd %dh", minutes/(24*60), minutes/60%24)
	}
	return fmt.Sprintf("%dh %dm", minutes/60, minutes%60)
}

func monitorState(s core.SessionState) string {
	switch s {
	case core.SessionWorking:
		return "Working"
	case core.SessionWaiting:
		return "Needs input"
	case core.SessionFinished:
		return "Turn done"
	case core.SessionInterrupted:
		return "Interrupted"
	default:
		return "Unconfirmed"
	}
}

func (m *AgentMonitorModel) visibleSessions() []core.SessionActivity {
	var rows []core.SessionActivity
	for _, row := range m.activity.Sessions {
		if m.activityErr != nil && (row.State == core.SessionWorking || row.State == core.SessionWaiting) {
			row.State, row.ActiveTool = core.SessionUnknown, ""
		}
		if m.provider == 1 && row.ProviderID != "codex" || m.provider == 2 && row.ProviderID != "claude_code" {
			continue
		}
		search := strings.ToLower(row.Project + " " + row.Model + " " + row.SessionID + " " + row.LastTool)
		if m.filter != "" && !strings.Contains(search, strings.ToLower(m.filter)) {
			continue
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a := rows[i].State == core.SessionWorking || rows[i].State == core.SessionWaiting
		b := rows[j].State == core.SessionWorking || rows[j].State == core.SessionWaiting
		if a != b {
			return a
		}
		return rows[i].LastActivityAt.After(rows[j].LastActivityAt)
	})
	return rows
}

func (m *AgentMonitorModel) quotaCard(provider, title string, width int) string {
	var snapshot core.UsageSnapshot
	for _, s := range m.snapshots {
		if s.ProviderID == provider {
			snapshot = s
			break
		}
	}
	primary, secondary := "rate_limit_primary", "rate_limit_secondary"
	if provider == "claude_code" {
		primary, secondary = "usage_five_hour", "usage_seven_day"
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render(title)}
	for _, item := range []struct{ key, label string }{{primary, "5 hours"}, {secondary, "Weekly"}} {
		metric, ok := snapshot.Metrics[item.key]
		if !ok || metric.Used == nil {
			lines = append(lines, item.label+"    —  No quota available")
			continue
		}
		used := min(100.0, max(0.0, *metric.Used))
		n := int(used / 100 * 12)
		color := colorGreen
		if used >= 80 {
			color = colorYellow
		}
		if used >= 95 {
			color = colorRed
		}
		bar := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("━", n) + strings.Repeat("·", 12-n))
		line := fmt.Sprintf("%-8s %s %3.0f%% used", item.label, bar, used)
		if reset, ok := snapshot.Resets[item.key]; ok && reset.After(m.now) && width >= 53 {
			line += " · resets in " + monitorReset(reset.Sub(m.now))
		}
		lines = append(lines, line)
	}
	status := "Measured " + monitorAge(snapshot.Timestamp, m.now) + " ago"
	if snapshot.Timestamp.IsZero() {
		status = "Waiting for the first quota check"
	}
	switch snapshot.Diagnostics["usage_api_error"] {
	case "":
	case "rate_limited":
		status = "Quotas rate limited by Anthropic · retrying"
	default:
		status = "Quotas unavailable · check the Claude sign-in"
	}
	if snapshot.Status == core.StatusAuth {
		status = "Sign in to the official client"
	}
	if snapshot.Status == core.StatusError {
		status = "Provider unavailable · " + status
	}
	if m.quotaErr != nil {
		status = "Offline · showing previous values"
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render(status))
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, max(1, width-4), "…")
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBorder).Padding(0, 1).Width(max(1, width-2)).Render(strings.Join(lines, "\n"))
}

func (m *AgentMonitorModel) View() string {
	w, h := max(20, m.width), max(6, m.height)
	rows := m.visibleSessions()
	selected := min(m.cursor, max(0, len(rows)-1))
	provider := []string{"Both", "Codex", "Claude"}[m.provider]
	title := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("AGENT MONITOR") + "  Codex + Claude"
	status := fmt.Sprintf("%s · %s · %d sessions · updated %s ago", provider, monitorWindow(m.window), len(rows), monitorAge(m.activity.UpdatedAt, m.now))
	if m.activityErr != nil {
		status = "Collector offline · previous data · run: agent-monitor service start"
	}
	cards := ""
	if w >= 100 {
		cw := (w - 2) / 2
		cards = lipgloss.JoinHorizontal(lipgloss.Top, m.quotaCard("codex", "Codex", cw), "  ", m.quotaCard("claude_code", "Claude", cw))
	} else {
		cards = m.quotaCard("codex", "Codex", w) + "\n" + m.quotaCard("claude_code", "Claude", w)
	}
	header := title + "\n" + ansi.Truncate(status, w, "…") + "\n" + cards
	filterLabel := "/ filter"
	if m.filter != "" || m.filtering {
		filterLabel = "Filter: " + monitorSafe(m.filter)
		if m.filtering {
			filterLabel += "▏"
		}
	}
	footer := "tab provider · ↑↓ session · " + filterLabel + " · h period · t theme · r refresh · q quit"
	var detail string
	if len(rows) > 0 {
		row := rows[selected]
		tool := row.ActiveTool
		if tool != "" {
			tool = "Running tool: " + tool
		} else if row.LastTool != "" {
			tool = "Last tool: " + row.LastTool
		} else {
			tool = row.StateReason
		}
		detail = monitorSafe(row.SessionID+" · "+row.Project) + "\n" + monitorSafe(tool)
	}
	if len(m.activity.Warnings) > 0 {
		detail += "\n" + monitorSafe(strings.Join(m.activity.Warnings, " · "))
	}
	columns := []int{9, 14, 22, 24, 9, 8}
	labels := []string{"Agent", "State", "Project", "Model", "Tokens", "Last"}
	if w < 100 {
		columns = []int{8, 14, max(8, w-38), 9}
		labels = []string{"Agent", "State", "Project", "Tokens"}
	}
	line := func(cells []string) string {
		var b strings.Builder
		for i, c := range cells {
			b.WriteString(monitorFit(c, columns[i]))
			if i < len(cells)-1 {
				b.WriteByte(' ')
			}
		}
		return ansi.Truncate(b.String(), w, "…")
	}
	var body []string
	body = append(body, lipgloss.NewStyle().Foreground(colorDim).Render(line(labels)))
	room := max(1, h-lipgloss.Height(header)-lipgloss.Height(detail)-4)
	start := max(0, selected-room+1)
	for i := start; i < min(len(rows), start+room); i++ {
		row := rows[i]
		name := "Codex"
		if row.ProviderID == "claude_code" {
			name = "Claude"
		}
		project := filepath.Base(row.Project)
		if row.Project == "" {
			project = "—"
		}
		cells := []string{name, monitorState(row.State), project, row.Model, monitorTokenLabel(row), monitorAge(row.LastActivityAt, m.now)}
		if w < 100 {
			cells = []string{name, monitorState(row.State), project, monitorTokenLabel(row)}
		}
		if i == selected {
			cells[0] = "› " + name
		}
		rendered := line(cells)
		if i == selected {
			rendered = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(rendered)
		}
		body = append(body, rendered)
	}
	if len(rows) == 0 {
		body = append(body, "Waiting for session logs in this period.")
	}
	result := header + "\n" + strings.Join(body, "\n") + "\n" + detail + "\n" + lipgloss.NewStyle().Foreground(colorDim).Render(footer)
	lines := strings.Split(result, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], w, "…")
	}
	if len(lines) > h {
		lines = append(lines[:h-1], ansi.Truncate(footer, w, "…"))
	}
	return strings.Join(lines, "\n")
}
