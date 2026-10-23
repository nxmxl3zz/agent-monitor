package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/janekbaraniewski/openusage/internal/core"
)

func TestMonitorAnalyticsCanonicalAccountingAndScope(t *testing.T) {
	_, db, store := openUsageViewRawTestStore(t)
	now := time.Date(2026, 10, 22, 18, 0, 0, 0, time.UTC)
	ctx := context.Background()
	request := IngestRequest{SourceSystem: "claude_code", SourceChannel: SourceChannelJSONL, OccurredAt: now.Add(-30 * time.Minute), ProviderID: "anthropic", AgentName: "claude_code", SessionID: "s", MessageID: "m", EventType: EventTypeMessageUsage, ModelRaw: "claude-opus", TokenUsage: core.TokenUsage{InputTokens: int64Ptr(10), OutputTokens: int64Ptr(20), CacheReadTokens: int64Ptr(60), CacheWriteTokens: int64Ptr(10), TotalTokens: int64Ptr(100), Requests: int64Ptr(1)}}
	ingest := func(r IngestRequest) {
		t.Helper()
		if _, err := store.Ingest(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	ingest(request)
	request.SourceChannel = SourceChannelHook
	ingest(request)
	request.SourceSystem, request.ProviderID, request.AgentName = "codex", "codex", "codex"
	request.SessionID, request.TurnID, request.MessageID, request.ModelRaw = "codex-session", "turn", "codex-message", "gpt-codex"
	request.TokenUsage = core.TokenUsage{InputTokens: int64Ptr(80), OutputTokens: int64Ptr(20), CacheReadTokens: int64Ptr(60), TotalTokens: int64Ptr(100), Requests: int64Ptr(1)}
	ingest(request)
	for _, item := range []struct {
		model string
		at    time.Time
	}{{"deepseek-v4", now.Add(-time.Minute)}, {"<synthetic>", now.Add(-time.Minute)}, {"old-model", now.Add(-2 * time.Hour)}, {"future-model", now.Add(time.Minute)}} {
		request.MessageID = item.model
		request.TurnID = item.model
		request.SessionID = item.model
		request.ModelRaw = item.model
		request.OccurredAt = item.at
		request.SourceChannel = SourceChannelJSONL
		ingest(request)
	}
	frame, err := ReadMonitorAnalytics(ctx, db, "1h", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.Points) != 2 || frame.BucketSeconds != 60 || !frame.Since.Equal(now.Add(-time.Hour)) {
		t.Fatalf("incorrect scope: %+v", frame)
	}
	var total, requests int64
	for _, point := range frame.Points {
		total += point.Total
		requests += point.Requests
		if point.Input+point.Output+point.CacheRead+point.CacheWrite != point.Total {
			t.Fatalf("cache counted twice: %+v", point)
		}
		if point.Provider == "codex" && point.Input != 20 {
			t.Fatalf("codex cached input not normalized: %+v", point)
		}
	}
	if total != 200 || requests != 2 {
		t.Fatalf("logical duplicates included: total=%d requests=%d", total, requests)
	}
	if _, err := ReadMonitorAnalytics(ctx, db, "invalid", now); err == nil {
		t.Fatal("invalid range accepted")
	}
}

func TestMonitorAnalyticsEmptyRangesRemainBounded(t *testing.T) {
	_, db, _ := openUsageViewRawTestStore(t)
	for _, period := range []string{"1h", "6h", "24h", "7d", "30d"} {
		frame, err := ReadMonitorAnalytics(context.Background(), db, period, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if frame.Points == nil || len(frame.Points) != 0 || int64(frame.Until.Sub(frame.Since).Seconds())/frame.BucketSeconds > 125 {
			t.Fatalf("unbounded or non-empty frame: %+v", frame)
		}
	}
}
