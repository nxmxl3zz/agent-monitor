package telemetry

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/janekbaraniewski/openusage/internal/core"
)

const SessionEvidenceTTL = 2 * time.Minute

type SessionRoot struct {
	ProviderID string
	Path       string
}

type sessionFile struct {
	info    os.FileInfo
	offset  int64
	row     core.SessionActivity
	pending map[string]string
}

// SessionTracker tails complete records only; it never retains prompts, tool
// arguments, responses, or credentials. Accounting stays in the canonical store.
type SessionTracker struct {
	mu    sync.Mutex
	files map[string]*sessionFile
}

func NewSessionTracker() *SessionTracker {
	return &SessionTracker{files: make(map[string]*sessionFile)}
}

func (t *SessionTracker) Snapshot(ctx context.Context, roots []SessionRoot, now, since time.Time) ([]core.SessionActivity, []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	seen := make(map[string]bool)
	rows := make(map[string]core.SessionActivity)
	var warnings []string
	for _, root := range roots {
		err := filepath.WalkDir(root.Path, func(path string, entry fs.DirEntry, walkErr error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !since.IsZero() && info.ModTime().Before(since) {
				return nil
			}
			seen[path] = true
			file := t.files[path]
			if file == nil || info.Size() < file.offset || !os.SameFile(file.info, info) ||
				(info.Size() == file.offset && !info.ModTime().Equal(file.info.ModTime())) {
				file = &sessionFile{row: core.SessionActivity{ProviderID: root.ProviderID, State: core.SessionUnknown, Source: "local_jsonl"}, pending: make(map[string]string)}
				if root.ProviderID == "claude_code" {
					file.row.SessionID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
					if filepath.Base(filepath.Dir(path)) == "subagents" {
						file.row.ParentSessionID = filepath.Base(filepath.Dir(filepath.Dir(path)))
					}
				}
				t.files[path] = file
			}
			if file.offset < info.Size() {
				if err := file.read(ctx, path); err != nil {
					warnings = append(warnings, root.ProviderID+": a session was only partially read")
				}
			}
			file.info = info
			row := file.row
			if row.SessionID == "" || row.LastActivityAt.IsZero() || (!since.IsZero() && row.LastActivityAt.Before(since)) {
				return nil
			}
			row.ObservedAt = now
			if (row.State == core.SessionWorking || row.State == core.SessionWaiting) && now.Sub(row.LastActivityAt) > SessionEvidenceTTL {
				row.State, row.ActiveTool, row.StateReason = core.SessionUnknown, "", "no recent events; process not confirmed"
			}
			key := row.ProviderID + ":" + row.SessionID
			if old, ok := rows[key]; !ok || row.LastActivityAt.After(old.LastActivityAt) {
				rows[key] = row
			}
			return nil
		})
		if err != nil {
			warnings = append(warnings, root.ProviderID+": some logs could not be read")
		}
	}
	for path := range t.files {
		if !seen[path] {
			delete(t.files, path)
		}
	}
	out := make([]core.SessionActivity, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActivityAt.After(out[j].LastActivityAt) })
	if len(out) > 200 {
		out = out[:200]
		warnings = append(warnings, "showing the 200 most recent sessions")
	}
	return out, warnings
}

func (f *sessionFile) read(ctx context.Context, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(f.offset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(file)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var line []byte
		for {
			part, readErr := reader.ReadSlice('\n')
			if len(line)+len(part) > 32*1024*1024 {
				return fmt.Errorf("session record exceeds limit")
			}
			line = append(line, part...)
			if readErr == bufio.ErrBufferFull {
				continue
			}
			if readErr == io.EOF {
				return nil
			} // Retry incomplete records next time.
			if readErr != nil {
				return readErr
			}
			break
		}
		f.offset += int64(len(line))
		f.apply(line)
	}
}

type activityRecord struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	Timestamp string          `json:"timestamp"`
	SessionID string          `json:"sessionId"`
	CWD       string          `json:"cwd"`
	IsMeta    bool            `json:"isMeta"`
	Payload   json.RawMessage `json:"payload"`
	Params    struct {
		Model string `json:"model"`
	} `json:"params"`
	Message struct {
		Model      string          `json:"model"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

func (f *sessionFile) apply(line []byte) {
	var r activityRecord
	if json.Unmarshal(line, &r) != nil {
		return
	}
	if r.SessionID != "" {
		f.row.SessionID = r.SessionID
	}
	if r.CWD != "" {
		f.row.Project = r.CWD
	}
	var p struct {
		Type           string          `json:"type"`
		ID             string          `json:"id"`
		SessionID      string          `json:"session_id"`
		Parent         string          `json:"parent_thread_id"`
		CWD            string          `json:"cwd"`
		Model          string          `json:"model"`
		Name           string          `json:"name"`
		CallID         string          `json:"call_id"`
		Source         json.RawMessage `json:"source"`
		ThreadSettings struct {
			Model string `json:"model"`
		} `json:"thread_settings"`
	}
	_ = json.Unmarshal(r.Payload, &p)
	if r.Type == "session_meta" {
		var src struct {
			Subagent struct {
				ThreadSpawn struct {
					Parent string `json:"parent_thread_id"`
				} `json:"thread_spawn"`
			} `json:"subagent"`
		}
		_ = json.Unmarshal(p.Source, &src)
		if p.Parent == "" {
			p.Parent = src.Subagent.ThreadSpawn.Parent
		}
		f.row.SessionID = p.SessionID
		if f.row.SessionID == "" {
			f.row.SessionID = p.ID
		}
		f.row.Project, f.row.ParentSessionID, f.row.Model = p.CWD, p.Parent, p.Model
		return
	}
	if p.Model != "" {
		f.row.Model = p.Model
	}
	if p.ThreadSettings.Model != "" {
		f.row.Model = p.ThreadSettings.Model
	}
	if r.Message.Model != "" {
		f.row.Model = r.Message.Model
	}
	if r.Params.Model != "" {
		f.row.Model = r.Params.Model
	}
	ts, err := time.Parse(time.RFC3339Nano, r.Timestamp)
	if err != nil || ts.Before(f.row.LastActivityAt) {
		return
	}
	state, reason, relevant := f.row.State, f.row.StateReason, false
	if f.row.ProviderID == "codex" {
		switch {
		case r.Type == "event_msg" && (p.Type == "task_started" || p.Type == "turn_started"):
			f.pending = make(map[string]string)
			state, reason, relevant = core.SessionWorking, "inicio de turno", true
		case r.Type == "event_msg" && (p.Type == "task_complete" || p.Type == "task_completed" || p.Type == "turn_complete"):
			f.pending = make(map[string]string)
			state, reason, relevant = core.SessionFinished, "turno finalizado", true
		case r.Type == "event_msg" && p.Type == "turn_aborted":
			f.pending = make(map[string]string)
			state, reason, relevant = core.SessionInterrupted, "turno cancelado", true
		case r.Type == "response_item" && (p.Type == "function_call" || p.Type == "custom_tool_call"):
			f.toolStart(p.CallID, p.Name)
			state, reason, relevant = core.SessionWorking, "llamada a herramienta", true
			if strings.HasSuffix(p.Name, "request_user_input") {
				state, reason = core.SessionWaiting, "waiting for a user reply"
			}
		case r.Type == "response_item" && (p.Type == "function_call_output" || p.Type == "custom_tool_call_output"):
			delete(f.pending, p.CallID)
			state, reason, relevant = core.SessionWorking, "resultado de herramienta recibido", true
		}
	} else {
		var blocks []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Name      string `json:"name"`
			ToolUseID string `json:"tool_use_id"`
		}
		_ = json.Unmarshal(r.Message.Content, &blocks)
		switch r.Type {
		case "api-request":
			state, reason, relevant = core.SessionWorking, "solicitud al modelo", true
		case "user":
			if !r.IsMeta {
				state, reason, relevant = core.SessionWorking, "client input", true
				toolResult := false
				for _, b := range blocks {
					if b.Type == "tool_result" {
						toolResult = true
						delete(f.pending, b.ToolUseID)
					}
				}
				if !toolResult {
					f.pending = make(map[string]string)
				}
			}
		case "assistant":
			state, reason, relevant = core.SessionWorking, "model response", true
			for _, b := range blocks {
				if b.Type == "tool_use" {
					f.toolStart(b.ID, b.Name)
					if b.Name == "AskUserQuestion" {
						state, reason = core.SessionWaiting, "waiting for a user reply"
					}
				}
			}
			if r.Message.StopReason == "end_turn" || r.Message.StopReason == "stop_sequence" {
				f.pending = make(map[string]string)
				state, reason = core.SessionFinished, "turno finalizado"
			}
		case "system":
			if r.Subtype == "turn_duration" {
				f.pending = make(map[string]string)
				state, reason, relevant = core.SessionFinished, "turno finalizado", true
			}
		}
	}
	if relevant {
		f.row.State, f.row.StateReason, f.row.LastActivityAt = state, reason, ts
		var names []string
		for _, name := range f.pending {
			names = append(names, name)
		}
		sort.Strings(names)
		f.row.ActiveTool = strings.Join(names, ", ")
	}
}

func (f *sessionFile) toolStart(id, name string) {
	if name != "" {
		f.row.LastTool = name
		f.pending[id] = name
	}
}

// ApplySessionTokens reuses the same logical-event deduplication as account
// analytics, so streamed fragments and hook/file duplicates are counted once.
func ApplySessionTokens(ctx context.Context, db *sql.DB, rows []core.SessionActivity, since time.Time) error {
	if len(rows) == 0 {
		return nil
	}
	cte, args := dedupedUsageCTE(usageFilter{ProviderIDs: []string{"codex", "claude_code", "anthropic"}, Since: since})
	query := cte + ` SELECT source_system, session_id, SUM(COALESCE(total_tokens, 0))
	 FROM deduped_usage WHERE event_type='message_usage' AND session_id IS NOT NULL
	 GROUP BY source_system, session_id`
	result, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("session usage: %w", err)
	}
	defer result.Close()
	totals := make(map[string]int64)
	for result.Next() {
		var provider, id string
		var total float64
		if err := result.Scan(&provider, &id, &total); err != nil {
			return err
		}
		totals[provider+":"+id] = int64(total)
	}
	if err := result.Err(); err != nil {
		return err
	}
	for i := range rows {
		if total, ok := totals[rows[i].ProviderID+":"+rows[i].SessionID]; ok {
			rows[i].TotalTokens, rows[i].TokensKnown = total, true
		}
	}
	return nil
}
