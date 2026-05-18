// Package kiro implements a local-data provider for Kiro CLI, the renamed
// Amazon Q Developer CLI.
//
// Status: EXPERIMENTAL. Schema confidence is LOW.
//
// The provider reads data.sqlite3 (at the platform-specific Kiro CLI data
// directory) and extracts conversation rows from `conversations_v2` or its
// legacy `conversations` predecessor. Both tables are simple key/value
// stores: a path-like key and a JSON blob value. Token counts are NOT
// persisted by upstream — at best they are recoverable from
// `user_turn_metadatas` inside the JSON blob when explicit
// input_tokens/output_tokens fields are present.
//
// We make no network calls, require no authentication, and read the
// database in read-only/immutable mode so we never block Kiro CLI itself.
// Missing or unreadable rows are skipped silently rather than failing the
// whole snapshot.
//
// macOS path: ~/Library/Application Support/kiro-cli/data.sqlite3
// Linux path: ~/.local/share/kiro-cli/data.sqlite3 (XDG default)
// Windows:    unpublished — users can override via the KIRO_DATA_DIR env var.
//
// If you have a Kiro CLI install and the parser under-reports, please file
// an issue with a copy of your `data.sqlite3` schema (the output of
// `sqlite3 data.sqlite3 '.schema'` is sufficient) so we can tighten the
// extraction.
package kiro

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/janekbaraniewski/openusage/internal/core"
	"github.com/janekbaraniewski/openusage/internal/providers/providerbase"
	"github.com/janekbaraniewski/openusage/internal/providers/shared"
)

// ID is the canonical provider identifier registered in the providers
// registry. Exported so external packages can reference it without
// stringly-typed coupling.
const ID = "kiro_cli"

// DefaultAccountID is the account ID used by the auto-detector when it
// registers a local install.
const DefaultAccountID = "kiro-cli"

const allTimeWindow = "all-time"

// Provider is a thin wrapper around providerbase.Base. Bulk of the work
// happens in Fetch.
type Provider struct {
	providerbase.Base
	clock core.Clock
}

// New constructs a Kiro provider with sensible widget defaults.
func New() *Provider {
	return &Provider{
		Base: providerbase.New(core.ProviderSpec{
			ID: ID,
			Info: core.ProviderInfo{
				Name:         "Kiro CLI",
				Capabilities: []string{"local_stats", "session_tracking", "experimental"},
				DocURL:       "https://github.com/aws/amazon-q-developer-cli",
			},
			Auth: core.ProviderAuthSpec{
				Type:             core.ProviderAuthTypeLocal,
				DefaultAccountID: DefaultAccountID,
			},
			Setup: core.ProviderSetupSpec{
				Quickstart: []string{
					"Install Kiro CLI (the renamed Amazon Q Developer CLI) and run at least one chat session.",
					"openusage auto-detects data.sqlite3 in the Kiro CLI data directory; no configuration required.",
					"Token estimation is experimental — please file an issue with your data.sqlite3 .schema output if values look wrong.",
				},
			},
			Dashboard: dashboardWidget(),
		}),
		clock: core.SystemClock{},
	}
}

// DetailWidget returns the standard coding-tool detail layout.
func (p *Provider) DetailWidget() core.DetailWidget {
	return detailWidget()
}

func (p *Provider) now() time.Time {
	if p != nil && p.clock != nil {
		return p.clock.Now()
	}
	return time.Now()
}

// HasChanged reports whether data.sqlite3 has been modified since the
// given time.
func (p *Provider) HasChanged(acct core.AccountConfig, since time.Time) (bool, error) {
	dbPath := resolveDBPath(acct)
	sessionsDir := resolveSessionsDir(acct)
	paths := make([]string, 0, 2)
	if dbPath != "" {
		paths = append(paths, dbPath)
	}
	if sessionsDir != "" {
		paths = append(paths, sessionsDir)
	}
	if len(paths) == 0 {
		return false, nil
	}
	return shared.AnyPathModifiedAfter(paths, since), nil
}

// Fetch reads Kiro file sessions and data.sqlite3 (if present) and produces a
// UsageSnapshot.
//
// Missing data sources are not an error: we return a StatusUnknown snapshot
// with an empty metrics map and a friendly message so the dashboard shows the
// provider as detected-but-quiet rather than failing.
func (p *Provider) Fetch(ctx context.Context, acct core.AccountConfig) (core.UsageSnapshot, error) {
	if strings.TrimSpace(acct.Provider) == "" {
		acct.Provider = p.ID()
	}

	snap := core.NewUsageSnapshot(p.ID(), acct.ID)
	snap.Timestamp = p.now()
	snap.DailySeries = make(map[string][]core.TimePoint)
	snap.SetDiagnostic("schema_confidence", "experimental")

	dbPath := resolveDBPath(acct)
	sessionsDir := resolveSessionsDir(acct)
	if dbPath == "" && sessionsDir == "" {
		snap.Status = core.StatusUnknown
		snap.Message = "Kiro CLI sessions not found"
		return snap, nil
	}
	if sessionsDir != "" {
		snap.Raw["sessions_dir"] = sessionsDir
	}
	if dbPath != "" {
		snap.Raw["db_path"] = dbPath
	}

	var (
		fileConversations []kiroConversation
		dbConversations   []kiroConversation
		errs              []error
	)

	if sessionsDir != "" {
		conversations, err := readKiroFileSessions(ctx, sessionsDir)
		if err != nil {
			snap.SetDiagnostic("sessions_error", err.Error())
			errs = append(errs, err)
		} else {
			fileConversations = conversations
		}
	}
	if dbPath != "" {
		conversations, err := queryKiroConversations(ctx, dbPath)
		if err != nil {
			snap.SetDiagnostic("query_error", err.Error())
			errs = append(errs, err)
		} else {
			dbConversations = conversations
		}
	}

	conversations := mergeKiroConversations(fileConversations, dbConversations)
	if len(conversations) == 0 && len(errs) > 0 {
		snap.Status = core.StatusError
		snap.Message = "Failed to read Kiro CLI local data"
		return snap, errors.Join(errs...)
	}

	if len(conversations) == 0 {
		snap.Status = core.StatusOK
		snap.Message = "No Kiro CLI conversations recorded"
		return snap, nil
	}

	populateSnapshot(&snap, conversations, p.now())
	snap.Status = core.StatusOK
	snap.Message = buildStatusMessage(snap)
	return snap, nil
}

func mergeKiroConversations(groups ...[]kiroConversation) []kiroConversation {
	merged := make(map[string]kiroConversation)
	order := make([]string, 0)
	for _, group := range groups {
		for _, conv := range group {
			key := kiroConversationKey(conv)
			if key == "" {
				key = fmt.Sprintf("%s:%d", conv.Source, len(order))
			}
			existing, ok := merged[key]
			if !ok {
				merged[key] = conv
				order = append(order, key)
				continue
			}
			merged[key] = mergeKiroConversation(existing, conv)
		}
	}
	out := make([]kiroConversation, 0, len(order))
	for _, key := range order {
		out = append(out, merged[key])
	}
	return out
}

func kiroConversationKey(conv kiroConversation) string {
	if id := strings.TrimSpace(conv.ConversationID); id != "" {
		return "id:" + id
	}
	if key := strings.TrimSpace(conv.Key); key != "" {
		return "key:" + key
	}
	return ""
}

func mergeKiroConversation(primary, secondary kiroConversation) kiroConversation {
	out := primary
	if out.Model == "" {
		out.Model = secondary.Model
	}
	if out.Workspace == "" {
		out.Workspace = secondary.Workspace
	}
	if out.UpdatedAt.IsZero() || (!secondary.UpdatedAt.IsZero() && secondary.UpdatedAt.After(out.UpdatedAt)) {
		out.UpdatedAt = secondary.UpdatedAt
	}
	if !out.HasTokens && secondary.HasTokens {
		out.InputTokens = secondary.InputTokens
		out.OutputTokens = secondary.OutputTokens
		out.TotalTokens = secondary.TotalTokens
		out.HasTokens = true
	}
	if !out.HasMessageCount && secondary.HasMessageCount {
		out.MessageCount = secondary.MessageCount
		out.HasMessageCount = true
	}
	if secondary.Source != "" && !strings.Contains(out.Source, secondary.Source) {
		if out.Source == "" {
			out.Source = secondary.Source
		} else {
			out.Source += "+" + secondary.Source
		}
	}
	return out
}

// populateSnapshot aggregates per-conversation records into snapshot
// metrics, per-model usage records, and daily series. Pure for ease of
// testing.
func populateSnapshot(snap *core.UsageSnapshot, conversations []kiroConversation, now time.Time) {
	type modelTotals struct {
		input         int64
		output        int64
		total         int64
		conversations int64
		messages      int64
		hasMessages   bool
		source        string
		workspace     string
	}

	perModel := make(map[string]*modelTotals)

	var (
		totalInput     int64
		totalOutput    int64
		totalTotal     int64
		totalMessages  int64
		hasAnyMessages bool
		withTokens     int64
	)

	tokensByDay := make(map[string]float64)
	convsByDay := make(map[string]float64)

	for _, c := range conversations {
		bucketKey := c.Model
		if strings.TrimSpace(bucketKey) == "" {
			bucketKey = "unknown"
		}
		bucket, ok := perModel[bucketKey]
		if !ok {
			bucket = &modelTotals{}
			perModel[bucketKey] = bucket
		}
		bucket.conversations++
		if c.HasTokens {
			bucket.input += c.InputTokens
			bucket.output += c.OutputTokens
			bucket.total += c.TotalTokens
			totalInput += c.InputTokens
			totalOutput += c.OutputTokens
			totalTotal += c.TotalTokens
			withTokens++
		}
		if c.HasMessageCount {
			bucket.messages += c.MessageCount
			bucket.hasMessages = true
			totalMessages += c.MessageCount
			hasAnyMessages = true
		}
		if bucket.source == "" && c.Source != "" {
			bucket.source = c.Source
		}
		if bucket.workspace == "" && c.Workspace != "" {
			bucket.workspace = c.Workspace
		}

		if !c.UpdatedAt.IsZero() {
			day := c.UpdatedAt.UTC().Format("2006-01-02")
			convsByDay[day]++
			if c.HasTokens {
				tokensByDay[day] += float64(c.TotalTokens)
			}
		}
	}

	setUsedMetric(snap, "total_conversations", float64(len(conversations)), "conversations", allTimeWindow)
	setUsedMetric(snap, "conversations_with_tokens", float64(withTokens), "conversations", allTimeWindow)
	setUsedMetric(snap, "total_tokens", float64(totalTotal), "tokens", allTimeWindow)
	setUsedMetric(snap, "total_input_tokens", float64(totalInput), "tokens", allTimeWindow)
	setUsedMetric(snap, "total_output_tokens", float64(totalOutput), "tokens", allTimeWindow)
	if hasAnyMessages {
		setUsedMetric(snap, "total_messages", float64(totalMessages), "messages", allTimeWindow)
	}

	if len(convsByDay) > 0 {
		snap.DailySeries["conversations"] = core.SortedTimePoints(convsByDay)
	}
	if len(tokensByDay) > 0 {
		snap.DailySeries["tokens"] = core.SortedTimePoints(tokensByDay)
	}

	for model, bucket := range perModel {
		rec := core.ModelUsageRecord{
			RawModelID: model,
			RawSource:  pickNonEmpty(bucket.source, "local"),
			Window:     allTimeWindow,
			Requests:   core.Float64Ptr(float64(bucket.conversations)),
		}
		if bucket.total > 0 {
			rec.InputTokens = core.Float64Ptr(float64(bucket.input))
			rec.OutputTokens = core.Float64Ptr(float64(bucket.output))
			rec.TotalTokens = core.Float64Ptr(float64(bucket.total))
		}
		if bucket.hasMessages {
			rec.SetDimension("message_count", fmt.Sprintf("%d", bucket.messages))
		}
		if bucket.workspace != "" {
			rec.SetDimension("workspace", bucket.workspace)
		}
		snap.AppendModelUsage(rec)
	}

	_ = now
}

// buildStatusMessage produces the short human-readable summary shown in
// the dashboard message line.
func buildStatusMessage(snap core.UsageSnapshot) string {
	parts := make([]string, 0, 3)
	if m, ok := snap.Metrics["total_conversations"]; ok && m.Used != nil && *m.Used > 0 {
		parts = append(parts, formatCount(*m.Used, "conversation"))
	}
	if m, ok := snap.Metrics["total_tokens"]; ok && m.Used != nil && *m.Used > 0 {
		parts = append(parts, shared.FormatTokenCount(int(*m.Used))+" tokens (est.)")
	}
	if len(parts) == 0 {
		return "OK"
	}
	return strings.Join(parts, ", ")
}

func setUsedMetric(snap *core.UsageSnapshot, key string, value float64, unit, window string) {
	if value <= 0 {
		return
	}
	v := value
	snap.Metrics[key] = core.Metric{
		Used:   &v,
		Unit:   unit,
		Window: window,
	}
}

func formatCount(v float64, noun string) string {
	if v == 1 {
		return "1 " + noun
	}
	return shared.FormatTokenCount(int(v)) + " " + noun + "s"
}
