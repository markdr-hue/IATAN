-- Asset scoping: global assets auto-inject on every page, page-scoped assets only when referenced.
ALTER TABLE assets ADD COLUMN scope TEXT NOT NULL DEFAULT 'global';
-- scope = 'global': auto-injected on every page (design system CSS/JS, router, etc.)
-- scope = 'page':   only injected when a page lists it in its assets column

-- Per-page asset list: JSON array of filenames to inject beyond globals.
-- e.g. ["charts.js", "maps.css"]
ALTER TABLE pages ADD COLUMN assets TEXT DEFAULT NULL;
