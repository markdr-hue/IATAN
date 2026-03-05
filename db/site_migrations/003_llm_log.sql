-- Detailed LLM request/response log.
-- Stores full payloads for every LLM API call (brain ticks + chat sessions).
CREATE TABLE IF NOT EXISTS llm_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,                    -- 'brain' or 'chat'
    session_id TEXT NOT NULL DEFAULT '',     -- 'brain' for ticks, chat session UUID for chat
    iteration INTEGER NOT NULL DEFAULT 0,   -- loop iteration (0-based) within tick/session
    model TEXT NOT NULL,
    provider_type TEXT NOT NULL DEFAULT '',  -- 'anthropic', 'openai'

    -- Request payload
    request_messages TEXT,                   -- JSON: full messages array sent
    request_system TEXT,                     -- system prompt sent
    request_tools TEXT,                      -- JSON: tool names only (not full schemas)
    request_max_tokens INTEGER,

    -- Response payload
    response_content TEXT,                   -- LLM text response
    response_tool_calls TEXT,                -- JSON: tool calls returned
    response_stop_reason TEXT,               -- end_turn, tool_use, max_tokens, etc.

    -- Token accounting (split)
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,

    -- Timing
    duration_ms INTEGER NOT NULL DEFAULT 0,

    -- Error tracking
    error_message TEXT,                      -- non-null if the call failed

    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_llm_log_date ON llm_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_llm_log_source ON llm_log(source, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_llm_log_session ON llm_log(session_id, created_at DESC);
