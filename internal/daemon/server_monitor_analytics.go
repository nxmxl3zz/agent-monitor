package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/janekbaraniewski/openusage/internal/core"
	"github.com/janekbaraniewski/openusage/internal/telemetry"
)

func (s *Service) handleMonitorAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	period := r.URL.Query().Get("range")
	if core.MonitorRangeDuration(period) == 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid range")
		return
	}
	frame, err := telemetry.ReadMonitorAnalytics(r.Context(), s.store.DB(), period, s.now())
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "usage charts unavailable")
		return
	}
	writeJSON(w, http.StatusOK, frame)
}

func (c *Client) MonitorAnalytics(ctx context.Context, period string) (core.MonitorAnalytics, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/v1/monitor-analytics?range="+period, nil)
	if err != nil {
		return core.MonitorAnalytics{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return core.MonitorAnalytics{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return core.MonitorAnalytics{}, fmt.Errorf("monitor analytics HTTP %d", resp.StatusCode)
	}
	var frame core.MonitorAnalytics
	err = json.NewDecoder(resp.Body).Decode(&frame)
	return frame, err
}
