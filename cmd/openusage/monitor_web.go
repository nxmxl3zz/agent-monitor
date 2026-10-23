package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/janekbaraniewski/openusage/internal/config"
	"github.com/janekbaraniewski/openusage/internal/core"
	"github.com/janekbaraniewski/openusage/internal/daemon"
	"github.com/janekbaraniewski/openusage/internal/monitorweb"
	"github.com/spf13/cobra"
)

func newMonitorWebCommand() *cobra.Command {
	var addr string
	var hosts []string
	cmd := &cobra.Command{Use: "web", Short: "Serve the mobile monitor on loopback for a private HTTPS proxy", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		request, err := daemon.BuildReadModelRequestFromConfig()
		if err != nil {
			return err
		}
		client := daemon.NewClient(daemon.ResolveSocketPath())
		load := func(ctx context.Context, window core.TimeWindow) (monitorweb.View, error) {
			frame, err := client.Sessions(ctx, window)
			if err != nil {
				return monitorweb.View{}, err
			}
			req := request
			req.TimeWindow = window
			snapshots, quotaErr := client.ReadModel(ctx, req)
			view := monitorweb.BuildView(frame, snapshots)
			if quotaErr != nil {
				view.Warnings = append(view.Warnings, "Quotas temporarily unavailable.")
			}
			return view, nil
		}
		server, err := monitorweb.NewServer(addr, hosts, core.ParseTimeWindow(cfg.Data.TimeWindow), load)
		if err != nil {
			return err
		}
		server.SetDashboardLoader(func(ctx context.Context, period string) (monitorweb.Dashboard, error) {
			window := core.TimeWindow3d
			if period == "7d" {
				window = core.TimeWindow7d
			} else if period == "30d" {
				window = core.TimeWindow30d
			}
			view, err := load(ctx, window)
			if err != nil {
				return monitorweb.Dashboard{}, err
			}
			analytics, err := client.MonitorAnalytics(ctx, period)
			if err != nil {
				return monitorweb.Dashboard{}, err
			}
			return monitorweb.BuildDashboard(view, analytics), nil
		})
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer cancel()
		fmt.Fprintf(cmd.OutOrStdout(), "Agent Monitor web: http://%s\n", addr)
		return server.Run(ctx)
	}}
	cmd.Flags().StringVar(&addr, "listen", "127.0.0.1:8787", "loopback IP and port")
	cmd.Flags().StringSliceVar(&hosts, "allow-host", nil, "additional exact Host values accepted through the authenticated proxy")
	return cmd
}
