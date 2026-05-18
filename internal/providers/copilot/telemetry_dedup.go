package copilot

import (
	"strings"

	"github.com/janekbaraniewski/openusage/internal/providers/shared"
)

// OTel-style record priorities for tool-call dedup. When the same operation
// (keyed by ToolCallID) emits multiple records, the highest-priority record
// wins. Lower-priority duplicates are suppressed to avoid double-counting.
//
// Priority order (highest first):
//
//	response  — tool.execution_complete (richest: result, status, error)
//	request   — assistant.message.tool_request (tool args, message linkage)
//	metric    — tool.execution_start (start timestamp / args)
//	error     — explicit error-only records (lowest)
//
// This mirrors the OTel JSONL convention used by GitHub Copilot's
// COPILOT_OTEL_ENABLED export, where request/response/error/metric records
// share an operation id.
const (
	copilotOTelPriorityNone     = 0
	copilotOTelPriorityError    = 1
	copilotOTelPriorityMetric   = 2
	copilotOTelPriorityRequest  = 3
	copilotOTelPriorityResponse = 4
)

// copilotOTelRecordPriority returns the dedup priority for an event based on
// its payload "event" tag. Returns 0 when the event does not participate in
// tool-call dedup.
func copilotOTelRecordPriority(ev shared.TelemetryEvent) int {
	if ev.EventType != shared.TelemetryEventTypeToolUsage {
		return copilotOTelPriorityNone
	}
	if strings.TrimSpace(ev.ToolCallID) == "" {
		return copilotOTelPriorityNone
	}
	eventTag, _ := ev.Payload["event"].(string)
	switch strings.TrimSpace(eventTag) {
	case "tool.execution_complete":
		if ev.Status == shared.TelemetryStatusError || ev.Status == shared.TelemetryStatusAborted {
			// Completion records still win over request/metric peers — they
			// carry the most ground-truth fields — but if a future emitter
			// produces a separate error-only record we want it to lose to a
			// successful response.
			return copilotOTelPriorityResponse
		}
		return copilotOTelPriorityResponse
	case "assistant.message.tool_request":
		return copilotOTelPriorityRequest
	case "tool.execution_start":
		return copilotOTelPriorityMetric
	}
	return copilotOTelPriorityNone
}

// dedupCopilotOTelToolEvents collapses multiple tool-call records that share a
// ToolCallID to the highest-priority record, preserving event order. Events
// that do not participate in dedup (non-tool-usage, no tool_call_id, or
// unknown event tag) pass through untouched.
func dedupCopilotOTelToolEvents(events []shared.TelemetryEvent) []shared.TelemetryEvent {
	if len(events) < 2 {
		return events
	}
	bestIndex := make(map[string]int, len(events))
	bestPriority := make(map[string]int, len(events))
	for i, ev := range events {
		prio := copilotOTelRecordPriority(ev)
		if prio == copilotOTelPriorityNone {
			continue
		}
		key := ev.ToolCallID
		if existing, ok := bestPriority[key]; !ok || prio > existing {
			bestIndex[key] = i
			bestPriority[key] = prio
		}
	}
	if len(bestIndex) == 0 {
		return events
	}
	out := make([]shared.TelemetryEvent, 0, len(events))
	for i, ev := range events {
		prio := copilotOTelRecordPriority(ev)
		if prio == copilotOTelPriorityNone {
			out = append(out, ev)
			continue
		}
		if bestIndex[ev.ToolCallID] == i {
			out = append(out, ev)
		}
	}
	return out
}
