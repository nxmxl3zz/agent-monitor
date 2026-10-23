package monitorweb

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/janekbaraniewski/openusage/internal/core"
)

func TestBuildViewLimitsFields(t *testing.T) {
	now := time.Now()
	used := 82.5
	frame := core.SessionActivityFrame{UpdatedAt: now, Window: core.TimeWindow1d, Warnings: []string{"private/path error"}, Sessions: []core.SessionActivity{
		{ProviderID: "codex", SessionID: "session", Project: "/private/project", Model: "model\x1b[31m", State: core.SessionWorking, LastActivityAt: now, ActiveTool: "Read", Source: "/private/session.jsonl", StateReason: "private reason", TotalTokens: 42, TokensKnown: true},
		{ProviderID: "claude_code", SessionID: "old", State: core.SessionWaiting, LastActivityAt: now.Add(-3 * time.Minute)},
		{ProviderID: "deepseek", SessionID: "excluded"},
	}}
	snapshot := core.UsageSnapshot{ProviderID: "claude_code", Status: core.StatusOK, Timestamp: now, Metrics: map[string]core.Metric{"usage_five_hour": {Used: &used}}, Resets: map[string]time.Time{"usage_five_hour": now.Add(time.Hour)}, Raw: map[string]string{"token": "private token"}, Diagnostics: map[string]string{"debug": "private diagnostic"}, Attributes: map[string]string{"email": "private email"}, Message: "private message"}
	view := BuildView(frame, map[string]core.UsageSnapshot{"claude": snapshot})
	if len(view.Sessions) != 2 || view.Sessions[0].Project != "project" || view.Sessions[0].Model != "model" || view.Sessions[0].Tokens == nil || *view.Sessions[0].Tokens != 42 || view.Sessions[1].Tokens != nil || view.Sessions[1].State != core.SessionUnknown {
		t.Fatalf("unexpected sessions: %+v", view.Sessions)
	}
	quota := view.Providers[1].Quotas[0]
	if quota.UsedPercent == nil || *quota.UsedPercent != used || quota.ResetsAt == nil || view.Providers[1].Quotas[1].UsedPercent != nil {
		t.Fatalf("unexpected quota: %+v", quota)
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "deepseek") {
		t.Fatalf("private data in response: %s", data)
	}
}

func TestBuildViewQuotaValues(t *testing.T) {
	for _, used := range []float64{0, 125, -10, math.NaN(), math.Inf(1)} {
		view := BuildView(core.SessionActivityFrame{}, map[string]core.UsageSnapshot{"codex": {ProviderID: "codex", Metrics: map[string]core.Metric{"rate_limit_primary": {Used: &used}}}})
		got := view.Providers[0].Quotas[0].UsedPercent
		if math.IsNaN(used) || math.IsInf(used, 0) {
			if got != nil {
				t.Fatalf("non-finite quota: %v", got)
			}
		} else if got == nil || *got != min(100.0, max(0.0, used)) {
			t.Fatalf("incorrect quota for %v: %v", used, got)
		}
	}
}

func TestBuildViewQuotaErrorDetail(t *testing.T) {
	for reason, detail := range map[string]string{"rate_limited": "rate_limited", "unavailable": ""} {
		snapshot := core.UsageSnapshot{ProviderID: "claude_code", Status: core.StatusOK, Diagnostics: map[string]string{"usage_api_error": reason}}
		provider := BuildView(core.SessionActivityFrame{}, map[string]core.UsageSnapshot{"claude": snapshot}).Providers[1]
		if provider.Status != core.StatusError || provider.Detail != detail {
			t.Fatalf("%s: got status %s detail %q", reason, provider.Status, provider.Detail)
		}
	}
}

func TestServerAccessBoundaries(t *testing.T) {
	for _, addr := range []string{":8787", "0.0.0.0:8787", "100.64.0.1:8787", "localhost:8787", "invalid"} {
		if _, err := NewServer(addr, nil, core.TimeWindow1d, nil); err == nil {
			t.Fatalf("accepted non-loopback listen %q", addr)
		}
	}
	for _, host := range []string{"*", "https://monitor.example", "user@monitor.example", "monitor.example/path", "monitor.example?x=1", "bad host", "monitor.example\\bad"} {
		if _, err := NewServer("127.0.0.1:8787", []string{host}, core.TimeWindow1d, nil); err == nil {
			t.Fatalf("accepted host %q", host)
		}
	}
	calls := 0
	server, err := NewServer("127.0.0.1:8787", []string{"pc.example.ts.net"}, core.TimeWindow1d, func(_ context.Context, window core.TimeWindow) (View, error) {
		calls++
		return View{Window: window}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, host, origin, site, path string
		want                                   int
	}{
		{"local", "GET", "127.0.0.1:8787", "", "", "/api/snapshot", 200},
		{"private proxy", "GET", "pc.example.ts.net", "https://pc.example.ts.net", "same-origin", "/api/snapshot?window=7d", 200},
		{"unlisted host", "GET", "evil.example", "", "", "/api/snapshot", 403},
		{"wrong port", "GET", "127.0.0.1:9999", "", "", "/api/snapshot", 403},
		{"cross origin", "GET", "127.0.0.1:8787", "https://evil.example", "", "/api/snapshot", 403},
		{"null origin", "GET", "127.0.0.1:8787", "null", "", "/api/snapshot", 403},
		{"cross site", "GET", "127.0.0.1:8787", "", "cross-site", "/api/snapshot", 403},
		{"no writes", "POST", "127.0.0.1:8787", "", "", "/api/snapshot", 405},
		{"bad window", "GET", "127.0.0.1:8787", "", "", "/api/snapshot?window=wrong", 400},
		{"not a proxy", "GET", "127.0.0.1:8787", "", "", "/v1/hook/codex", 404},
		{"no file access", "GET", "127.0.0.1:8787", "", "", "/../../.claude/.credentials.json", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			req := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Sec-Fetch-Site", tc.site)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			if response.Code != tc.want {
				t.Fatalf("got %d: %s", response.Code, response.Body.String())
			}
			if tc.want != 200 && calls != before {
				t.Fatal("rejected request called collector")
			}
			if response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("missing browser isolation headers")
			}
		})
	}
}

func TestServerAssetsAndCollectorFailure(t *testing.T) {
	server, err := NewServer("127.0.0.1:8787", nil, core.TimeWindow1d, func(context.Context, core.TimeWindow) (View, error) {
		return View{}, errors.New("private error with credential path")
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/app.css", "/app.js", "/charts.js", "/inter.woff2", "/favicon.svg", "/api/snapshot"} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest("GET", "http://127.0.0.1:8787"+path, nil))
		want := http.StatusOK
		if path == "/api/snapshot" {
			want = http.StatusServiceUnavailable
		}
		if recorder.Code != want || recorder.Body.Len() == 0 || strings.Contains(recorder.Body.String(), "private error") {
			t.Fatalf("%s: %d %s", path, recorder.Code, recorder.Body.String())
		}
		recorder = httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest("HEAD", "http://127.0.0.1:8787"+path, nil))
		if recorder.Code != want || recorder.Body.Len() != 0 {
			t.Fatalf("HEAD %s: %d body size %d", path, recorder.Code, recorder.Body.Len())
		}
	}
}

func TestDashboardRangeAndReadOnlyProjection(t *testing.T) {
	server, err := NewServer("127.0.0.1:8787", nil, core.TimeWindow1d, nil)
	if err != nil {
		t.Fatal(err)
	}
	server.SetDashboardLoader(func(_ context.Context, period string) (Dashboard, error) {
		if period == "30d" {
			return Dashboard{}, errors.New("private database error")
		}
		view := View{Providers: []Provider{}, Sessions: []Session{{ID: "private-session", Project: "private-project"}}, Warnings: []string{sessionsWarning, "Quotas temporarily unavailable."}}
		return BuildDashboard(view, core.MonitorAnalytics{Range: period, Points: []core.MonitorUsagePoint{{Model: "gpt\x1b[31m"}}}), nil
	})
	for _, tc := range []struct {
		method, period string
		code           int
	}{{"GET", "1h", 200}, {"GET", "6h", 200}, {"GET", "24h", 200}, {"GET", "7d", 200}, {"GET", "30d", 503}, {"GET", "all", 400}, {"GET", "", 400}, {"POST", "1h", 405}} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(tc.method, "http://127.0.0.1:8787/api/dashboard?range="+tc.period, nil))
		if response.Code != tc.code || strings.Contains(response.Body.String(), "private database") {
			t.Fatalf("%s %s: %d %s", tc.method, tc.period, response.Code, response.Body.String())
		}
		if tc.code == 200 && strings.Contains(response.Body.String(), "001b") {
			t.Fatal("terminal escape sequence leaked into chart model")
		}
		if tc.code == 200 {
			var body map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if _, ok := body["sessions"]; ok || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "Some session") {
				t.Fatalf("session data leaked into dashboard: %s", response.Body.String())
			}
			if !strings.Contains(string(body["warnings"]), "Quotas") {
				t.Fatalf("quota warning dropped: %s", body["warnings"])
			}
		}
	}
}
