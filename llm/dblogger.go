/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package llm

import "database/sql"

// DBLLMLogger writes LLM log entries to a site's SQLite database.
type DBLLMLogger struct {
	db *sql.DB
}

// NewDBLLMLogger creates a logger that writes to the given site database.
func NewDBLLMLogger(siteDB *sql.DB) *DBLLMLogger {
	return &DBLLMLogger{db: siteDB}
}

// LogLLMCall inserts a log entry asynchronously (fire-and-forget).
func (l *DBLLMLogger) LogLLMCall(entry LLMLogEntry) {
	go func() {
		_, _ = l.db.Exec(
			`INSERT INTO llm_log (
				source, session_id, iteration, model, provider_type,
				request_messages, request_system, request_tools, request_max_tokens,
				response_content, response_tool_calls, response_stop_reason,
				input_tokens, output_tokens, duration_ms, error_message
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			entry.Source, entry.SessionID, entry.Iteration,
			entry.Model, entry.ProviderType,
			entry.RequestMessages, entry.RequestSystem,
			entry.RequestTools, entry.RequestMaxTokens,
			entry.ResponseContent, entry.ResponseToolCalls, entry.ResponseStopReason,
			entry.InputTokens, entry.OutputTokens,
			entry.DurationMs, nullIfEmpty(entry.ErrorMessage),
		)
	}()
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
