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
