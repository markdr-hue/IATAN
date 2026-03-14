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
)

// ---------------------------------------------------------------------------
// LayoutTool — manage site layouts (nav, footer, shared structure)
// ---------------------------------------------------------------------------

type LayoutTool struct{}

func (t *LayoutTool) Name() string { return "manage_layout" }
func (t *LayoutTool) Description() string {
	return "Save, get, list, or revert page layouts."
}

func (t *LayoutTool) Guide() string {
	return `### Layout System (manage_layout)
- head_content: extra HTML for <head> (fonts, meta tags). Shared CSS/JS auto-injected — don't duplicate.
- body_before_main: HTML before page content (nav, header). Server wraps page content in <main> automatically.
- body_after_main: HTML after page content (footer).
- "default" layout applies to all pages unless overridden per-page.
- Pages using layout="none" render content as-is without layout wrapping.
- patch action: apply targeted search/replace to a layout field without rewriting it. Use patches=[{"search":"old","replace":"new"}] and field=body_before_main|body_after_main|head_content.`
}

func (t *LayoutTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"save", "patch", "get", "list", "history", "revert"},
			},
			"version": map[string]interface{}{
				"type":        "integer",
				"description": "Version number to restore (revert action)",
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
			"patches": map[string]interface{}{
				"type":        "string",
				"description": `JSON array of search/replace pairs for patch action: [{"search":"old","replace":"new"}]. Applies to body_before_main by default, or specify field.`,
			},
			"field": map[string]interface{}{
				"type":        "string",
				"description": "Which layout field to patch: body_before_main (default), body_after_main, or head_content.",
				"enum":        []string{"body_before_main", "body_after_main", "head_content"},
			},
		},
		"required": []string{"action"},
	}
}

func (t *LayoutTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	return DispatchAction(ctx, args, map[string]ActionHandler{
		"save":    t.save,
		"patch":   t.patch,
		"get":     t.get,
		"list":    t.list,
		"history": t.history,
		"revert":  t.revert,
	}, func(a map[string]interface{}) string {
		if _, has := a["patches"]; has {
			return "patch"
		}
		if _, has := a["version"]; has {
			return "revert"
		}
		if _, has := a["body_before_main"]; has {
			return "save"
		}
		if _, has := a["body_after_main"]; has {
			return "save"
		}
		if _, has := a["head_content"]; has {
			return "save"
		}
		if _, has := a["name"]; has {
			return "get"
		}
		return "list"
	})
}

// Regexes for stripping content that shouldn't be in layout fields.
var (
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

	// Strip document shell tags and shared asset references from layout fields.
	for _, field := range []*string{&headContent, &bodyBefore, &bodyAfter} {
		if layoutDocShellRe.MatchString(*field) {
			*field = layoutDocShellRe.ReplaceAllString(*field, "")
			warnings = append(warnings, "Stripped <!DOCTYPE>/<html>/<head>/<body> tags — the server handles document structure")
		}
	}
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

	// Before upsert: capture existing layout into version history.
	var existingID int
	var oldHead, oldBefore, oldAfter, oldTemplate sql.NullString
	qErr := ctx.DB.QueryRow(
		"SELECT id, head_content, body_before_main, body_after_main, template FROM layouts WHERE name = ?", name,
	).Scan(&existingID, &oldHead, &oldBefore, &oldAfter, &oldTemplate)
	if qErr == nil {
		var maxVer int
		ctx.DB.QueryRow("SELECT COALESCE(MAX(version_number), 0) FROM layout_versions WHERE layout_id = ?", existingID).Scan(&maxVer)
		ctx.DB.Exec(
			`INSERT INTO layout_versions (layout_id, name, head_content, body_before_main, body_after_main, template, version_number, changed_by)
			 VALUES (?, ?, ?, ?, ?, ?, ?, 'brain')`,
			existingID, name, oldHead, oldBefore, oldAfter, oldTemplate, maxVer+1,
		)
	}

	// Always write NULL for template (template mode removed).
	_, err := ctx.DB.Exec(
		`INSERT INTO layouts (name, head_content, body_before_main, body_after_main, template, updated_at)
		 VALUES (?, ?, ?, ?, NULL, CURRENT_TIMESTAMP)
		 ON CONFLICT(name) DO UPDATE SET
		   head_content = excluded.head_content,
		   body_before_main = excluded.body_before_main,
		   body_after_main = excluded.body_after_main,
		   template = NULL,
		   updated_at = CURRENT_TIMESTAMP`,
		name, headContent, bodyBefore, bodyAfter,
	)
	if err != nil {
		return nil, fmt.Errorf("saving layout: %w", err)
	}

	// Tell the LLM what this layout provides so pages don't duplicate nav/header/footer.
	lowerBefore := strings.ToLower(bodyBefore)
	lowerAfter := strings.ToLower(bodyAfter)
	resultData := map[string]interface{}{
		"name": name,
		"layout_provides": map[string]interface{}{
			"has_nav":    strings.Contains(lowerBefore, "<nav"),
			"has_header": strings.Contains(lowerBefore, "<header"),
			"has_footer": strings.Contains(lowerAfter, "<footer"),
			"note":       "Pages go inside <main> — do not duplicate nav/header/footer in page content.",
		},
	}
	if len(warnings) > 0 {
		resultData["warnings"] = warnings
	}
	return &Result{Success: true, Data: resultData}, nil
}

// ---------------------------------------------------------------------------
// patch — apply search/replace pairs to a layout field without full rewrite
// ---------------------------------------------------------------------------

func (t *LayoutTool) patch(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["name"].(string)
	if name == "" {
		name = "default"
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

	field, _ := args["field"].(string)
	if field == "" {
		field = "body_before_main"
	}

	// Load the existing layout.
	var layoutID int
	var headContent, bodyBefore, bodyAfter sql.NullString
	var oldTemplate sql.NullString
	err := ctx.DB.QueryRow(
		"SELECT id, head_content, body_before_main, body_after_main, template FROM layouts WHERE name = ?", name,
	).Scan(&layoutID, &headContent, &bodyBefore, &bodyAfter, &oldTemplate)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("layout %q not found", name)}, nil
	}

	// Save version history before modifying.
	var maxVer int
	ctx.DB.QueryRow("SELECT COALESCE(MAX(version_number), 0) FROM layout_versions WHERE layout_id = ?", layoutID).Scan(&maxVer)
	ctx.DB.Exec(
		`INSERT INTO layout_versions (layout_id, name, head_content, body_before_main, body_after_main, template, version_number, changed_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'brain')`,
		layoutID, name, headContent, bodyBefore, bodyAfter, oldTemplate, maxVer+1,
	)

	// Select the target field content.
	var target string
	switch field {
	case "body_before_main":
		target = bodyBefore.String
	case "body_after_main":
		target = bodyAfter.String
	case "head_content":
		target = headContent.String
	default:
		return &Result{Success: false, Error: fmt.Sprintf("invalid field %q — use body_before_main, body_after_main, or head_content", field)}, nil
	}

	// Apply patches.
	var applied, notFound []string
	for _, p := range patches {
		if p.Search == "" {
			continue
		}
		if !strings.Contains(target, p.Search) {
			notFound = append(notFound, p.Search)
			continue
		}
		target = strings.ReplaceAll(target, p.Search, p.Replace)
		label := p.Search
		if len(label) > 60 {
			label = label[:60] + "..."
		}
		applied = append(applied, label)
	}

	if len(applied) == 0 && len(notFound) > 0 {
		return &Result{Success: false, Error: "no patches matched", Data: map[string]interface{}{"not_found": notFound}}, nil
	}

	// Write back the patched field.
	_, err = ctx.DB.Exec(
		fmt.Sprintf("UPDATE layouts SET %s = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", field),
		target, layoutID,
	)
	if err != nil {
		return nil, fmt.Errorf("saving patched layout: %w", err)
	}

	resultData := map[string]interface{}{
		"name":    name,
		"field":   field,
		"applied": len(applied),
	}
	if len(notFound) > 0 {
		resultData["not_found"] = notFound
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

	data := map[string]interface{}{
		"name":             name,
		"head_content":     headContent,
		"body_before_main": bodyBefore,
		"body_after_main":  bodyAfter,
		"created_at":       createdAt,
		"updated_at":       updatedAt,
	}

	return &Result{Success: true, Data: data}, nil
}

func (t *LayoutTool) list(ctx *ToolContext, _ map[string]interface{}) (*Result, error) {
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

// ---------------------------------------------------------------------------
// history — view version history for a layout
// ---------------------------------------------------------------------------

func (t *LayoutTool) history(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["name"].(string)
	if name == "" {
		name = "default"
	}

	var layoutID int
	err := ctx.DB.QueryRow("SELECT id FROM layouts WHERE name = ?", name).Scan(&layoutID)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("layout %q not found", name)}, nil
	}

	rows, err := ctx.DB.Query(
		"SELECT version_number, changed_by, created_at FROM layout_versions WHERE layout_id = ? ORDER BY version_number DESC LIMIT 20",
		layoutID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying layout history: %w", err)
	}
	defer rows.Close()

	var versions []map[string]interface{}
	for rows.Next() {
		var ver int
		var changedBy sql.NullString
		var createdAt string
		if rows.Scan(&ver, &changedBy, &createdAt) == nil {
			versions = append(versions, map[string]interface{}{
				"version":    ver,
				"changed_by": changedBy.String,
				"created_at": createdAt,
			})
		}
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"name":     name,
		"versions": versions,
	}}, nil
}

// ---------------------------------------------------------------------------
// revert — restore a layout to a previous version
// ---------------------------------------------------------------------------

func (t *LayoutTool) revert(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["name"].(string)
	if name == "" {
		name = "default"
	}

	version, ok := args["version"].(float64)
	if !ok || version < 1 {
		return &Result{Success: false, Error: "version (number) is required"}, nil
	}

	// Find the layout.
	var layoutID int
	var oldHead, oldBefore, oldAfter, oldTemplate sql.NullString
	err := ctx.DB.QueryRow(
		"SELECT id, head_content, body_before_main, body_after_main, template FROM layouts WHERE name = ?", name,
	).Scan(&layoutID, &oldHead, &oldBefore, &oldAfter, &oldTemplate)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("layout %q not found", name)}, nil
	}

	// Load the requested version.
	var verHead, verBefore, verAfter sql.NullString
	err = ctx.DB.QueryRow(
		"SELECT head_content, body_before_main, body_after_main FROM layout_versions WHERE layout_id = ? AND version_number = ?",
		layoutID, int(version),
	).Scan(&verHead, &verBefore, &verAfter)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("version %d not found for layout %q", int(version), name)}, nil
	}

	// Save current state as a new version (so revert is reversible).
	var maxVer int
	ctx.DB.QueryRow("SELECT COALESCE(MAX(version_number), 0) FROM layout_versions WHERE layout_id = ?", layoutID).Scan(&maxVer)
	ctx.DB.Exec(
		`INSERT INTO layout_versions (layout_id, name, head_content, body_before_main, body_after_main, template, version_number, changed_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'revert')`,
		layoutID, name, oldHead, oldBefore, oldAfter, oldTemplate, maxVer+1,
	)

	// Restore the old version (template always NULL for new saves).
	_, err = ctx.DB.Exec(
		"UPDATE layouts SET head_content = ?, body_before_main = ?, body_after_main = ?, template = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		verHead, verBefore, verAfter, layoutID,
	)
	if err != nil {
		return nil, fmt.Errorf("reverting layout: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"name":             name,
		"restored_version": int(version),
	}}, nil
}

func (t *LayoutTool) MaxResultSize() int { return 16000 }

func (t *LayoutTool) Summarize(result string) string {
	r, data, _, ok := parseSummaryResult(result)
	if !ok {
		return summarizeTruncate(result, 200)
	}
	if !r.Success {
		return summarizeError(r.Error)
	}
	if data == nil {
		return summarizeTruncate(result, 300)
	}
	name, _ := data["name"].(string)
	// Preserve layout_provides and warnings for the LLM context.
	var parts []string
	parts = append(parts, fmt.Sprintf(`"success":true,"layout":"%s"`, name))
	if provides, ok := data["layout_provides"]; ok {
		pJSON, _ := json.Marshal(provides)
		parts = append(parts, fmt.Sprintf(`"layout_provides":%s`, pJSON))
	}
	if warnings, ok := data["warnings"]; ok {
		wJSON, _ := json.Marshal(warnings)
		parts = append(parts, fmt.Sprintf(`"warnings":%s`, wJSON))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
