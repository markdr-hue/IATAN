-- Additional indexes for common query patterns.

-- Analytics: queries group by page_path and filter by date range.
CREATE INDEX IF NOT EXISTS idx_analytics_page_path ON analytics(page_path, created_at DESC);

-- Goals: brain queries active goals sorted by priority.
CREATE INDEX IF NOT EXISTS idx_goals_status_priority ON goals(status, priority DESC);

-- Page versions: ensure fast lookups by page_id.
CREATE INDEX IF NOT EXISTS idx_page_versions_page ON page_versions(page_id);
