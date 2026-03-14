/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"database/sql"
	"fmt"
	"time"
)

// validModes are the allowed site operating modes.
var validModes = map[string]bool{
	"building":   true,
	"monitoring": true,
	"paused":     true,
}

// ---------------------------------------------------------------------------
// manage_site
// ---------------------------------------------------------------------------

// SiteTool consolidates site info and mode management into a single tool.
type SiteTool struct{}

func (t *SiteTool) Name() string { return "manage_site" }
func (t *SiteTool) Description() string {
	return "Get site info, change operating mode, or manage URL redirects."
}

func (t *SiteTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"info", "set_mode", "add_redirect", "remove_redirect", "list_redirects"},
			},
			"mode": map[string]interface{}{
				"type":        "string",
				"description": "The mode to set (for set_mode)",
				"enum":        []string{"building", "monitoring", "paused"},
			},
			"source_path": map[string]interface{}{
				"type":        "string",
				"description": "Source URL path for redirect (e.g. /old-page). Must start with /.",
			},
			"target_path": map[string]interface{}{
				"type":        "string",
				"description": "Target URL path or full URL to redirect to (e.g. /new-page).",
			},
			"status_code": map[string]interface{}{
				"type":        "integer",
				"description": "HTTP status code: 301 (permanent, default) or 302 (temporary).",
			},
		},
		"required": []string{"action"},
	}
}

func (t *SiteTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	return DispatchAction(ctx, args, map[string]ActionHandler{
		"info":             t.info,
		"set_mode":         t.setMode,
		"add_redirect":     t.addRedirect,
		"remove_redirect":  t.removeRedirect,
		"list_redirects":   t.listRedirects,
	}, nil)
}

func (t *SiteTool) info(ctx *ToolContext, _ map[string]interface{}) (*Result, error) {
	var name, mode string
	var domain, description sql.NullString
	var createdAt, updatedAt time.Time

	err := ctx.GlobalDB.QueryRow(
		"SELECT name, domain, mode, description, created_at, updated_at FROM sites WHERE id = ?",
		ctx.SiteID,
	).Scan(&name, &domain, &mode, &description, &createdAt, &updatedAt)
	if err != nil {
		return &Result{Success: false, Error: "site not found"}, nil
	}

	result := map[string]interface{}{
		"name":       name,
		"mode":       mode,
		"created_at": createdAt,
		"updated_at": updatedAt,
	}
	if domain.Valid && domain.String != "" {
		result["domain"] = domain.String
	}
	if description.Valid && description.String != "" {
		result["description"] = description.String
	}

	return &Result{Success: true, Data: result}, nil
}

func (t *SiteTool) setMode(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	mode, _ := args["mode"].(string)
	if !validModes[mode] {
		return &Result{Success: false, Error: "mode must be one of: building, monitoring, paused"}, nil
	}

	res, err := ctx.GlobalDB.Exec(
		"UPDATE sites SET mode = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		mode, ctx.SiteID,
	)
	if err != nil {
		return nil, fmt.Errorf("setting site mode: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return &Result{Success: false, Error: "site not found"}, nil
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"site_id": ctx.SiteID,
		"mode":    mode,
	}}, nil
}

// ---------------------------------------------------------------------------
// Redirect management
// ---------------------------------------------------------------------------

func (t *SiteTool) addRedirect(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	source, _ := args["source_path"].(string)
	target, _ := args["target_path"].(string)
	if source == "" || target == "" {
		return &Result{Success: false, Error: "source_path and target_path are required"}, nil
	}
	if source[0] != '/' {
		return &Result{Success: false, Error: "source_path must start with /"}, nil
	}

	statusCode := 301
	if sc, ok := args["status_code"].(float64); ok {
		statusCode = int(sc)
	}
	if statusCode != 301 && statusCode != 302 {
		return &Result{Success: false, Error: "status_code must be 301 (permanent) or 302 (temporary)"}, nil
	}

	_, err := ctx.DB.Exec(
		`INSERT INTO redirects (source_path, target_path, status_code) VALUES (?, ?, ?)
		 ON CONFLICT(source_path) DO UPDATE SET target_path = excluded.target_path, status_code = excluded.status_code`,
		source, target, statusCode,
	)
	if err != nil {
		return nil, fmt.Errorf("adding redirect: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"source_path": source,
		"target_path": target,
		"status_code": statusCode,
	}}, nil
}

func (t *SiteTool) removeRedirect(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	source, _ := args["source_path"].(string)
	if source == "" {
		return &Result{Success: false, Error: "source_path is required"}, nil
	}

	res, err := ctx.DB.Exec("DELETE FROM redirects WHERE source_path = ?", source)
	if err != nil {
		return nil, fmt.Errorf("removing redirect: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &Result{Success: false, Error: "redirect not found"}, nil
	}

	return &Result{Success: true, Data: map[string]interface{}{"deleted": source}}, nil
}

func (t *SiteTool) listRedirects(ctx *ToolContext, _ map[string]interface{}) (*Result, error) {
	rows, err := ctx.DB.Query("SELECT source_path, target_path, status_code, created_at FROM redirects ORDER BY source_path")
	if err != nil {
		return nil, fmt.Errorf("listing redirects: %w", err)
	}
	defer rows.Close()

	var redirects []map[string]interface{}
	for rows.Next() {
		var source, target string
		var statusCode int
		var createdAt time.Time
		if err := rows.Scan(&source, &target, &statusCode, &createdAt); err != nil {
			continue
		}
		redirects = append(redirects, map[string]interface{}{
			"source_path": source,
			"target_path": target,
			"status_code": statusCode,
			"created_at":  createdAt,
		})
	}

	return &Result{Success: true, Data: redirects}, nil
}
