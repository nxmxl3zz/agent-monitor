package kiro

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// kiroConversation is the in-memory representation of one row of the
// conversations table that we were able to make sense of. Token totals are
// best-effort: Kiro CLI (and upstream Amazon Q Developer CLI) does not
// persist token counts directly — at best we recover an estimate from
// `context_usage_percentage × context_window_tokens` walked out of the
// stored JSON blob. Rows without any recoverable numeric signal are
// surfaced as session-count-only.
type kiroConversation struct {
	Key             string
	ConversationID  string
	Source          string
	Workspace       string
	Model           string
	UpdatedAt       time.Time
	InputTokens     int64
	OutputTokens    int64
	TotalTokens     int64
	HasTokens       bool
	MessageCount    int64
	HasMessageCount bool
}

// tableChoice records which conversations table we ended up reading and
// whether a conversation_id column is present. Kiro CLI ships with a
// renamed table (`conversations_v2`) while the upstream Amazon Q
// Developer CLI uses plain `conversations`; both are key/value JSON stores
// so we tolerate either at runtime rather than failing on a missing table.
type tableChoice struct {
	Name            string
	HasConversation bool // conversation_id column present
	HasValue        bool // value column present (required)
}

// detectConversationsTable inspects both candidate table names and reports
// the first one whose schema we can read. Returns an empty choice with
// ok=false when neither table is present.
func detectConversationsTable(ctx context.Context, db *sql.DB) (tableChoice, bool, error) {
	for _, name := range []string{"conversations_v2", "conversations"} {
		cols, err := readTableInfo(ctx, db, name)
		if err != nil {
			return tableChoice{}, false, err
		}
		if len(cols) == 0 {
			continue
		}
		choice := tableChoice{
			Name:            name,
			HasConversation: cols["conversation_id"],
			HasValue:        cols["value"],
		}
		if !choice.HasValue {
			// Table exists but doesn't have the value blob we need.
			continue
		}
		return choice, true, nil
	}
	return tableChoice{}, false, nil
}

func readTableInfo(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info("%s")`, table))
	if err != nil {
		return nil, fmt.Errorf("kiro: reading %s schema: %w", table, err)
	}
	defer rows.Close()

	present := make(map[string]bool)
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return nil, fmt.Errorf("kiro: scanning %s schema: %w", table, err)
		}
		present[strings.ToLower(strings.TrimSpace(name))] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("kiro: iterating %s schema: %w", table, err)
	}
	return present, nil
}

// queryKiroConversations enumerates the conversations table and parses the
// JSON value blob defensively. We don't fail on schema mismatch: any row we
// can't unmarshal is silently skipped, and the function returns whatever
// well-formed records it could extract.
//
// Schema confidence is LOW. The reference value blob structure varies
// across Kiro CLI releases and was reverse-engineered from a single
// recon snapshot; expect this parser to under-extract on newer or older
// schemas until field samples are collected from real installs.
func queryKiroConversations(ctx context.Context, dbPath string) ([]kiroConversation, error) {
	db, err := openReadOnly(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if err := pingContext(ctx, db); err != nil {
		return nil, err
	}

	choice, ok, err := detectConversationsTable(ctx, db)
	if err != nil {
		return nil, err
	}
	if !ok {
		// Neither table found. Treat as empty rather than error so the
		// dashboard shows "detected-but-quiet" rather than failing.
		return nil, nil
	}

	selectParts := []string{
		"key",
	}
	if choice.HasConversation {
		selectParts = append(selectParts, "conversation_id")
	} else {
		selectParts = append(selectParts, "NULL AS conversation_id")
	}
	selectParts = append(selectParts, "value")

	query := fmt.Sprintf(
		`SELECT %s FROM "%s" WHERE value IS NOT NULL AND TRIM(value) != ''`,
		strings.Join(selectParts, ", "),
		choice.Name,
	)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("kiro: querying %s: %w", choice.Name, err)
	}
	defer rows.Close()

	var out []kiroConversation
	for rows.Next() {
		var (
			key            string
			conversationID sql.NullString
			value          string
		)
		if err := rows.Scan(&key, &conversationID, &value); err != nil {
			return nil, fmt.Errorf("kiro: scanning row from %s: %w", choice.Name, err)
		}

		conv, ok := parseKiroValue(key, conversationID.String, value)
		if !ok {
			continue
		}
		out = append(out, conv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("kiro: iterating rows from %s: %w", choice.Name, err)
	}

	return out, nil
}

// kiroValueShape is a permissive view onto the conversations.value JSON
// blob. We unmarshal into a loose map first and walk it manually rather
// than committing to a brittle struct hierarchy; Kiro's schema is not yet
// stable enough to bind tightly.
type kiroValueShape struct {
	SessionID    string          `json:"session_id"`
	Cwd          string          `json:"cwd"`
	SessionState json.RawMessage `json:"session_state"`
	History      json.RawMessage `json:"history"`
	UpdatedAt    string          `json:"updated_at"`
}

// parseKiroValue extracts whatever we can from a single conversation row.
// Returns (conv, true) on success, (zero, false) when the row is too
// malformed to surface (no model, no tokens, no message count).
func parseKiroValue(key, conversationID, raw string) (kiroConversation, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return kiroConversation{}, false
	}

	var shape kiroValueShape
	if err := json.Unmarshal([]byte(raw), &shape); err != nil {
		// Not JSON we can read; surface the row as a session-only record
		// so it still contributes to the conversation count.
		return kiroConversation{
			Key:            key,
			ConversationID: conversationID,
			Source:         "sqlite",
		}, true
	}

	conv := kiroConversation{
		Key:            key,
		ConversationID: pickNonEmpty(conversationID, shape.SessionID),
		Source:         "sqlite",
		Workspace:      strings.TrimSpace(shape.Cwd),
	}

	// Model lives inside session_state.rts_model_state.model_info.model_id
	// per the recon snapshot. We walk through json.RawMessage so partial
	// unmarshal failures don't blow up the whole row.
	var contextWindow int64
	if len(shape.SessionState) > 0 {
		var sessionState map[string]json.RawMessage
		if err := json.Unmarshal(shape.SessionState, &sessionState); err == nil {
			conv.Model = extractModelFromSessionState(sessionState)
			contextWindow = extractContextWindowFromSessionState(sessionState)
			// Token estimation from user_turn_metadatas: we attempt to
			// extract explicit input_tokens/output_tokens first, then
			// estimate from context usage percentages where possible.
			input, output, total, hasTokens := extractTokensFromSessionState(sessionState)
			if hasTokens {
				conv.InputTokens = input
				conv.OutputTokens = output
				conv.TotalTokens = total
				conv.HasTokens = true
			} else if contextWindow > 0 {
				input, output, total, hasTokens = estimateTokensFromSessionState(sessionState, contextWindow)
				if hasTokens {
					conv.InputTokens = input
					conv.OutputTokens = output
					conv.TotalTokens = total
					conv.HasTokens = true
				}
			}
		}
	}

	if shape.UpdatedAt != "" {
		if ts, ok := parseTimestamp(shape.UpdatedAt); ok {
			conv.UpdatedAt = ts
		}
	}

	if len(shape.History) > 0 {
		// History is typically an array; len gives us a rough message count.
		var hist []json.RawMessage
		if err := json.Unmarshal(shape.History, &hist); err == nil {
			conv.MessageCount = int64(len(hist))
			conv.HasMessageCount = true
		}
		if !conv.HasTokens {
			input, output, total, hasTokens := extractTokensFromHistory(shape.History, contextWindow)
			if hasTokens {
				conv.InputTokens = input
				conv.OutputTokens = output
				conv.TotalTokens = total
				conv.HasTokens = true
			}
		}
	}

	return conv, true
}

// extractModelFromSessionState walks session_state.rts_model_state.model_info
// and returns the model_id string. Returns "" when any layer of the chain
// is missing or has the wrong type — we never panic on bad shapes.
func extractModelFromSessionState(sessionState map[string]json.RawMessage) string {
	rts, ok := sessionState["rts_model_state"]
	if !ok {
		return ""
	}
	var rtsMap map[string]json.RawMessage
	if err := json.Unmarshal(rts, &rtsMap); err != nil {
		return ""
	}
	info, ok := rtsMap["model_info"]
	if !ok {
		return ""
	}
	var infoMap map[string]any
	if err := json.Unmarshal(info, &infoMap); err != nil {
		return ""
	}
	if v, ok := infoMap["model_id"]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// extractContextWindowFromSessionState reads
// session_state.rts_model_state.model_info.context_window_tokens when present.
func extractContextWindowFromSessionState(sessionState map[string]json.RawMessage) int64 {
	info := extractModelInfoFromSessionState(sessionState)
	if info == nil {
		return 0
	}
	return readInt64(info, "context_window_tokens")
}

func extractModelInfoFromSessionState(sessionState map[string]json.RawMessage) map[string]any {
	rts, ok := sessionState["rts_model_state"]
	if !ok {
		return nil
	}
	var rtsMap map[string]json.RawMessage
	if err := json.Unmarshal(rts, &rtsMap); err != nil {
		return nil
	}
	info, ok := rtsMap["model_info"]
	if !ok {
		return nil
	}
	var infoMap map[string]any
	if err := json.Unmarshal(info, &infoMap); err != nil {
		return nil
	}
	return infoMap
}

// extractTokensFromSessionState reads session_state.conversation_metadata
// .user_turn_metadatas and sums explicit input_tokens/output_tokens
// fields. Returns (0, 0, 0, false) when no usable turn metadata exists.
func extractTokensFromSessionState(sessionState map[string]json.RawMessage) (int64, int64, int64, bool) {
	meta, ok := sessionState["conversation_metadata"]
	if !ok {
		return 0, 0, 0, false
	}
	var metaMap map[string]json.RawMessage
	if err := json.Unmarshal(meta, &metaMap); err != nil {
		return 0, 0, 0, false
	}
	turns, ok := metaMap["user_turn_metadatas"]
	if !ok {
		return 0, 0, 0, false
	}
	var turnList []map[string]any
	if err := json.Unmarshal(turns, &turnList); err != nil {
		return 0, 0, 0, false
	}

	var input, output int64
	anyFound := false
	for _, turn := range turnList {
		if v := readInt64(turn, "input_tokens"); v > 0 {
			input += v
			anyFound = true
		}
		if v := readInt64(turn, "output_tokens"); v > 0 {
			output += v
			anyFound = true
		}
	}
	if !anyFound {
		return 0, 0, 0, false
	}
	return input, output, input + output, true
}

// estimateTokensFromSessionState estimates input tokens from
// context_usage_percentage × context_window_tokens when explicit token counts
// are unavailable.
func estimateTokensFromSessionState(sessionState map[string]json.RawMessage, contextWindow int64) (int64, int64, int64, bool) {
	if contextWindow <= 0 {
		return 0, 0, 0, false
	}
	turns, ok := userTurnMetadata(sessionState)
	if !ok {
		return 0, 0, 0, false
	}
	var input int64
	for _, turn := range turns {
		pct := readFloat64(turn, "context_usage_percentage")
		if pct <= 0 {
			continue
		}
		estimated := int64(pct * float64(contextWindow))
		if estimated > 0 {
			input += estimated
		}
	}
	if input <= 0 {
		return 0, 0, 0, false
	}
	return input, 0, input, true
}

func userTurnMetadata(sessionState map[string]json.RawMessage) ([]map[string]any, bool) {
	meta, ok := sessionState["conversation_metadata"]
	if !ok {
		return nil, false
	}
	var metaMap map[string]json.RawMessage
	if err := json.Unmarshal(meta, &metaMap); err != nil {
		return nil, false
	}
	turns, ok := metaMap["user_turn_metadatas"]
	if !ok {
		return nil, false
	}
	var turnList []map[string]any
	if err := json.Unmarshal(turns, &turnList); err != nil {
		return nil, false
	}
	return turnList, true
}

func extractTokensFromHistory(raw json.RawMessage, contextWindow int64) (int64, int64, int64, bool) {
	if len(raw) == 0 {
		return 0, 0, 0, false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, 0, 0, false
	}
	var input, output int64
	walkKiroJSON(value, func(m map[string]any) {
		if v := readInt64(m, "input_tokens"); v > 0 {
			input += v
		}
		if v := readInt64(m, "output_tokens"); v > 0 {
			output += v
		}
		if contextWindow > 0 && readInt64(m, "input_tokens") == 0 {
			if pct := readFloat64(m, "context_usage_percentage"); pct > 0 {
				input += int64(pct * float64(contextWindow))
			}
		}
		if readInt64(m, "output_tokens") == 0 {
			if responseSize := readInt64(m, "response_size"); responseSize > 0 {
				output += estimateTokensFromChars(responseSize)
			}
		}
	})
	if input == 0 && output == 0 {
		return 0, 0, 0, false
	}
	return input, output, input + output, true
}

func walkKiroJSON(v any, visit func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		visit(x)
		for _, child := range x {
			walkKiroJSON(child, visit)
		}
	case []any:
		for _, child := range x {
			walkKiroJSON(child, visit)
		}
	}
}

// readInt64 pulls a numeric field out of a map[string]any, accepting both
// float64 (the default JSON-number decoding) and int64 (in case a custom
// decoder is used upstream). Returns 0 on any type mismatch.
func readInt64(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		if n < 0 {
			return 0
		}
		return int64(n)
	case int64:
		if n < 0 {
			return 0
		}
		return n
	case int:
		if n < 0 {
			return 0
		}
		return int64(n)
	}
	return 0
}

func readFloat64(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		if n < 0 {
			return 0
		}
		return n
	case int64:
		if n < 0 {
			return 0
		}
		return float64(n)
	case int:
		if n < 0 {
			return 0
		}
		return float64(n)
	}
	return 0
}

func pickNonEmpty(a, b string) string {
	a = strings.TrimSpace(a)
	if a != "" {
		return a
	}
	return strings.TrimSpace(b)
}

// parseTimestamp accepts RFC3339 (the most likely format), the SQLite
// datetime layout, and a date-only fallback.
func parseTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func estimateTokensFromChars(chars int64) int64 {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}
