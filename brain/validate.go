/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package brain

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// countExists runs a SELECT COUNT(*) query and returns the count.
// Returns 0 if the query fails (logged as warning).
func countExists(db *sql.DB, query string, args ...any) int {
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		slog.Warn("countExists query failed", "query", query, "error", err)
		return 0
	}
	return n
}

// endpointTables maps blueprint endpoint actions to their DB tables and labels.
var endpointTables = map[string]struct{ table, label string }{
	"create_api":       {"api_endpoints", "API"},
	"create_auth":      {"auth_endpoints", "Auth"},
	"create_websocket": {"ws_endpoints", "WebSocket"},
	"create_stream":    {"stream_endpoints", "Stream"},
	"create_upload":    {"upload_endpoints", "Upload"},
}

// validateBlueprintConformance checks that all items from the Blueprint
// were actually created during the BUILD stage. Returns a list of missing items.
// This is a high-level structural check — it does NOT inspect code quality.
func validateBlueprintConformance(db *sql.DB, bp *Blueprint) []string {
	var issues []string

	// Check all Blueprint pages were created.
	for _, page := range bp.Pages {
		if countExists(db, "SELECT COUNT(*) FROM pages WHERE path = ? AND is_deleted = 0", page.Path) == 0 {
			issues = append(issues, fmt.Sprintf("Blueprint page %s (%s) was not created", page.Path, page.Title))
		}
	}

	// Check custom layouts referenced by pages exist.
	for _, page := range bp.Pages {
		layout := page.Layout
		if layout == "" || layout == "default" || layout == "none" {
			continue
		}
		if countExists(db, "SELECT COUNT(*) FROM layouts WHERE name = ?", layout) == 0 {
			issues = append(issues, fmt.Sprintf("Page %s references layout %q but it was not created", page.Path, layout))
		}
	}

	// Check all Blueprint data tables were created.
	for _, t := range bp.DataTables {
		if t.Name == "" {
			continue
		}
		if countExists(db, "SELECT COUNT(*) FROM dynamic_tables WHERE table_name = ?", t.Name) == 0 {
			issues = append(issues, fmt.Sprintf("Table %q not created", t.Name))
		}
	}

	// Check all Blueprint endpoints were created.
	for _, ep := range bp.Endpoints {
		if ep.Path == "" {
			continue
		}
		if et, ok := endpointTables[ep.Action]; ok {
			if countExists(db, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE path = ?", et.table), ep.Path) == 0 {
				issues = append(issues, fmt.Sprintf("%s endpoint for path %q not created", et.label, ep.Path))
			}
		}
	}

	// Check all Blueprint webhooks were created.
	for _, wh := range bp.Webhooks {
		if wh.Name == "" {
			continue
		}
		if countExists(db, "SELECT COUNT(*) FROM webhooks WHERE name = ?", wh.Name) == 0 {
			issues = append(issues, fmt.Sprintf("Webhook %q not created", wh.Name))
		}
	}

	// Check all Blueprint scheduled tasks were created.
	for _, task := range bp.ScheduledTasks {
		if task.Name == "" {
			continue
		}
		if countExists(db, "SELECT COUNT(*) FROM scheduled_tasks WHERE name = ?", task.Name) == 0 {
			issues = append(issues, fmt.Sprintf("Scheduled task %q not created", task.Name))
		}
	}

	// Check all nav items reference existing pages.
	for _, navPath := range bp.NavItems {
		if countExists(db, "SELECT COUNT(*) FROM pages WHERE path = ? AND is_deleted = 0", navPath) == 0 {
			issues = append(issues, fmt.Sprintf("Nav item %q references non-existent page", navPath))
		}
	}

	return issues
}

// internalHrefRe matches href="/..." (internal links, not anchors, external, or API/asset/file paths).
var internalHrefRe = regexp.MustCompile(`href="(/[^"]*)"`)

// validatePageQuality performs deeper quality checks on built pages.
// Returns issues that should trigger a fixup cycle.
func validatePageQuality(db *sql.DB, bp *Blueprint) []string {
	var issues []string

	// Collect all existing page paths for link validation.
	existingPages := make(map[string]bool)
	pageRows, err := db.Query("SELECT path FROM pages WHERE is_deleted = 0")
	if err == nil {
		defer pageRows.Close()
		for pageRows.Next() {
			var p string
			pageRows.Scan(&p)
			existingPages[p] = true
		}
	}

	for _, page := range bp.Pages {
		var content sql.NullString
		var assetsJSON sql.NullString
		err := db.QueryRow(
			"SELECT content, assets FROM pages WHERE path = ? AND is_deleted = 0",
			page.Path,
		).Scan(&content, &assetsJSON)
		if err != nil {
			continue // Page doesn't exist — caught by validateBlueprintConformance.
		}

		// Empty pages.
		if !content.Valid || strings.TrimSpace(content.String) == "" {
			issues = append(issues, fmt.Sprintf("Page %s has no content", page.Path))
			continue
		}

		// Missing page-scoped assets.
		if assetsJSON.Valid && assetsJSON.String != "" && assetsJSON.String != "[]" {
			var assetList []string
			if json.Unmarshal([]byte(assetsJSON.String), &assetList) == nil {
				for _, a := range assetList {
					if countExists(db, "SELECT COUNT(*) FROM assets WHERE filename = ?", a) == 0 {
						issues = append(issues, fmt.Sprintf("Page %s references asset %q which does not exist", page.Path, a))
					}
				}
			}
		}

		// Broken internal links.
		matches := internalHrefRe.FindAllStringSubmatch(content.String, -1)
		for _, m := range matches {
			href := m[1]
			// Skip API, assets, files, and anchor-only links.
			if strings.HasPrefix(href, "/api/") || strings.HasPrefix(href, "/assets/") ||
				strings.HasPrefix(href, "/files/") || href == "#" {
				continue
			}
			// Strip query string and fragment for matching.
			clean := strings.SplitN(href, "?", 2)[0]
			clean = strings.SplitN(clean, "#", 2)[0]
			if clean == "" {
				continue
			}
			// Check exact match or parameterized route pattern.
			if !existingPages[clean] && !matchesParameterizedPage(clean, existingPages) {
				issues = append(issues, fmt.Sprintf("Page %s has link to %q which does not exist", page.Path, clean))
			}
		}
	}

	// Validate API column references in JS assets.
	issues = append(issues, validateAPIColumnRefs(db)...)

	// No global CSS — site will have no styling.
	if len(bp.Pages) > 0 {
		if countExists(db, "SELECT COUNT(*) FROM assets WHERE scope = 'global' AND filename LIKE '%.css'") == 0 {
			issues = append(issues, "No global CSS file — site will have no styling")
		}
	}

	// CSS class validation: check that classes used in HTML exist in CSS.
	issues = append(issues, validateCSSClassRefs(db, bp)...)

	return issues
}

// htmlClassRe matches class="..." attributes in HTML.
var htmlClassRe = regexp.MustCompile(`class="([^"]+)"`)

// cssDefinedClassRe matches class selectors in CSS (e.g., .card, .btn-primary).
var cssDefinedClassRe = regexp.MustCompile(`\.([\w-]+)\s*[{,:\s]`)

// validateCSSClassRefs checks that CSS classes referenced in page HTML actually
// exist in at least one CSS file. Reports only high-confidence mismatches.
func validateCSSClassRefs(db *sql.DB, bp *Blueprint) []string {
	// Load all CSS content (global + page-scoped).
	cssRows, err := db.Query("SELECT filename, storage_path FROM assets WHERE filename LIKE '%.css'")
	if err != nil {
		return nil
	}
	defer cssRows.Close()

	definedClasses := make(map[string]bool)
	for cssRows.Next() {
		var filename, storagePath string
		cssRows.Scan(&filename, &storagePath)
		if storagePath == "" {
			continue
		}
		data, err := os.ReadFile(storagePath)
		if err != nil {
			continue
		}
		for _, m := range cssDefinedClassRe.FindAllStringSubmatch(string(data), -1) {
			definedClasses[m[1]] = true
		}
	}

	if len(definedClasses) == 0 {
		return nil // No CSS files to validate against.
	}

	// Common framework/utility classes to ignore (not defined in custom CSS).
	commonClasses := map[string]bool{
		"active": true, "hidden": true, "disabled": true, "show": true, "hide": true,
		"open": true, "closed": true, "selected": true, "loading": true, "error": true,
		"success": true, "visible": true, "collapsed": true, "expanded": true,
	}

	// Check each page's HTML for class references.
	var issues []string
	missingCounts := make(map[string]int)

	for _, page := range bp.Pages {
		var content sql.NullString
		db.QueryRow("SELECT content FROM pages WHERE path = ? AND is_deleted = 0", page.Path).Scan(&content)
		if !content.Valid || content.String == "" {
			continue
		}

		matches := htmlClassRe.FindAllStringSubmatch(content.String, -1)
		for _, m := range matches {
			for _, cls := range strings.Fields(m[1]) {
				cls = strings.TrimSpace(cls)
				if cls == "" || commonClasses[cls] || definedClasses[cls] {
					continue
				}
				missingCounts[cls]++
			}
		}
	}

	// Only report classes that appear multiple times (reduces false positives from
	// dynamic classes set via JS).
	for cls, count := range missingCounts {
		if count >= 2 {
			issues = append(issues, fmt.Sprintf(
				"CSS class %q used in %d pages but not defined in any CSS file — add it to global CSS or fix the class name",
				cls, count))
		}
	}

	// Cap at 5 issues to avoid overwhelming the fixup prompt.
	if len(issues) > 5 {
		issues = issues[:5]
	}

	return issues
}

// apiQueryParamRe matches /api/ENDPOINT followed by query parameters.
// Captures: endpoint path and the query string portion.
// Works for both HTML href and JS string patterns like:
//
//	/api/messages?room_id=...
//	/api/messages?room_id=${...}
//	/api/messages?' + 'room_id=' + ...
var apiQueryParamRe = regexp.MustCompile(`/api/([\w-]+)\?([^"'\x60#\s]+)`)

// apiEndpointRefRe matches /api/ENDPOINT references (with or without query string).
var apiEndpointRefRe = regexp.MustCompile(`/api/([\w-]+)`)

// queryParamNameRe extracts parameter names from a query string like "room_id=...&limit=50".
var queryParamNameRe = regexp.MustCompile(`(?:^|&)([\w]+)=`)

// jsPropertyAccessRe matches .property patterns after common API response variable names.
// Catches: item.room_id, row.room_id, data.room_id, msg.room_id, etc.
var jsPropertyAccessRe = regexp.MustCompile(`\b(?:item|row|record|entry|msg|message|data|result|obj)\.([\w]+)`)

// reservedQueryParams are query params handled by the platform, not table columns.
var reservedQueryParams = map[string]bool{
	"limit": true, "offset": true, "sort": true, "order": true,
	"search": true, "stats": true, "v": true, "t": true, "token": true,
}

// validateAPIColumnRefs checks that JS code referencing /api/ endpoints uses
// column names that actually exist in the target table.
func validateAPIColumnRefs(db *sql.DB) []string {
	// Build endpoint → table mapping.
	epToTable := make(map[string]string)
	epRows, err := db.Query("SELECT path, table_name FROM api_endpoints")
	if err != nil {
		return nil
	}
	defer epRows.Close()
	for epRows.Next() {
		var path, tableName string
		epRows.Scan(&path, &tableName)
		epToTable[path] = tableName
	}
	if len(epToTable) == 0 {
		return nil
	}

	// Build table → column set using PRAGMA.
	tableColumns := make(map[string]map[string]bool)
	for _, tableName := range epToTable {
		if _, ok := tableColumns[tableName]; ok {
			continue
		}
		cols := getTableColumns(db, tableName)
		if len(cols) > 0 {
			tableColumns[tableName] = cols
		}
	}

	// Collect all JS content: page-scoped and global JS assets.
	jsContents := loadJSAssetContents(db)

	// Also include inline scripts from page content.
	pageRows, err := db.Query("SELECT path, content FROM pages WHERE is_deleted = 0")
	if err == nil {
		defer pageRows.Close()
		for pageRows.Next() {
			var path string
			var content sql.NullString
			pageRows.Scan(&path, &content)
			if content.Valid && content.String != "" {
				jsContents = append(jsContents, jsSource{name: "page:" + path, content: content.String})
			}
		}
	}

	var issues []string
	reported := make(map[string]bool) // deduplicate

	for _, js := range jsContents {
		// Find /api/ENDPOINT?param=value patterns.
		matches := apiQueryParamRe.FindAllStringSubmatch(js.content, -1)
		for _, m := range matches {
			epPath := m[1]
			queryStr := m[2]

			tableName, ok := epToTable[epPath]
			if !ok {
				continue
			}
			cols, ok := tableColumns[tableName]
			if !ok {
				continue
			}

			// Extract param names from query string.
			paramMatches := queryParamNameRe.FindAllStringSubmatch(queryStr, -1)
			for _, pm := range paramMatches {
				param := pm[1]
				if reservedQueryParams[param] {
					continue
				}
				if !cols[param] {
					key := epPath + ":" + param
					if !reported[key] {
						issues = append(issues, fmt.Sprintf(
							"JS references /api/%s with filter %q but column does not exist in table %q (available: %s)",
							epPath, param, tableName, columnList(cols),
						))
						reported[key] = true
					}
				}
			}
		}

		// Find property access patterns on API response data that reference
		// endpoints used in the same JS file.
		referencedEndpoints := apiEndpointRefRe.FindAllStringSubmatch(js.content, -1)
		epSet := make(map[string]bool)
		for _, m := range referencedEndpoints {
			epSet[m[1]] = true
		}

		// Only check property access if the JS uses exactly one endpoint
		// (avoids false positives from multi-endpoint files).
		if len(epSet) == 1 {
			var singleEP string
			for ep := range epSet {
				singleEP = ep
			}
			tableName, ok := epToTable[singleEP]
			if !ok {
				continue
			}
			cols, ok := tableColumns[tableName]
			if !ok {
				continue
			}

			propMatches := jsPropertyAccessRe.FindAllStringSubmatch(js.content, -1)
			for _, pm := range propMatches {
				prop := pm[1]
				// Skip common non-column properties.
				if prop == "length" || prop == "id" || prop == "data" ||
					prop == "error" || prop == "message" || prop == "status" ||
					prop == "count" || prop == "offset" || prop == "limit" ||
					prop == "toString" || prop == "map" || prop == "filter" ||
					prop == "forEach" || prop == "push" || prop == "value" ||
					prop == "style" || prop == "className" || prop == "innerHTML" ||
					prop == "textContent" || prop == "addEventListener" {
					continue
				}
				if !cols[prop] {
					key := singleEP + ":prop:" + prop
					if !reported[key] {
						issues = append(issues, fmt.Sprintf(
							"JS accesses .%s on /api/%s response but column does not exist in table %q",
							prop, singleEP, tableName,
						))
						reported[key] = true
					}
				}
			}
		}
	}

	return issues
}

type jsSource struct {
	name    string
	content string
}

// getTableColumns returns the set of column names for a table.
func getTableColumns(db *sql.DB, tableName string) map[string]bool {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return nil
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			continue
		}
		cols[name] = true
	}
	return cols
}

// loadJSAssetContents reads all .js asset files and returns their content.
func loadJSAssetContents(db *sql.DB) []jsSource {
	rows, err := db.Query("SELECT filename, storage_path FROM assets WHERE filename LIKE '%.js'")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var sources []jsSource
	for rows.Next() {
		var filename, storagePath string
		rows.Scan(&filename, &storagePath)
		if storagePath == "" {
			continue
		}
		data, err := os.ReadFile(storagePath)
		if err != nil {
			slog.Debug("could not read JS asset for validation", "file", filename, "error", err)
			continue
		}
		sources = append(sources, jsSource{name: filename, content: string(data)})
	}
	return sources
}

// columnList returns a comma-separated list of column names for error messages.
func columnList(cols map[string]bool) string {
	var names []string
	for c := range cols {
		names = append(names, c)
	}
	if len(names) > 8 {
		names = names[:8]
		names = append(names, "...")
	}
	return strings.Join(names, ", ")
}

// matchesParameterizedPage checks if a concrete path (e.g., /room/5) matches
// a parameterized page pattern (e.g., /room/:id).
func matchesParameterizedPage(path string, pages map[string]bool) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for p := range pages {
		pageParts := strings.Split(strings.Trim(p, "/"), "/")
		if len(pageParts) != len(parts) {
			continue
		}
		match := true
		for i, pp := range pageParts {
			if strings.HasPrefix(pp, ":") {
				continue // Wildcard segment.
			}
			if pp != parts[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
