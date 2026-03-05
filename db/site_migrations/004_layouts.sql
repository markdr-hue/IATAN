-- Server-side layout system: layouts wrap page content with shared structure (nav, footer).
-- The server auto-injects all CSS/JS assets and wraps page content in <main>...</main>.
CREATE TABLE IF NOT EXISTS layouts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    head_content TEXT DEFAULT '',
    body_before_main TEXT DEFAULT '',
    body_after_main TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Pages reference a layout by name. NULL = "default" layout, "none" = no layout wrapping.
ALTER TABLE pages ADD COLUMN layout TEXT DEFAULT NULL;
