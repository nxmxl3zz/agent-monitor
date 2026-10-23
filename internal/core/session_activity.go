package core

import "time"

type SessionState string

const (
	SessionWorking     SessionState = "working"
	SessionWaiting     SessionState = "waiting"
	SessionFinished    SessionState = "finished"
	SessionInterrupted SessionState = "interrupted"
	SessionUnknown     SessionState = "unknown"
)

// SessionActivity reports observed turn state, not operating-system liveness.
// Tokens belong to the requested reporting window and include cached input once.
type SessionActivity struct {
	ProviderID      string       `json:"provider_id"`
	SessionID       string       `json:"session_id"`
	ParentSessionID string       `json:"parent_session_id,omitempty"`
	Project         string       `json:"project,omitempty"`
	Model           string       `json:"model,omitempty"`
	State           SessionState `json:"state"`
	StateReason     string       `json:"state_reason"`
	LastActivityAt  time.Time    `json:"last_activity_at"`
	ObservedAt      time.Time    `json:"observed_at"`
	LastTool        string       `json:"last_tool,omitempty"`
	ActiveTool      string       `json:"active_tool,omitempty"`
	TotalTokens     int64        `json:"total_tokens"`
	TokensKnown     bool         `json:"tokens_known"`
	Source          string       `json:"source"`
}

type SessionActivityFrame struct {
	Sessions  []SessionActivity `json:"sessions"`
	UpdatedAt time.Time         `json:"updated_at"`
	Window    TimeWindow        `json:"window"`
	Warnings  []string          `json:"warnings,omitempty"`
}
