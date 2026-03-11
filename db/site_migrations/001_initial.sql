-- Site-level schema: all per-site tables (no site_id columns).
-- Each site gets its own SQLite database at data/sites/{id}/site.db.

-- Pages and versioning
CREATE TABLE IF NOT EXISTS pages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL UNIQUE,
    title TEXT,
    content TEXT,
    template TEXT,
    status TEXT DEFAULT 'published',
    metadata TEXT DEFAULT '{}',
    layout TEXT DEFAULT NULL,
    assets TEXT DEFAULT NULL,
    is_deleted BOOLEAN DEFAULT 0,
    deleted_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS page_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    page_id INTEGER NOT NULL,
    path TEXT NOT NULL,
    title TEXT,
    content TEXT,
    template TEXT,
    status TEXT,
    metadata TEXT,
    version_number INTEGER NOT NULL,
    changed_by TEXT NOT NULL DEFAULT 'brain',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_page_versions ON page_versions(page_id, version_number DESC);
CREATE INDEX IF NOT EXISTS idx_page_versions_date ON page_versions(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_page_versions_page ON page_versions(page_id);

-- Brain-created assets (CSS, JS, images)
CREATE TABLE IF NOT EXISTS assets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    filename TEXT NOT NULL UNIQUE,
    content_type TEXT,
    size INTEGER,
    storage_path TEXT NOT NULL,
    alt_text TEXT,
    scope TEXT NOT NULL DEFAULT 'global',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- User-uploaded files
CREATE TABLE IF NOT EXISTS files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    filename TEXT NOT NULL UNIQUE,
    content_type TEXT,
    size INTEGER,
    storage_path TEXT NOT NULL,
    description TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_files_date ON files(created_at DESC);

-- Brain event log
CREATE TABLE IF NOT EXISTS brain_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    summary TEXT,
    details TEXT,
    tokens_used INTEGER DEFAULT 0,
    model TEXT,
    duration_ms INTEGER,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_brain_log_date ON brain_log(created_at);
CREATE INDEX IF NOT EXISTS idx_brain_log_event ON brain_log(event_type);

-- Key-value memory store
CREATE TABLE IF NOT EXISTS memory (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key TEXT NOT NULL UNIQUE,
    value TEXT NOT NULL,
    category TEXT DEFAULT 'general',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Chat messages (brain + user sessions)
CREATE TABLE IF NOT EXISTS chat_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    tool_calls TEXT,
    tool_call_id TEXT,
    metadata TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_chat_session ON chat_messages(session_id, created_at);

-- Questions and answers (human-in-the-loop)
CREATE TABLE IF NOT EXISTS questions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    question TEXT NOT NULL,
    context TEXT,
    options TEXT,
    urgency TEXT DEFAULT 'normal',
    status TEXT DEFAULT 'pending',
    type TEXT DEFAULT 'text',
    secret_name TEXT,
    fields TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_questions_status ON questions(status);

CREATE TABLE IF NOT EXISTS answers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    question_id INTEGER NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    answer TEXT NOT NULL,
    answered_by INTEGER,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Dynamic tables registry (LLM-created schemas)
CREATE TABLE IF NOT EXISTS dynamic_tables (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    table_name TEXT NOT NULL UNIQUE,
    schema_def TEXT NOT NULL,
    secure_columns TEXT DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- API endpoints
CREATE TABLE IF NOT EXISTS api_endpoints (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL UNIQUE,
    table_name TEXT NOT NULL,
    methods TEXT DEFAULT '["GET","POST"]',
    public_columns TEXT,
    requires_auth BOOLEAN DEFAULT 0,
    public_read BOOLEAN DEFAULT 0,
    required_role TEXT DEFAULT NULL,
    rate_limit INTEGER DEFAULT 60,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Auth endpoints
CREATE TABLE IF NOT EXISTS auth_endpoints (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    table_name TEXT NOT NULL,
    path TEXT NOT NULL UNIQUE,
    username_column TEXT NOT NULL DEFAULT 'email',
    password_column TEXT NOT NULL DEFAULT 'password',
    public_columns TEXT DEFAULT '[]',
    jwt_expiry_hours INTEGER DEFAULT 24,
    default_role TEXT NOT NULL DEFAULT 'user',
    role_column TEXT NOT NULL DEFAULT 'role',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- OAuth providers (social login)
CREATE TABLE IF NOT EXISTS oauth_providers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    client_id TEXT NOT NULL,
    client_secret_name TEXT NOT NULL,
    authorize_url TEXT NOT NULL,
    token_url TEXT NOT NULL,
    userinfo_url TEXT NOT NULL,
    scopes TEXT NOT NULL DEFAULT 'openid email profile',
    username_field TEXT NOT NULL DEFAULT 'email',
    auth_endpoint_path TEXT NOT NULL,
    is_enabled BOOLEAN DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Webhooks
CREATE TABLE IF NOT EXISTS webhooks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    secret TEXT,
    url TEXT,
    direction TEXT DEFAULT 'incoming',
    is_enabled BOOLEAN DEFAULT 1,
    last_triggered DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS webhook_subscriptions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    webhook_id INTEGER NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(webhook_id, event_type)
);

CREATE TABLE IF NOT EXISTS webhook_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    webhook_id INTEGER NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    direction TEXT NOT NULL,
    event_type TEXT,
    payload TEXT,
    status_code INTEGER,
    response TEXT,
    success BOOLEAN DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_webhook_logs_date ON webhook_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_subs_webhook ON webhook_subscriptions(webhook_id);

-- Analytics (page views)
CREATE TABLE IF NOT EXISTS analytics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    page_path TEXT NOT NULL,
    visitor_hash TEXT,
    referrer TEXT,
    user_agent TEXT,
    country TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_analytics_date ON analytics(created_at);
CREATE INDEX IF NOT EXISTS idx_analytics_page_path ON analytics(page_path, created_at DESC);

-- Secrets (encrypted key-value)
CREATE TABLE IF NOT EXISTS secrets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    value_encrypted TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Scheduled tasks
CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT,
    cron_expression TEXT,
    interval_seconds INTEGER,
    prompt TEXT,
    is_enabled BOOLEAN DEFAULT 1,
    last_run DATETIME,
    next_run DATETIME,
    run_count INTEGER DEFAULT 0,
    error_count INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS task_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id INTEGER NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    result TEXT,
    error_message TEXT,
    started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_task_runs_task ON task_runs(task_id, started_at DESC);

-- Activity log
CREATE TABLE IF NOT EXISTS activity_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    summary TEXT,
    details TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_activity_log_date ON activity_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_activity_log_type ON activity_log(event_type, created_at DESC);

-- Approval rules
CREATE TABLE IF NOT EXISTS approval_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tool_name TEXT NOT NULL UNIQUE,
    requires_approval BOOLEAN DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Site snapshots
CREATE TABLE IF NOT EXISTS site_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    snapshot_data TEXT NOT NULL,
    created_by TEXT NOT NULL DEFAULT 'brain',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Service providers (external API integrations)
CREATE TABLE IF NOT EXISTS service_providers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    base_url TEXT NOT NULL,
    auth_type TEXT DEFAULT 'bearer',
    auth_header TEXT DEFAULT 'Authorization',
    auth_prefix TEXT DEFAULT 'Bearer',
    secret_name TEXT,
    description TEXT DEFAULT '',
    api_docs TEXT DEFAULT '',
    is_enabled BOOLEAN DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Detailed LLM request/response log
CREATE TABLE IF NOT EXISTS llm_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    iteration INTEGER NOT NULL DEFAULT 0,
    model TEXT NOT NULL,
    provider_type TEXT NOT NULL DEFAULT '',
    request_messages TEXT,
    request_system TEXT,
    request_tools TEXT,
    request_max_tokens INTEGER,
    response_content TEXT,
    response_tool_calls TEXT,
    response_stop_reason TEXT,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    error_message TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_llm_log_date ON llm_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_llm_log_source ON llm_log(source, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_llm_log_session ON llm_log(session_id, created_at DESC);

-- Server-side layout system
CREATE TABLE IF NOT EXISTS layouts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    head_content TEXT DEFAULT '',
    body_before_main TEXT DEFAULT '',
    body_after_main TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Pipeline state: singleton row tracking current build progress
CREATE TABLE IF NOT EXISTS pipeline_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    stage TEXT NOT NULL DEFAULT 'ANALYZE',
    plan_json TEXT,
    blueprint_json TEXT,
    update_description TEXT,
    current_page_index INTEGER DEFAULT 0,
    error_count INTEGER DEFAULT 0,
    last_error TEXT,
    paused BOOLEAN DEFAULT 0,
    pause_reason TEXT,
    started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO pipeline_state (id) VALUES (1);

-- Stage execution log
CREATE TABLE IF NOT EXISTS stage_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    stage TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'started',
    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0,
    tool_calls INTEGER DEFAULT 0,
    duration_ms INTEGER DEFAULT 0,
    error_message TEXT,
    started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_stage_log_stage ON stage_log(stage, started_at DESC);

-- File version history for text assets (CSS, JS, SVG, etc.)
CREATE TABLE IF NOT EXISTS file_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    storage_type TEXT NOT NULL,
    filename TEXT NOT NULL,
    content TEXT NOT NULL,
    content_type TEXT,
    size INTEGER,
    version_number INTEGER NOT NULL,
    changed_by TEXT NOT NULL DEFAULT 'brain',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_file_versions_lookup
    ON file_versions (storage_type, filename, version_number DESC);

-- Upload endpoints (file upload routes)
CREATE TABLE IF NOT EXISTS upload_endpoints (
    id INTEGER PRIMARY KEY,
    path TEXT UNIQUE NOT NULL,
    allowed_types TEXT,
    max_size_mb INTEGER DEFAULT 5,
    requires_auth BOOLEAN DEFAULT 0,
    table_name TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- SSE stream endpoints
CREATE TABLE IF NOT EXISTS stream_endpoints (
    id INTEGER PRIMARY KEY,
    path TEXT UNIQUE NOT NULL,
    event_types TEXT,
    requires_auth BOOLEAN DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- WebSocket endpoints
CREATE TABLE IF NOT EXISTS ws_endpoints (
    id INTEGER PRIMARY KEY,
    path TEXT UNIQUE NOT NULL,
    event_types TEXT,
    receive_event_type TEXT DEFAULT 'ws.message',
    write_to_table TEXT DEFAULT '',
    requires_auth BOOLEAN DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
