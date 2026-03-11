/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package brain

import (
	"database/sql"
	"fmt"
	"log/slog"
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

	// Check all nav items reference existing pages.
	for _, navPath := range bp.NavItems {
		if countExists(db, "SELECT COUNT(*) FROM pages WHERE path = ? AND is_deleted = 0", navPath) == 0 {
			issues = append(issues, fmt.Sprintf("Nav item %q references non-existent page", navPath))
		}
	}

	return issues
}
