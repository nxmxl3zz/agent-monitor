package kiro

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type kiroHeader struct {
	SessionID           string
	Cwd                 string
	Model               string
	ContextWindowTokens int64
	TurnMetadatas       []kiroTurnMetadata
	UpdatedAt           time.Time
}

type kiroTurnMetadata struct {
	InputTokens            int64
	OutputTokens           int64
	HasInputTokens         bool
	HasOutputTokens        bool
	ContextUsagePercentage float64
	RequestStart           time.Time
	RequestEnd             time.Time
}

type kiroMessageEvent struct {
	MessageID    string
	Timestamp    time.Time
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	HasTokens    bool
}

// parseKiroSessionHeader reads the small JSON header file from
// ~/.kiro/sessions/cli/<session>.json.
func parseKiroSessionHeader(path string) (*kiroHeader, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("kiro: reading %s: %w", path, err)
	}

	var shape kiroValueShape
	if err := json.Unmarshal(data, &shape); err != nil {
		// Malformed session headers are non-fatal.
		return nil, nil
	}

	header := &kiroHeader{
		SessionID: strings.TrimSpace(shape.SessionID),
		Cwd:       strings.TrimSpace(shape.Cwd),
	}
	if header.SessionID == "" {
		header.SessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if shape.UpdatedAt != "" {
		if ts, ok := parseTimestamp(shape.UpdatedAt); ok {
			header.UpdatedAt = ts
		}
	}

	if len(shape.SessionState) > 0 {
		var sessionState map[string]json.RawMessage
		if err := json.Unmarshal(shape.SessionState, &sessionState); err == nil {
			header.Model = extractModelFromSessionState(sessionState)
			header.ContextWindowTokens = extractContextWindowFromSessionState(sessionState)
			header.TurnMetadatas = extractTurnMetadata(sessionState)
		}
	}

	return header, nil
}

func extractTurnMetadata(sessionState map[string]json.RawMessage) []kiroTurnMetadata {
	rawTurns, ok := userTurnMetadata(sessionState)
	if !ok {
		return nil
	}
	out := make([]kiroTurnMetadata, 0, len(rawTurns))
	for _, raw := range rawTurns {
		input, hasInput := readOptionalInt64(raw, "input_tokens", "inputTokens")
		output, hasOutput := readOptionalInt64(raw, "output_tokens", "outputTokens")
		turn := kiroTurnMetadata{
			InputTokens:            input,
			OutputTokens:           output,
			HasInputTokens:         hasInput,
			HasOutputTokens:        hasOutput,
			ContextUsagePercentage: readFloat64Any(raw, "context_usage_percentage", "contextUsagePercentage"),
		}
		if ts, ok := parseTimestamp(readStringAny(raw, "request_end_time", "requestEndTime")); ok {
			turn.RequestEnd = ts
		}
		if ts, ok := parseTimestamp(readStringAny(raw, "request_start_time", "requestStartTime")); ok {
			turn.RequestStart = ts
		}
		out = append(out, turn)
	}
	return out
}

// parseKiroSessionJSONL reads the companion JSONL transcript and emits one
// event per assistant message, deduplicated by message_id.
func parseKiroSessionJSONL(jsonlPath string, header *kiroHeader) ([]kiroMessageEvent, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("kiro: opening %s: %w", jsonlPath, err)
	}
	defer f.Close()

	type jsonlLine struct {
		Kind      string          `json:"kind"`
		Data      json.RawMessage `json:"data"`
		Timestamp string          `json:"timestamp"`
	}

	eventsByID := make(map[string]kiroMessageEvent)
	order := make([]string, 0)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	lineNo := 0
	assistantIndex := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry jsonlLine
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if !strings.EqualFold(entry.Kind, "AssistantMessage") {
			continue
		}

		var data map[string]any
		if err := json.Unmarshal(entry.Data, &data); err != nil {
			continue
		}

		messageID := pickNonEmpty(
			readStringAny(data, "message_id", "messageId"),
			readStringAny(data, "id"),
		)
		if messageID == "" {
			messageID = fmt.Sprintf("%s:%d", jsonlPath, lineNo)
		}

		event := kiroMessageEvent{MessageID: messageID}
		if ts, ok := parseTimestamp(readStringAny(data, "timestamp")); ok {
			event.Timestamp = ts
		} else if ts, ok := parseTimestamp(entry.Timestamp); ok {
			event.Timestamp = ts
		}

		var metadata map[string]any
		if raw, ok := data["metadata"].(map[string]any); ok {
			metadata = raw
		} else {
			metadata = map[string]any{}
		}

		turn := kiroTurnMetadata{}
		if header != nil && assistantIndex < len(header.TurnMetadatas) {
			turn = header.TurnMetadatas[assistantIndex]
		}
		assistantIndex++

		contentChars := int64(textLength(data["content"]))
		input, output, hasTokens := estimateEventTokens(header, turn, metadata, contentChars)
		if hasTokens {
			event.InputTokens = input
			event.OutputTokens = output
			event.TotalTokens = input + output
			event.HasTokens = true
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = pickTime(turn.RequestEnd, turn.RequestStart)
		}

		if _, ok := eventsByID[messageID]; !ok {
			order = append(order, messageID)
		}
		// Last duplicate wins; streaming transcripts often rewrite richer
		// metadata for the same message ID later in the file.
		eventsByID[messageID] = event
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("kiro: scanning %s: %w", jsonlPath, err)
	}

	events := make([]kiroMessageEvent, 0, len(order))
	for _, id := range order {
		events = append(events, eventsByID[id])
	}
	return events, nil
}

func estimateEventTokens(header *kiroHeader, turn kiroTurnMetadata, metadata map[string]any, contentChars int64) (int64, int64, bool) {
	var input, output int64
	hasTokens := false

	if turn.HasInputTokens && turn.InputTokens > 0 {
		input = turn.InputTokens
		hasTokens = true
	}
	if turn.HasOutputTokens && turn.OutputTokens > 0 {
		output = turn.OutputTokens
		hasTokens = true
	}
	if input == 0 && header != nil && header.ContextWindowTokens > 0 {
		pct := turn.ContextUsagePercentage
		if pct <= 0 {
			pct = readFloat64Any(metadata, "context_usage_percentage", "contextUsagePercentage")
		}
		if pct > 0 {
			input = int64(pct * float64(header.ContextWindowTokens))
			hasTokens = hasTokens || input > 0
		}
	}
	if output == 0 {
		if responseSize := readInt64Any(metadata, "response_size", "responseSize"); responseSize > 0 {
			output = estimateTokensFromChars(responseSize)
			hasTokens = true
		} else if contentChars > 0 {
			output = estimateTokensFromChars(contentChars)
			hasTokens = true
		}
	}

	return input, output, hasTokens
}

func parseKiroSession(headerPath string) (*kiroConversation, error) {
	header, err := parseKiroSessionHeader(headerPath)
	if err != nil || header == nil {
		return nil, err
	}

	jsonlPath := strings.TrimSuffix(headerPath, filepath.Ext(headerPath)) + ".jsonl"
	events, err := parseKiroSessionJSONL(jsonlPath, header)
	if err != nil {
		return nil, err
	}

	conv := &kiroConversation{
		Key:            headerPath,
		ConversationID: header.SessionID,
		Source:         "jsonl",
		Workspace:      header.Cwd,
		Model:          header.Model,
		UpdatedAt:      header.UpdatedAt,
	}

	if len(events) > 0 {
		conv.MessageCount = int64(len(events))
		conv.HasMessageCount = true
		for _, event := range events {
			if !event.Timestamp.IsZero() && event.Timestamp.After(conv.UpdatedAt) {
				conv.UpdatedAt = event.Timestamp
			}
			if event.HasTokens {
				conv.InputTokens += event.InputTokens
				conv.OutputTokens += event.OutputTokens
				conv.TotalTokens += event.TotalTokens
				conv.HasTokens = true
			}
		}
		return conv, nil
	}

	input, output, total, hasTokens := tokensFromTurnMetadata(header)
	if hasTokens {
		conv.InputTokens = input
		conv.OutputTokens = output
		conv.TotalTokens = total
		conv.HasTokens = true
	}
	if len(header.TurnMetadatas) > 0 {
		conv.MessageCount = int64(len(header.TurnMetadatas))
		conv.HasMessageCount = true
		for _, turn := range header.TurnMetadatas {
			ts := pickTime(turn.RequestEnd, turn.RequestStart)
			if !ts.IsZero() && ts.After(conv.UpdatedAt) {
				conv.UpdatedAt = ts
			}
		}
	}
	if conv.UpdatedAt.IsZero() {
		if info, statErr := os.Stat(headerPath); statErr == nil {
			conv.UpdatedAt = info.ModTime().UTC()
		}
	}
	return conv, nil
}

func tokensFromTurnMetadata(header *kiroHeader) (int64, int64, int64, bool) {
	if header == nil {
		return 0, 0, 0, false
	}
	var input, output int64
	hasTokens := false
	for _, turn := range header.TurnMetadatas {
		if turn.HasInputTokens && turn.InputTokens > 0 {
			input += turn.InputTokens
			hasTokens = true
		} else if header.ContextWindowTokens > 0 && turn.ContextUsagePercentage > 0 {
			input += int64(turn.ContextUsagePercentage * float64(header.ContextWindowTokens))
			hasTokens = true
		}
		if turn.HasOutputTokens && turn.OutputTokens > 0 {
			output += turn.OutputTokens
			hasTokens = true
		}
	}
	if !hasTokens {
		return 0, 0, 0, false
	}
	return input, output, input + output, true
}

func readKiroFileSessions(ctx context.Context, dir string) ([]kiroConversation, error) {
	var out []kiroConversation
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".json" || strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		conv, perFileErr := parseKiroSession(path)
		if perFileErr != nil || conv == nil {
			return nil
		}
		out = append(out, *conv)
		return nil
	})
	if walkErr != nil {
		return out, walkErr
	}
	return out, nil
}

func readOptionalInt64(m map[string]any, keys ...string) (int64, bool) {
	for _, key := range keys {
		v, ok := m[key]
		if !ok {
			continue
		}
		switch n := v.(type) {
		case float64:
			if n < 0 {
				return 0, true
			}
			return int64(n), true
		case int64:
			if n < 0 {
				return 0, true
			}
			return n, true
		case int:
			if n < 0 {
				return 0, true
			}
			return int64(n), true
		}
	}
	return 0, false
}

func readInt64Any(m map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if v := readInt64(m, key); v > 0 {
			return v
		}
	}
	return 0
}

func readFloat64Any(m map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if v := readFloat64(m, key); v > 0 {
			return v
		}
	}
	return 0
}

func readStringAny(m map[string]any, keys ...string) string {
	for _, key := range keys {
		v, ok := m[key]
		if !ok {
			continue
		}
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func textLength(v any) int {
	switch x := v.(type) {
	case string:
		return len(x)
	case []any:
		total := 0
		for _, item := range x {
			total += textLength(item)
		}
		return total
	case map[string]any:
		total := 0
		if s, ok := x["text"].(string); ok {
			total += len(s)
		}
		if s, ok := x["content"].(string); ok {
			total += len(s)
		}
		for key, child := range x {
			if key == "text" || key == "content" {
				continue
			}
			total += textLength(child)
		}
		return total
	default:
		return 0
	}
}

func pickTime(times ...time.Time) time.Time {
	for _, ts := range times {
		if !ts.IsZero() {
			return ts
		}
	}
	return time.Time{}
}
