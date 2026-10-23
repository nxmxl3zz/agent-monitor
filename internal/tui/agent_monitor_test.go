package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/janekbaraniewski/openusage/internal/core"
)

func testMonitor() *AgentMonitorModel {
	return NewAgentMonitorModel(func(context.Context, core.TimeWindow) (core.SessionActivityFrame, error) {
		return core.SessionActivityFrame{}, nil
	}, func(context.Context, core.TimeWindow) (map[string]core.UsageSnapshot, error) { return nil, nil }, core.TimeWindow1d)
}

func TestAgentMonitorResponsiveAndMissingData(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {100, 30}, {80, 24}, {45, 18}, {20, 8}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := testMonitor()
			m.width, m.height = size[0], size[1]
			m.activity.Sessions = []core.SessionActivity{{ProviderID: "codex", SessionID: "s", Project: "/work/工程-proyecto", State: core.SessionWorking, LastTool: "Read", LastActivityAt: time.Now()}}
			view := m.View()
			for _, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) > size[0] {
					t.Fatalf("line exceeds width: %d > %d", lipgloss.Width(line), size[0])
				}
			}
			if lipgloss.Height(view) > size[1] {
				t.Fatal("view exceeds height")
			}
			if size[0] >= 80 && !strings.Contains(ansi.Strip(view), "No quota available") {
				t.Fatal("missing quota displayed as a number")
			}
		})
	}
}

func TestAgentMonitorFiltersDisconnectAndTerminalEscapes(t *testing.T) {
	m := testMonitor()
	m.activity.Sessions = []core.SessionActivity{{ProviderID: "codex", SessionID: "c", Project: "/work/api", State: core.SessionWorking, ActiveTool: "Bash"}, {ProviderID: "claude_code", SessionID: "a", Project: "/work/web", State: core.SessionFinished}}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if rows := m.visibleSessions(); len(rows) != 1 || rows[0].ProviderID != "codex" {
		t.Fatal("provider filter failed")
	}
	m.provider = 0
	m.filter = "web"
	if rows := m.visibleSessions(); len(rows) != 1 || rows[0].SessionID != "a" {
		t.Fatal("project filter failed")
	}
	m.filter = ""
	m.activityErr = errors.New("offline")
	for _, row := range m.visibleSessions() {
		if row.State == core.SessionWorking || row.ActiveTool != "" {
			t.Fatal("disconnected data shown as live")
		}
	}
	if got := monitorSafe("\x1b[31mhello\x1b[0m\n\x1b]0;unsafe\a"); got != "hello" {
		t.Fatalf("terminal escape leaked: %q", got)
	}
	if monitorTokenLabel(core.SessionActivity{}) != "—" {
		t.Fatal("unknown tokens shown as zero")
	}
}

func TestAgentMonitorIgnoresOldWindowResponses(t *testing.T) {
	m := testMonitor()
	m.window = core.TimeWindow7d
	m.Update(monitorActivityMsg{window: core.TimeWindow1d, frame: core.SessionActivityFrame{Sessions: []core.SessionActivity{{SessionID: "old"}}}})
	if len(m.activity.Sessions) != 0 {
		t.Fatal("response from old reporting window used")
	}
}
