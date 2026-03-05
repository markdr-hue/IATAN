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
	return "Manage site settings. Actions: info (get site details), set_mode (change operating mode)."
}

func (t *SiteTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"info", "set_mode"},
			},
			"mode": map[string]interface{}{
				"type":        "string",
				"description": "The mode to set (for set_mode)",
				"enum":        []string{"building", "monitoring", "paused"},
			},
		},
		"required": []string{"action"},
	}
}

func (t *SiteTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	action, errResult := RequireAction(args)
	if errResult != nil {
		return errResult, nil
	}
	switch action {
	case "info":
		return t.info(ctx, args)
	case "set_mode":
		return t.setMode(ctx, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown action: %q (use info, set_mode)", action)}, nil
	}
}

func (t *SiteTool) info(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	var name, mode string
	var domain, description, direction sql.NullString
	var createdAt, updatedAt time.Time

	err := ctx.GlobalDB.QueryRow(
		"SELECT name, domain, mode, description, direction, created_at, updated_at FROM sites WHERE id = ?",
		ctx.SiteID,
	).Scan(&name, &domain, &mode, &description, &direction, &createdAt, &updatedAt)
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
	if direction.Valid && direction.String != "" {
		result["direction"] = direction.String
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

	if mode == "building" {
		if _, err := ctx.GlobalDB.Exec("UPDATE sites SET tick_count = 0 WHERE id = ?", ctx.SiteID); err != nil {
			return nil, fmt.Errorf("resetting tick count: %w", err)
		}
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"site_id": ctx.SiteID,
		"mode":    mode,
	}}, nil
}
