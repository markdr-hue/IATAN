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

-- Brain-created assets (CSS, JS, images)
CREATE TABLE IF NOT EXISTS assets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    filename TEXT NOT NULL UNIQUE,
    content_type TEXT,
    size INTEGER,
    storage_path TEXT NOT NULL,
    alt_text TEXT,
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

-- Goals (hierarchical)
CREATE TABLE IF NOT EXISTS goals (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    description TEXT,
    status TEXT DEFAULT 'pending',
    priority INTEGER DEFAULT 0,
    progress INTEGER DEFAULT 0,
    parent_id INTEGER REFERENCES goals(id),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_goals_status ON goals(status);
CREATE INDEX IF NOT EXISTS idx_goals_parent ON goals(parent_id);

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
