/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"database/sql"
	"encoding/json"
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
	return "Manage site pages. Actions: save (create/update page), patch (search/replace fix without full rewrite), get (read page), list (list all), delete (soft-delete), restore, history (version history), search (full-text search)."
}

func (t *PagesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action":   map[string]interface{}{"type": "string", "description": "Action to perform", "enum": []string{"save", "patch", "get", "list", "delete", "restore", "history", "search"}},
			"path":     map[string]interface{}{"type": "string", "description": "URL path for the page (e.g. /about)"},
			"title":    map[string]interface{}{"type": "string", "description": "Page title"},
			"content":  map[string]interface{}{"type": "string", "description": "HTML content for the page. The server wraps this with the site layout (nav/footer) and injects shared CSS/JS automatically."},
			"template": map[string]interface{}{"type": "string", "description": "Template name to use for rendering"},
			"status":   map[string]interface{}{"type": "string", "description": "Page status: published or draft", "enum": []string{"published", "draft"}},
			"layout":   map[string]interface{}{"type": "string", "description": `Layout name for this page. Default: uses "default" layout. Use "none" for no layout wrapping.`},
			"assets":   map[string]interface{}{"type": "string", "description": `JSON array of page-scoped asset filenames to include on this page (e.g. ["charts.js","maps.css"]). Global-scope assets are auto-injected on all pages.`},
			"metadata": map[string]interface{}{"type": "string", "description": "JSON string of additional metadata (description, og_image, canonical, keywords)"},
			"limit":    map[string]interface{}{"type": "number", "description": "Maximum number of results to return"},
			"query":    map[string]interface{}{"type": "string", "description": "Search query for full-text search"},
			"patches":  map[string]interface{}{"type": "string", "description": `JSON array of search/replace pairs for patch action: [{"search":"old text","replace":"new text"}]. Works on HTML and JS content.`},
		},
		"required": []string{"action"},
	}
}

func (t *PagesTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	return DispatchAction(ctx, args, map[string]ActionHandler{
		"save":    t.save,
		"patch":   t.patch,
		"get":     t.get,
		"list":    t.list,
		"delete":  t.delete,
		"restore": t.restore,
		"history": t.history,
		"search":  t.search,
	}, func(a map[string]interface{}) string {
		if _, has := a["content"]; has {
			return "save"
		}
		if _, has := a["query"]; has {
			return "search"
		}
		if _, has := a["path"]; has {
			return "get"
		}
		return "list"
	})
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

	// Sanitize: strip dangerous event handlers and javascript: URIs from content.
	content = sanitizePageHTML(content)

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
	warnings := validatePageContent(content)

	// Classify into hard errors (must fix) and soft warnings (informational).
	var hardErrors, softWarnings []string
	for _, w := range warnings {
		if strings.Contains(w, "but no id=") || strings.Contains(w, "syntax error") || strings.Contains(w, "brace") {
			hardErrors = append(hardErrors, w)
		} else {
			softWarnings = append(softWarnings, w)
		}
	}

	// Coherence hints: check layout nav and internal links.
	hints := checkCoherence(path, content, ctx.DB)

	resultData := map[string]interface{}{"path": path, "title": title, "status": status}
	if len(softWarnings) > 0 {
		resultData["ATTENTION_JS_ISSUES"] = softWarnings
	}
	if len(hints) > 0 {
		resultData["hints"] = hints
	}
	if len(hardErrors) > 0 {
		resultData["errors"] = hardErrors
		return &Result{
			Success: false,
			Error:   "Page saved with errors that MUST be fixed: " + strings.Join(hardErrors, "; "),
			Data:    resultData,
		}, nil
	}
	return &Result{Success: true, Data: resultData}, nil
}

// ---------------------------------------------------------------------------
// patch — apply search/replace pairs to a page without full rewrite
// ---------------------------------------------------------------------------

func (t *PagesTool) patch(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return &Result{Success: false, Error: "path is required"}, nil
	}
	patchesStr, _ := args["patches"].(string)
	if patchesStr == "" {
		return &Result{Success: false, Error: "patches is required (JSON array of {search, replace} pairs)"}, nil
	}

	var patches []struct {
		Search  string `json:"search"`
		Replace string `json:"replace"`
	}
	if err := json.Unmarshal([]byte(patchesStr), &patches); err != nil {
		return &Result{Success: false, Error: "patches must be a JSON array: " + err.Error()}, nil
	}
	if len(patches) == 0 {
		return &Result{Success: false, Error: "patches array is empty"}, nil
	}

	// Read current page.
	var existingID int
	var title, content, template, status, metadata, layout, pageAssets sql.NullString
	err := ctx.DB.QueryRow(
		"SELECT id, title, content, template, status, metadata, layout, assets FROM pages WHERE path = ? AND is_deleted = 0",
		path,
	).Scan(&existingID, &title, &content, &template, &status, &metadata, &layout, &pageAssets)
	if err != nil {
		return &Result{Success: false, Error: "page not found"}, nil
	}

	// Capture version history before modifying.
	var maxVer int
	ctx.DB.QueryRow("SELECT COALESCE(MAX(version_number), 0) FROM page_versions WHERE page_id = ?", existingID).Scan(&maxVer)
	ctx.DB.Exec(
		`INSERT INTO page_versions (page_id, path, title, content, template, status, metadata, version_number, changed_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'brain')`,
		existingID, path, title, content, template, status, metadata, maxVer+1,
	)

	// Apply patches sequentially.
	modified := content.String
	var applied, notFound []string
	for _, p := range patches {
		if p.Search == "" {
			continue
		}
		if !strings.Contains(modified, p.Search) {
			notFound = append(notFound, p.Search)
			continue
		}
		modified = strings.ReplaceAll(modified, p.Search, p.Replace)
		if len(p.Search) > 60 {
			applied = append(applied, p.Search[:60]+"...")
		} else {
			applied = append(applied, p.Search)
		}
	}

	if len(applied) == 0 && len(notFound) > 0 {
		return &Result{Success: false, Error: "no patches matched", Data: map[string]interface{}{"not_found": notFound}}, nil
	}

	// Sanitize and save.
	modified = sanitizePageHTML(modified)
	_, err = ctx.DB.Exec(
		"UPDATE pages SET content = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		modified, existingID,
	)
	if err != nil {
		return nil, fmt.Errorf("saving patched page: %w", err)
	}

	// Post-save validation.
	warnings := validatePageContent(modified)
	hints := checkCoherence(path, modified, ctx.DB)

	resultData := map[string]interface{}{
		"path":    path,
		"applied": len(applied),
	}
	if len(notFound) > 0 {
		resultData["not_found"] = notFound
	}
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

func (t *PagesTool) list(ctx *ToolContext, _ map[string]interface{}) (*Result, error) {
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

func validatePageContent(content string) []string {
	var warnings []string

	// Structural warnings (nav, footer, inline styles, shared asset links, style blocks)
	// removed — the server handles all of these silently via stripDocumentShell,
	// stripSharedAssetRefs, and extractAssetTags. Flagging them caused unnecessary
	// fix loops that wasted tokens without improving quality.

	scriptContent := extractScriptContent(content)
	if scriptContent == "" {
		return nil
	}

	// JS brace balance check.
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

// sanitizePageHTML strips dangerous attributes (on* event handlers, javascript: URIs)
// from HTML content to prevent XSS. Preserves all other HTML structure since pages
// are intentionally HTML content built by the AI brain.
var (
	onEventAttrRe   = regexp.MustCompile(`(?i)\s+on[a-z]+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]*)`)
	jsURIRe         = regexp.MustCompile(`(?i)(href|src|action)\s*=\s*(?:"javascript:[^"]*"|'javascript:[^']*')`)
	dataURIScriptRe = regexp.MustCompile(`(?i)(href|src)\s*=\s*(?:"data:text/html[^"]*"|'data:text/html[^']*')`)
)

func sanitizePageHTML(content string) string {
	content = onEventAttrRe.ReplaceAllString(content, "")
	content = jsURIRe.ReplaceAllString(content, "")
	content = dataURIScriptRe.ReplaceAllString(content, "")
	return content
}

func (t *PagesTool) MaxResultSize() int { return 16000 }

func (t *PagesTool) Summarize(result string) string {
	r, data, dataArr, ok := parseSummaryResult(result)
	if !ok {
		return summarizeTruncate(result, 200)
	}
	if !r.Success {
		return summarizeError(r.Error)
	}
	if dataArr != nil {
		return fmt.Sprintf(`{"success":true,"summary":"Returned %d items"}`, len(dataArr))
	}
	if data == nil {
		return summarizeTruncate(result, 300)
	}
	if content, ok := data["content"].(string); ok && content != "" {
		path, _ := data["path"].(string)
		fingerprint := pageStructureFingerprint(content)
		return fmt.Sprintf(`{"success":true,"summary":"Read page %s (%d chars). %s"}`, path, len(content), fingerprint)
	}
	warnings, hasW := data["warnings"]
	hints, hasH := data["hints"]
	if hasW || hasH {
		path, _ := data["path"].(string)
		var parts []string
		parts = append(parts, fmt.Sprintf(`"success":true,"path":"%s"`, path))
		if hasW {
			wJSON, _ := json.Marshal(warnings)
			parts = append(parts, fmt.Sprintf(`"warnings":%s`, wJSON))
		}
		if hasH {
			hJSON, _ := json.Marshal(hints)
			parts = append(parts, fmt.Sprintf(`"hints":%s`, hJSON))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	return summarizeTruncate(result, 300)
}
