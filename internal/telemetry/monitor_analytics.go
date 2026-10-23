package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/janekbaraniewski/openusage/internal/core"
)

func ReadMonitorAnalytics(ctx context.Context, db *sql.DB, period string, now time.Time) (core.MonitorAnalytics, error) {
	duration := core.MonitorRangeDuration(period)
	if duration == 0 {
		return core.MonitorAnalytics{}, fmt.Errorf("monitor analytics: invalid range")
	}
	step := int64(60)
	if duration > time.Hour {
		step = 300
	}
	if duration > 6*time.Hour {
		step = 900
	}
	if duration > 24*time.Hour {
		step = 7200
	}
	if duration > 7*24*time.Hour {
		step = 21600
	}
	frame := core.MonitorAnalytics{Range: period, Since: now.Add(-duration), Until: now, BucketSeconds: step, Points: []core.MonitorUsagePoint{}}
	where, args := usageWhereClause("e", usageFilter{ProviderIDs: []string{"codex", "claude_code", "anthropic"}})
	where += " AND julianday(e.occurred_at) >= julianday(?) AND julianday(e.occurred_at) <= julianday(?)"
	args = append(args, frame.Since.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano))
	cte, args := dedupedUsageCTEWhere(where, args)
	// Codex input already includes cached input; Claude reports uncached input
	// separately. Expose mutually exclusive categories while preserving total_tokens.
	query := cte + `SELECT (unixepoch(occurred_at) / ?) * ?, source_system,
		COALESCE(NULLIF(model_canonical, ''), NULLIF(model_raw, ''), 'Unknown model'),
		SUM(CASE WHEN source_system='codex' THEN MAX(0, COALESCE(input_tokens,0)-COALESCE(cache_read_tokens,0)) ELSE COALESCE(input_tokens,0) END),
		SUM(COALESCE(output_tokens,0)), SUM(COALESCE(cache_read_tokens,0)), SUM(COALESCE(cache_write_tokens,0)),
		SUM(COALESCE(total_tokens,0)), SUM(COALESCE(requests,1))
		FROM deduped_usage WHERE event_type='message_usage' AND source_system IN ('codex','claude_code')
		AND LOWER(COALESCE(NULLIF(model_canonical,''),model_raw,'')) NOT LIKE 'deepseek%'
		AND COALESCE(NULLIF(model_canonical,''),model_raw,'') != '<synthetic>'
		GROUP BY 1,2,3 ORDER BY 1,2,3`
	args = append(args, step, step)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return frame, fmt.Errorf("monitor analytics: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var point core.MonitorUsagePoint
		if err := rows.Scan(&point.At, &point.Provider, &point.Model, &point.Input, &point.Output, &point.CacheRead, &point.CacheWrite, &point.Total, &point.Requests); err != nil {
			return frame, err
		}
		frame.Points = append(frame.Points, point)
	}
	return frame, rows.Err()
}
