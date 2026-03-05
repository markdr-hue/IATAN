/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// PagesTool — unified manager for all page operations
// ---------------------------------------------------------------------------

type PagesTool struct{}

func (t *PagesTool) Name() string { return "manage_pages" }
func (t *PagesTool) Description() string {
	return "Manage site pages. Actions: save (create/update page), get (read page), list (list all), delete (soft-delete), restore, history (version history), search (full-text search)."
}

func (t *PagesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action":   map[string]interface{}{"type": "string", "description": "Action to perform", "enum": []string{"save", "get", "list", "delete", "restore", "history", "search"}},
			"path":     map[string]interface{}{"type": "string", "description": "URL path for the page (e.g. /about)"},
			"title":    map[string]interface{}{"type": "string", "description": "Page title"},
			"content":  map[string]interface{}{"type": "string", "description": "MAIN-CONTENT-ONLY HTML. No nav, footer, or shared asset links (the server auto-injects those from the layout and assets tables). No <!DOCTYPE>/<html>/<head>/<body> tags."},
			"template": map[string]interface{}{"type": "string", "description": "Template name to use for rendering"},
			"status":   map[string]interface{}{"type": "string", "description": "Page status: published or draft", "enum": []string{"published", "draft"}},
			"layout":   map[string]interface{}{"type": "string", "description": `Layout name for this page. Default: uses "default" layout. Use "none" for no layout wrapping.`},
			"assets":   map[string]interface{}{"type": "string", "description": `JSON array of page-scoped asset filenames to include on this page (e.g. ["charts.js","maps.css"]). Global-scope assets are auto-injected on all pages.`},
			"metadata": map[string]interface{}{"type": "string", "description": "JSON string of additional metadata (description, og_image, canonical, keywords)"},
			"limit":    map[string]interface{}{"type": "number", "description": "Maximum number of results to return"},
			"query":    map[string]interface{}{"type": "string", "description": "Search query for full-text search"},
		},
		"required": []string{"action"},
	}
}

func (t *PagesTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	action, errResult := RequireAction(args)
	if errResult != nil {
		// Infer action from provided args — LLMs sometimes omit the action field.
		if _, hasContent := args["content"]; hasContent {
			action = "save"
		} else if _, hasQuery := args["query"]; hasQuery {
			action = "search"
		} else if _, hasPath := args["path"]; hasPath {
			action = "get"
		} else {
			action = "list"
		}
		args["action"] = action
	}
	switch action {
	case "save":
		return t.save(ctx, args)
	case "get":
		return t.get(ctx, args)
	case "list":
		return t.list(ctx, args)
	case "delete":
		return t.delete(ctx, args)
	case "restore":
		return t.restore(ctx, args)
	case "history":
		return t.history(ctx, args)
	case "search":
		return t.search(ctx, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown action %q — use save, get, list, delete, restore, history, or search", action)}, nil
	}
}

// ---------------------------------------------------------------------------
// save — create or update a page with version history capture
// ---------------------------------------------------------------------------

func (t *PagesTool) save(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return &Result{Success: false, Error: "path is required"}, nil
	}
	content, _ := args["content"].(string)
	title, _ := args["title"].(string)
	template, _ := args["template"].(string)
	status, _ := args["status"].(string)
	if status == "" {
		status = "published"
	}
	metadata, _ := args["metadata"].(string)
	if metadata == "" {
		metadata = "{}"
	}
	layout, _ := args["layout"].(string)     // "" = default layout, "none" = no layout
	pageAssets, _ := args["assets"].(string) // JSON array of page-scoped asset filenames

	// Before upsert: capture existing page into version history.
	var existingID int
	var oldTitle, oldContent, oldTemplate, oldStatus, oldMeta sql.NullString
	err := ctx.DB.QueryRow(
		"SELECT id, title, content, template, status, metadata FROM pages WHERE path = ? AND is_deleted = 0",
		path,
	).Scan(&existingID, &oldTitle, &oldContent, &oldTemplate, &oldStatus, &oldMeta)
	if err == nil {
		// Page exists — save current version before overwriting.
		var maxVer int
		ctx.DB.QueryRow("SELECT COALESCE(MAX(version_number), 0) FROM page_versions WHERE page_id = ?", existingID).Scan(&maxVer)
		ctx.DB.Exec(
			`INSERT INTO page_versions (page_id, path, title, content, template, status, metadata, version_number, changed_by)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'brain')`,
			existingID, path, oldTitle, oldContent, oldTemplate, oldStatus, oldMeta, maxVer+1,
		)
	}

	// Layout column: NULL means use "default" layout. Store NULL unless explicitly set.
	var layoutArg interface{}
	if layout != "" {
		layoutArg = layout
	}
	// Assets column: NULL means no page-scoped assets. Store NULL unless explicitly set.
	var assetsArg interface{}
	if pageAssets != "" {
		assetsArg = pageAssets
	}

	// Upsert the page.
	_, err = ctx.DB.Exec(
		`INSERT INTO pages (path, title, content, template, status, metadata, layout, assets, is_deleted, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, CURRENT_TIMESTAMP)
		 ON CONFLICT(path) DO UPDATE SET
		   title = excluded.title,
		   content = excluded.content,
		   template = excluded.template,
		   status = excluded.status,
		   metadata = excluded.metadata,
		   layout = excluded.layout,
		   assets = excluded.assets,
		   is_deleted = 0,
		   deleted_at = NULL,
		   updated_at = CURRENT_TIMESTAMP`,
		path, title, content, template, status, metadata, layoutArg, assetsArg,
	)
	if err != nil {
		return nil, fmt.Errorf("saving page: %w", err)
	}

	// Post-save validation: warn about missing asset references, inline styles, JS issues.
	warnings := validatePageContent(content, ctx.DB)

	// Coherence hints: check layout nav and internal links.
	hints := checkCoherence(path, content, ctx.DB)

	resultData := map[string]interface{}{"path": path, "title": title, "status": status}
	if len(warnings) > 0 {
		resultData["warnings"] = warnings
	}
	if len(hints) > 0 {
		resultData["hints"] = hints
	}
	return &Result{Success: true, Data: resultData}, nil
}

// ---------------------------------------------------------------------------
// get — retrieve a page by its path
// ---------------------------------------------------------------------------

func (t *PagesTool) get(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return &Result{Success: false, Error: "path is required"}, nil
	}

	var id int
	var title, content, template, status, metadata sql.NullString
	var createdAt, updatedAt time.Time

	err := ctx.DB.QueryRow(
		"SELECT id, title, content, template, status, metadata, created_at, updated_at FROM pages WHERE path = ? AND is_deleted = 0",
		path,
	).Scan(&id, &title, &content, &template, &status, &metadata, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return &Result{Success: false, Error: "page not found"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting page: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"id":         id,
		"path":       path,
		"title":      title.String,
		"content":    content.String,
		"template":   template.String,
		"status":     status.String,
		"metadata":   metadata.String,
		"created_at": createdAt,
		"updated_at": updatedAt,
	}}, nil
}

// ---------------------------------------------------------------------------
// list — list all pages for the current site
// ---------------------------------------------------------------------------

func (t *PagesTool) list(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	rows, err := ctx.DB.Query(
		"SELECT id, path, title, status, updated_at FROM pages WHERE is_deleted = 0 ORDER BY path",
	)
	if err != nil {
		return nil, fmt.Errorf("listing pages: %w", err)
	}
	defer rows.Close()

	var pages []map[string]interface{}
	for rows.Next() {
		var id int
		var path string
		var title, status sql.NullString
		var updatedAt time.Time
		if err := rows.Scan(&id, &path, &title, &status, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning page: %w", err)
		}
		pages = append(pages, map[string]interface{}{
			"id":         id,
			"path":       path,
			"title":      title.String,
			"status":     status.String,
			"updated_at": updatedAt,
		})
	}

	return &Result{Success: true, Data: pages}, nil
}

// ---------------------------------------------------------------------------
// delete — soft-delete a page by its path
// ---------------------------------------------------------------------------

func (t *PagesTool) delete(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return &Result{Success: false, Error: "path is required"}, nil
	}

	res, err := ctx.DB.Exec(
		"UPDATE pages SET is_deleted = 1, deleted_at = CURRENT_TIMESTAMP WHERE path = ? AND is_deleted = 0",
		path,
	)
	if err != nil {
		return nil, fmt.Errorf("deleting page: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return &Result{Success: false, Error: "page not found"}, nil
	}

	return &Result{Success: true, Data: map[string]interface{}{"deleted": path}}, nil
}

// ---------------------------------------------------------------------------
// restore — restore a soft-deleted page
// ---------------------------------------------------------------------------

func (t *PagesTool) restore(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return &Result{Success: false, Error: "path is required"}, nil
	}

	res, err := ctx.DB.Exec(
		"UPDATE pages SET is_deleted = 0, deleted_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE path = ? AND is_deleted = 1",
		path,
	)
	if err != nil {
		return nil, fmt.Errorf("restoring page: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return &Result{Success: false, Error: "no deleted page found at that path"}, nil
	}

	return &Result{Success: true, Data: map[string]interface{}{"restored": path}}, nil
}

// ---------------------------------------------------------------------------
// history — view version history for a page
// ---------------------------------------------------------------------------

func (t *PagesTool) history(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return &Result{Success: false, Error: "path is required"}, nil
	}

	limit := 20
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	// Find the page ID.
	var pageID int
	err := ctx.DB.QueryRow(
		"SELECT id FROM pages WHERE path = ?",
		path,
	).Scan(&pageID)
	if err != nil {
		return &Result{Success: false, Error: "page not found"}, nil
	}

	rows, err := ctx.DB.Query(
		"SELECT version_number, title, status, changed_by, created_at FROM page_versions WHERE page_id = ? ORDER BY version_number DESC LIMIT ?",
		pageID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying page history: %w", err)
	}
	defer rows.Close()

	var versions []map[string]interface{}
	for rows.Next() {
		var ver int
		var title, status, changedBy sql.NullString
		var createdAt time.Time
		if err := rows.Scan(&ver, &title, &status, &changedBy, &createdAt); err != nil {
			ctx.Logger.Warn("scan error in page history", "page_id", pageID, "error", err)
			continue
		}
		versions = append(versions, map[string]interface{}{
			"version":    ver,
			"title":      title.String,
			"status":     status.String,
			"changed_by": changedBy.String,
			"created_at": createdAt,
		})
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"path":     path,
		"versions": versions,
	}}, nil
}

// ---------------------------------------------------------------------------
// search — full-text search across pages
// ---------------------------------------------------------------------------

func (t *PagesTool) search(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	q, _ := args["query"].(string)
	if q == "" {
		return &Result{Success: false, Error: "query is required for search"}, nil
	}
	escaped := strings.NewReplacer("%", `\%`, "_", `\_`).Replace(q)
	like := "%" + escaped + "%"
	rows, err := ctx.DB.Query(
		"SELECT id, path, title, status FROM pages WHERE is_deleted = 0 AND (title LIKE ? ESCAPE '\\' OR content LIKE ? ESCAPE '\\') ORDER BY path",
		like, like,
	)
	if err != nil {
		return nil, fmt.Errorf("searching pages: %w", err)
	}
	defer rows.Close()
	var pages []map[string]interface{}
	for rows.Next() {
		var id int
		var path string
		var title, status sql.NullString
		if err := rows.Scan(&id, &path, &title, &status); err != nil {
			ctx.Logger.Warn("scan error in page search", "query", q, "error", err)
			continue
		}
		pages = append(pages, map[string]interface{}{"id": id, "path": path, "title": title.String, "status": status.String})
	}
	return &Result{Success: true, Data: pages}, nil
}

// ---------------------------------------------------------------------------
// Page content validation helpers
// ---------------------------------------------------------------------------

var (
	pageScriptRe  = regexp.MustCompile(`(?isU)<script[^>]*>(.+)</script>`)
	internalLinkRe = regexp.MustCompile(`href\s*=\s*["'](/[^"'#?]*)["'#?]?`)
)

// Regexes for JS DOM reference validation.
var (
	getByIdRe       = regexp.MustCompile(`getElementById\(\s*['"]([^'"]+)['"]\s*\)`)
	querySelectorRe = regexp.MustCompile(`querySelector\(\s*['"]#([^'"]+)['"]\s*\)`)
	htmlIDRe        = regexp.MustCompile(`(?i)\bid\s*=\s*["']([^"']+)["']`)
	// Detects unguarded querySelector/querySelectorAll: result used with . but no ?. and not inside an if-check.
	// Matches: querySelector(...).something or querySelectorAll(...).something (without ?.)
	unguardedQSRe = regexp.MustCompile(`querySelector(?:All)?\([^)]+\)\.(?:[a-zA-Z])`)
	// Detects top-level const/let/class declarations that cause re-declaration errors in SPA navigation.
	globalDeclRe = regexp.MustCompile(`(?m)^(?:const|let|class)\s+\w+`)
)

func extractScriptContent(content string) string {
	matches := pageScriptRe.FindAllStringSubmatch(content, -1)
	var parts []string
	for _, m := range matches {
		if len(m) > 1 {
			parts = append(parts, m[1])
		}
	}
	return strings.Join(parts, "\n")
}

func validatePageContent(content string, db *sql.DB) []string {
	var warnings []string
	lower := strings.ToLower(content)

	// Determine site architecture for SPA-specific validation.
	var siteArch string
	_ = db.QueryRow("SELECT value FROM memory WHERE key = 'site_architecture'").Scan(&siteArch)

	// Warn if page contains layout-level content (nav, footer) — these belong in manage_layout.
	if strings.Contains(lower, "<nav") {
		warnings = append(warnings, "Page contains <nav> — navigation should be in the layout (manage_layout), not in individual pages")
	}
	if strings.Contains(lower, "<footer") {
		warnings = append(warnings, "Page contains <footer> — footer should be in the layout (manage_layout), not in individual pages")
	}

	// Warn if page includes shared asset references (auto-injected by the server).
	if strings.Contains(lower, `rel="stylesheet"`) && strings.Contains(lower, "/assets/") {
		warnings = append(warnings, "Page contains <link rel='stylesheet'> for /assets/ — shared CSS is auto-injected by the server. Remove these links.")
	}
	if strings.Contains(lower, `<script`) && strings.Contains(lower, `src="`) && strings.Contains(lower, "/assets/") {
		warnings = append(warnings, "Page contains <script src='/assets/'> — shared JS is auto-injected by the server. Remove these tags.")
	}

	if strings.Contains(lower, "style=\"") {
		warnings = append(warnings, "Page contains inline styles — prefer shared CSS classes")
	}
	if strings.Contains(lower, "<style>") || strings.Contains(lower, "<style ") {
		warnings = append(warnings, "Page contains a <style> block — prefer adding styles to shared CSS")
	}

	scriptContent := extractScriptContent(content)
	if scriptContent != "" {
		opens := strings.Count(scriptContent, "{")
		closes := strings.Count(scriptContent, "}")
		if opens != closes {
			warnings = append(warnings, fmt.Sprintf("Possible JS syntax error: %d open braces vs %d close braces", opens, closes))
		}

		// Collect all IDs defined in the HTML.
		htmlIDs := map[string]bool{}
		for _, m := range htmlIDRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				htmlIDs[m[1]] = true
			}
		}

		// Check getElementById('xxx') references against actual IDs.
		referencedIDs := map[string]bool{}
		for _, m := range getByIdRe.FindAllStringSubmatch(scriptContent, -1) {
			if len(m) > 1 {
				referencedIDs[m[1]] = true
			}
		}
		// Check querySelector('#xxx') references.
		for _, m := range querySelectorRe.FindAllStringSubmatch(scriptContent, -1) {
			if len(m) > 1 {
				referencedIDs[m[1]] = true
			}
		}
		for id := range referencedIDs {
			if !htmlIDs[id] {
				warnings = append(warnings, fmt.Sprintf("JS references element #%s but no id=\"%s\" found in HTML — will cause TypeError at runtime", id, id))
			}
		}

		// Detect unguarded querySelector().property (missing ?. operator).
		if unguardedQSRe.MatchString(scriptContent) {
			warnings = append(warnings, "querySelector() result used without ?. — add optional chaining to avoid TypeError when element is missing")
		}

		// SPA: detect top-level const/let/class declarations that fail on re-execution.
		if siteArch == "spa" && globalDeclRe.MatchString(scriptContent) {
			warnings = append(warnings, "SPA page has top-level const/let/class in inline <script> — wrap in an IIFE: (function(){ ... })(); to prevent 'Identifier already declared' errors on navigation")
		}
	}

	return warnings
}

// checkCoherence checks for navigation and link consistency after saving a page.
// Returns hints (not warnings) that help the LLM maintain site coherence.
func checkCoherence(pagePath, content string, db *sql.DB) []string {
	var hints []string

	// Check if the layout nav links to this page (skip for / since it's often just the logo link).
	if pagePath != "/" {
		var navHTML sql.NullString
		db.QueryRow("SELECT body_before_main FROM layouts WHERE name = 'default'").Scan(&navHTML)
		if navHTML.Valid && navHTML.String != "" {
			if !strings.Contains(navHTML.String, `"`+pagePath+`"`) && !strings.Contains(navHTML.String, `'`+pagePath+`'`) {
				hints = append(hints, fmt.Sprintf("Layout nav does not link to %s — update with manage_layout if this page should be in navigation", pagePath))
			}
		}
	}

	// Check internal links in page content for dead links (pages that don't exist).
	matches := internalLinkRe.FindAllStringSubmatch(content, -1)
	seen := map[string]bool{}
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		target := m[1]
		// Skip asset/api/file paths and self-references.
		if strings.HasPrefix(target, "/assets/") || strings.HasPrefix(target, "/api/") || strings.HasPrefix(target, "/files/") || target == pagePath {
			continue
		}
		if seen[target] {
			continue
		}
		seen[target] = true
		var exists int
		db.QueryRow("SELECT COUNT(*) FROM pages WHERE path = ? AND is_deleted = 0", target).Scan(&exists)
		if exists == 0 {
			hints = append(hints, fmt.Sprintf("Links to %s which does not exist yet — create it or fix the link", target))
		}
	}

	return hints
}
