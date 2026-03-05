/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// LayoutTool — manage site layouts (nav, footer, shared structure)
// ---------------------------------------------------------------------------

type LayoutTool struct{}

func (t *LayoutTool) Name() string { return "manage_layout" }
func (t *LayoutTool) Description() string {
	return "Manage site layouts that wrap every page with shared structure. The server auto-injects <main>...</main> around page content and auto-injects all CSS/JS assets from /assets/. Actions: save (create/update layout), get (read layout), list (list all layouts)."
}

func (t *LayoutTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"save", "get", "list"},
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": `Layout name. Use "default" for the main site layout.`,
			},
			"head_content": map[string]interface{}{
				"type":        "string",
				"description": "Extra HTML for <head> (Google Fonts, custom meta, favicons). Shared CSS/JS from /assets/ are auto-injected — do NOT include them here.",
			},
			"body_before_main": map[string]interface{}{
				"type":        "string",
				"description": "HTML before page content: skip-to-content link, nav, header. Do NOT include <main> — the server adds it automatically.",
			},
			"body_after_main": map[string]interface{}{
				"type":        "string",
				"description": "HTML after page content: footer, back-to-top button. Do NOT include </main> — the server adds it automatically.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *LayoutTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	action, errResult := RequireAction(args)
	if errResult != nil {
		// Infer action from provided args — LLMs sometimes omit the action field.
		if _, hasBefore := args["body_before_main"]; hasBefore {
			action = "save"
		} else if _, hasAfter := args["body_after_main"]; hasAfter {
			action = "save"
		} else if _, hasHead := args["head_content"]; hasHead {
			action = "save"
		} else if _, hasName := args["name"]; hasName {
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
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown action %q — use save, get, or list", action)}, nil
	}
}

// Regexes for stripping content that shouldn't be in layouts.
var (
	layoutMainOpenRe  = regexp.MustCompile(`(?i)<main[\s>][^>]*>`)
	layoutMainCloseRe = regexp.MustCompile(`(?i)</main\s*>`)
	layoutDocShellRe  = regexp.MustCompile(`(?i)</?(!DOCTYPE|html|head|body)[^>]*>`)
	layoutAssetLinkRe = regexp.MustCompile(`(?i)<link[^>]*href=["']/assets/[^"']*["'][^>]*/?>`)
	layoutAssetSrcRe  = regexp.MustCompile(`(?i)<script[^>]*src=["']/assets/[^"']*["'][^>]*>[\s\S]*?</script>`)
)

func (t *LayoutTool) save(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return &Result{Success: false, Error: "name is required (use \"default\" for the main layout)"}, nil
	}

	headContent, _ := args["head_content"].(string)
	bodyBefore, _ := args["body_before_main"].(string)
	bodyAfter, _ := args["body_after_main"].(string)

	var warnings []string

	// Strip document shell tags from all fields.
	for _, field := range []*string{&headContent, &bodyBefore, &bodyAfter} {
		if layoutDocShellRe.MatchString(*field) {
			*field = layoutDocShellRe.ReplaceAllString(*field, "")
			warnings = append(warnings, "Stripped <!DOCTYPE>/<html>/<head>/<body> tags — the server handles document structure")
		}
	}

	// Strip <main>/<//main> from body fields.
	for _, field := range []*string{&bodyBefore, &bodyAfter} {
		if layoutMainOpenRe.MatchString(*field) {
			*field = layoutMainOpenRe.ReplaceAllString(*field, "")
			warnings = append(warnings, "Stripped <main> tag — the server wraps page content in <main> automatically")
		}
		if layoutMainCloseRe.MatchString(*field) {
			*field = layoutMainCloseRe.ReplaceAllString(*field, "")
			warnings = append(warnings, "Stripped </main> tag — the server wraps page content in <main> automatically")
		}
	}

	// Strip shared asset references (auto-injected by server).
	for _, field := range []*string{&headContent, &bodyBefore, &bodyAfter} {
		if layoutAssetLinkRe.MatchString(*field) {
			*field = layoutAssetLinkRe.ReplaceAllString(*field, "")
			warnings = append(warnings, "Stripped <link> for /assets/ — shared CSS is auto-injected by the server")
		}
		if layoutAssetSrcRe.MatchString(*field) {
			*field = layoutAssetSrcRe.ReplaceAllString(*field, "")
			warnings = append(warnings, "Stripped <script src='/assets/'> — shared JS is auto-injected by the server")
		}
	}

	// Advisory warnings.
	if bodyBefore != "" && !strings.Contains(strings.ToLower(bodyBefore), "<nav") {
		warnings = append(warnings, "Layout body_before_main has no <nav> element — consider adding navigation")
	}
	if bodyAfter != "" && !strings.Contains(strings.ToLower(bodyAfter), "<footer") {
		warnings = append(warnings, "Layout body_after_main has no <footer> element — consider adding a footer")
	}

	_, err := ctx.DB.Exec(
		`INSERT INTO layouts (name, head_content, body_before_main, body_after_main, updated_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(name) DO UPDATE SET
		   head_content = excluded.head_content,
		   body_before_main = excluded.body_before_main,
		   body_after_main = excluded.body_after_main,
		   updated_at = CURRENT_TIMESTAMP`,
		name, headContent, bodyBefore, bodyAfter,
	)
	if err != nil {
		return nil, fmt.Errorf("saving layout: %w", err)
	}

	resultData := map[string]interface{}{"name": name}
	if len(warnings) > 0 {
		resultData["warnings"] = warnings
	}
	return &Result{Success: true, Data: resultData}, nil
}

func (t *LayoutTool) get(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["name"].(string)
	if name == "" {
		name = "default"
	}

	var headContent, bodyBefore, bodyAfter string
	var createdAt, updatedAt string
	err := ctx.DB.QueryRow(
		"SELECT head_content, body_before_main, body_after_main, created_at, updated_at FROM layouts WHERE name = ?",
		name,
	).Scan(&headContent, &bodyBefore, &bodyAfter, &createdAt, &updatedAt)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("layout %q not found", name)}, nil
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"name":             name,
		"head_content":     headContent,
		"body_before_main": bodyBefore,
		"body_after_main":  bodyAfter,
		"created_at":       createdAt,
		"updated_at":       updatedAt,
	}}, nil
}

func (t *LayoutTool) list(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	rows, err := ctx.DB.Query("SELECT name, length(body_before_main) + length(body_after_main) AS size, created_at, updated_at FROM layouts ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("listing layouts: %w", err)
	}
	defer rows.Close()

	var layouts []map[string]interface{}
	for rows.Next() {
		var name, createdAt, updatedAt string
		var size int
		if rows.Scan(&name, &size, &createdAt, &updatedAt) == nil {
			layouts = append(layouts, map[string]interface{}{
				"name":       name,
				"size":       size,
				"created_at": createdAt,
				"updated_at": updatedAt,
			})
		}
	}
	return &Result{Success: true, Data: map[string]interface{}{
		"layouts": layouts,
		"count":   len(layouts),
	}}, nil
}
