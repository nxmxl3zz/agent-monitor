package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/janekbaraniewski/openusage/internal/config"
	"github.com/janekbaraniewski/openusage/internal/core"
	"github.com/janekbaraniewski/openusage/internal/daemon"
	"github.com/janekbaraniewski/openusage/internal/tui"
	"github.com/spf13/cobra"
)

func newMonitorCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "monitor", Short: "Monitor Codex and Claude quotas and observed session activity", RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		window := core.ParseTimeWindow(cfg.Data.TimeWindow)
		client := daemon.NewClient(daemon.ResolveSocketPath())
		request, err := daemon.BuildReadModelRequestFromConfig()
		if err != nil {
			return err
		}
		activity := func(ctx context.Context, tw core.TimeWindow) (core.SessionActivityFrame, error) {
			return client.Sessions(ctx, tw)
		}
		quotas := func(ctx context.Context, tw core.TimeWindow) (map[string]core.UsageSnapshot, error) {
			req := request
			req.TimeWindow = tw
			return client.ReadModel(ctx, req)
		}
		if asJSON {
			ctx, cancel := context.WithTimeout(cmd.Context(), 12*time.Second)
			defer cancel()
			sessions, err := activity(ctx, window)
			if err != nil {
				return fmt.Errorf("start the collector with './scripts/agent-monitor service start': %w", err)
			}
			snapshots, err := quotas(ctx, window)
			if err != nil {
				return err
			}
			for id, snapshot := range snapshots {
				snapshot.Raw = nil
				snapshots[id] = snapshot
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(struct {
				Activity  core.SessionActivityFrame     `json:"activity"`
				Snapshots map[string]core.UsageSnapshot `json:"snapshots"`
			}{sessions, snapshots})
		}
		if err := requireDashboardTerminal(); err != nil {
			return err
		}
		if err := tui.LoadThemes(config.ConfigDir()); err != nil {
			return err
		}
		tui.SetThemeByName(cfg.Theme)
		model := tui.NewAgentMonitorModel(activity, quotas, window)
		model.OnWindow = func(tw core.TimeWindow) { _ = config.SaveTimeWindow(string(tw)) }
		model.OnTheme = func(theme string) { _ = config.SaveTheme(theme) }
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer cancel()
		_, err = tea.NewProgram(model, tea.WithContext(ctx), tea.WithAltScreen()).Run()
		return err
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print one snapshot as JSON instead of opening the terminal view")
	return cmd
}
