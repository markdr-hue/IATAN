/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package brain

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var internalHrefRe = regexp.MustCompile(`href=["'](/[^"'#]*?)["']`)
var scriptTagRe = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)

// stripScripts removes <script> blocks so link validation ignores JS-generated hrefs.
func stripScripts(html string) string {
	return scriptTagRe.ReplaceAllString(html, "")
}

// validateSite runs all Go-based validation checks on a site.
// Returns a list of issues found (empty = all OK).
func validateSite(db *sql.DB) []string {
	var issues []string

	issues = append(issues, validateLinks(db)...)
	issues = append(issues, validateNav(db)...)
	issues = append(issues, validateAssets(db)...)
	issues = append(issues, validateStructure(db)...)
	issues = append(issues, validateEssentialPages(db)...)
	issues = append(issues, validateCSSAlignment(db)...)
	issues = append(issues, validateEndpoints(db)...)
	issues = append(issues, validateHeadings(db)...)
	issues = append(issues, validateContentLength(db)...)
	issues = append(issues, validateMetadata(db)...)
	issues = append(issues, validateSections(db)...)

	return issues
}

// validateLinks checks that all internal links in pages point to existing pages.
func validateLinks(db *sql.DB) []string {
	var issues []string

	rows, err := db.Query("SELECT path, content FROM pages WHERE is_deleted = 0 AND content IS NOT NULL")
	if err != nil {
		return nil
	}
	defer rows.Close()

	// Build set of existing paths.
	existingPaths := loadExistingPaths(db)

	for rows.Next() {
		var path, content string
		if rows.Scan(&path, &content) != nil {
			continue
		}

		clean := stripScripts(content)
		matches := internalHrefRe.FindAllStringSubmatch(clean, -1)
		seen := map[string]bool{}
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			target := m[1]
			// Skip non-page paths.
			if strings.HasPrefix(target, "/assets/") ||
				strings.HasPrefix(target, "/api/") ||
				strings.HasPrefix(target, "/files/") ||
				target == path {
				continue
			}
			// Skip dynamic/JS template literal targets.
			if strings.Contains(target, "${") || strings.HasSuffix(target, "=") || strings.HasSuffix(target, "?") {
				continue
			}
			if seen[target] {
				continue
			}
			seen[target] = true
			if !existingPaths[target] {
				issues = append(issues, fmt.Sprintf("Page %s links to %s which does not exist", path, target))
			}
		}
	}

	return issues
}

// validateNav checks that all links in the layout nav point to existing pages.
func validateNav(db *sql.DB) []string {
	var issues []string

	var navHTML sql.NullString
	db.QueryRow("SELECT body_before_main FROM layouts WHERE name = 'default'").Scan(&navHTML)
	if !navHTML.Valid || navHTML.String == "" {
		return nil
	}

	existingPaths := loadExistingPaths(db)

	clean := stripScripts(navHTML.String)
	matches := internalHrefRe.FindAllStringSubmatch(clean, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		target := m[1]
		if strings.HasPrefix(target, "/assets/") || strings.HasPrefix(target, "/api/") {
			continue
		}
		// Skip dynamic/JS template literal targets.
		if strings.Contains(target, "${") || strings.HasSuffix(target, "=") || strings.HasSuffix(target, "?") {
			continue
		}
		if !existingPaths[target] {
			issues = append(issues, fmt.Sprintf("Nav links to %s which does not exist", target))
		}
	}

	return issues
}

// validateAssets checks that page-scoped assets referenced by pages exist.
func validateAssets(db *sql.DB) []string {
	var issues []string

	rows, err := db.Query("SELECT path, assets FROM pages WHERE is_deleted = 0 AND assets IS NOT NULL AND assets != '[]' AND assets != ''")
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var path, assetsJSON string
		if rows.Scan(&path, &assetsJSON) != nil {
			continue
		}
		var assetList []string
		if err := json.Unmarshal([]byte(assetsJSON), &assetList); err != nil {
			continue
		}
		for _, asset := range assetList {
			var exists int
			db.QueryRow("SELECT COUNT(*) FROM assets WHERE filename = ?", asset).Scan(&exists)
			if exists == 0 {
				issues = append(issues, fmt.Sprintf("Page %s references asset %q which does not exist", path, asset))
			}
		}
	}

	return issues
}

// validateStructure checks that page content doesn't contain layout-level elements.
func validateStructure(db *sql.DB) []string {
	var issues []string

	rows, err := db.Query("SELECT path, content FROM pages WHERE is_deleted = 0 AND content IS NOT NULL")
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var path, content string
		if rows.Scan(&path, &content) != nil {
			continue
		}
		lower := strings.ToLower(content)

		if strings.Contains(lower, "<!doctype") {
			issues = append(issues, fmt.Sprintf("Page %s contains <!DOCTYPE> — pages should be main-content only", path))
		}
		if strings.Contains(lower, "<html") {
			issues = append(issues, fmt.Sprintf("Page %s contains <html> tag — pages should be main-content only", path))
		}
		if strings.Contains(lower, "<nav") {
			issues = append(issues, fmt.Sprintf("Page %s contains <nav> — navigation belongs in the layout", path))
		}
		if strings.Contains(lower, "<footer") {
			issues = append(issues, fmt.Sprintf("Page %s contains <footer> — footer belongs in the layout", path))
		}
	}

	return issues
}

// validateEssentialPages checks for required pages.
func validateEssentialPages(db *sql.DB) []string {
	var issues []string

	// Check for homepage.
	var homeExists int
	db.QueryRow("SELECT COUNT(*) FROM pages WHERE path = '/' AND is_deleted = 0").Scan(&homeExists)
	if homeExists == 0 {
		issues = append(issues, "Missing homepage at path /")
	}

	// Check for 404 page.
	var notFoundExists int
	db.QueryRow("SELECT COUNT(*) FROM pages WHERE path = '/404' AND is_deleted = 0").Scan(&notFoundExists)
	if notFoundExists == 0 {
		issues = append(issues, "Missing 404 page at path /404")
	}

	return issues
}

// loadExistingPaths returns a set of all non-deleted page paths.
func loadExistingPaths(db *sql.DB) map[string]bool {
	paths := make(map[string]bool)
	rows, err := db.Query("SELECT path FROM pages WHERE is_deleted = 0")
	if err != nil {
		return paths
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if rows.Scan(&path) == nil {
			paths[path] = true
		}
	}
	return paths
}

// CSS-HTML alignment validation regexes.
var (
	// Matches CSS class selectors like .container, .hero-section, .btn-primary
	// Skips pseudo-classes (::before, :hover) and media queries.
	cssSelectorRe = regexp.MustCompile(`\.([a-zA-Z_][\w-]*)(?:\s*[{,:\[>\+~\s])`)
	// Matches class="..." and class='...' in HTML.
	htmlClassAttrRe = regexp.MustCompile(`class\s*=\s*["']([^"']+)["']`)
)

// validateCSSAlignment checks that CSS classes used in HTML pages are defined
// in the global stylesheet. Reports pages where most classes are undefined.
func validateCSSAlignment(db *sql.DB) []string {
	// Load global CSS from disk.
	var storagePath sql.NullString
	db.QueryRow(
		"SELECT storage_path FROM assets WHERE scope = 'global' AND filename LIKE '%.css' ORDER BY filename LIMIT 1",
	).Scan(&storagePath)
	if !storagePath.Valid || storagePath.String == "" {
		return nil // No CSS file to validate against.
	}
	cssData, err := os.ReadFile(storagePath.String)
	if err != nil {
		return nil
	}
	cssContent := string(cssData)

	// Extract all class names defined in CSS.
	cssClasses := make(map[string]bool)
	for _, m := range cssSelectorRe.FindAllStringSubmatch(cssContent, -1) {
		if len(m) > 1 {
			cssClasses[m[1]] = true
		}
	}
	if len(cssClasses) == 0 {
		return []string{"Global CSS file has no class selectors — pages will be unstyled"}
	}

	// Check each page's HTML class usage against CSS definitions.
	var issues []string
	rows, err := db.Query("SELECT path, content FROM pages WHERE is_deleted = 0 AND content IS NOT NULL")
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var path, content string
		if rows.Scan(&path, &content) != nil {
			continue
		}

		// Extract all class names used in the page HTML.
		pageClasses := make(map[string]bool)
		for _, m := range htmlClassAttrRe.FindAllStringSubmatch(content, -1) {
			if len(m) < 2 {
				continue
			}
			// Split space-separated classes: class="hero container dark"
			for _, cls := range strings.Fields(m[1]) {
				pageClasses[cls] = true
			}
		}
		if len(pageClasses) == 0 {
			continue
		}

		// Common utility/state classes toggled by JS — don't need CSS definitions.
		skipClasses := map[string]bool{
			"active": true, "hidden": true, "open": true, "closed": true,
			"disabled": true, "loading": true, "error": true, "selected": true,
			"visible": true, "collapsed": true, "expanded": true, "show": true,
			"fade-in": true, "fade-out": true,
		}

		// Find classes used in HTML that aren't in CSS.
		var undefined []string
		for cls := range pageClasses {
			if !cssClasses[cls] && !skipClasses[cls] {
				undefined = append(undefined, cls)
			}
		}

		// Report if >60% of classes are undefined (allows some semantic/utility classes).
		if len(undefined) > 0 && float64(len(undefined))/float64(len(pageClasses)) > 0.6 {
			// Limit to 5 example classes to keep issue text compact.
			examples := undefined
			if len(examples) > 5 {
				examples = examples[:5]
			}
			issues = append(issues, fmt.Sprintf(
				"Page %s uses %d CSS classes not defined in stylesheet (e.g. %s) — update HTML to use classes from the global CSS",
				path, len(undefined), strings.Join(examples, ", "),
			))
		}
	}

	return issues
}

// validateEndpoints checks that API endpoints reference tables that still exist.
func validateEndpoints(db *sql.DB) []string {
	var issues []string

	rows, err := db.Query("SELECT path, table_name FROM api_endpoints")
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var path, tableName string
		if rows.Scan(&path, &tableName) != nil {
			continue
		}
		var exists int
		db.QueryRow("SELECT COUNT(*) FROM dynamic_tables WHERE table_name = ?", tableName).Scan(&exists)
		if exists == 0 {
			issues = append(issues, fmt.Sprintf("API endpoint /%s references table %q which no longer exists", path, tableName))
		}
	}

	// Also check auth endpoints.
	authRows, err := db.Query("SELECT path, table_name FROM auth_endpoints")
	if err != nil {
		return issues
	}
	defer authRows.Close()

	for authRows.Next() {
		var path, tableName string
		if authRows.Scan(&path, &tableName) != nil {
			continue
		}
		var exists int
		db.QueryRow("SELECT COUNT(*) FROM dynamic_tables WHERE table_name = ?", tableName).Scan(&exists)
		if exists == 0 {
			issues = append(issues, fmt.Sprintf("Auth endpoint /%s references table %q which no longer exists", path, tableName))
		}
	}

	return issues
}

// Heading tag regex for hierarchy validation.
var headingTagRe = regexp.MustCompile(`(?i)<h([1-6])[\s>]`)

// validateHeadings checks heading hierarchy in page content.
// The layout typically provides h1, so we only flag multiple h1s and skipped levels.
func validateHeadings(db *sql.DB) []string {
	var issues []string

	// Check if the layout already provides an h1.
	var layoutHTML sql.NullString
	db.QueryRow("SELECT body_before_main FROM layouts WHERE name = 'default'").Scan(&layoutHTML)
	layoutHasH1 := layoutHTML.Valid && headingTagRe.MatchString(layoutHTML.String) &&
		strings.Contains(strings.ToLower(layoutHTML.String), "<h1")

	rows, err := db.Query("SELECT path, content FROM pages WHERE is_deleted = 0 AND content IS NOT NULL")
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var path, content string
		if rows.Scan(&path, &content) != nil {
			continue
		}

		clean := stripScripts(content)
		matches := headingTagRe.FindAllStringSubmatch(clean, -1)
		if len(matches) == 0 {
			continue
		}

		h1Count := 0
		var levels []int
		for _, m := range matches {
			level, _ := strconv.Atoi(m[1])
			levels = append(levels, level)
			if level == 1 {
				h1Count++
			}
		}

		// Only flag multiple h1s. Missing h1 is OK if layout provides one.
		if h1Count > 1 {
			issues = append(issues, fmt.Sprintf("Page %s has %d <h1> headings (should have exactly one)", path, h1Count))
		} else if h1Count == 0 && !layoutHasH1 {
			issues = append(issues, fmt.Sprintf("Page %s has no <h1> heading", path))
		}

		// Check for skipped levels (e.g. h2 → h4 without h3).
		seen := make(map[int]bool)
		for _, l := range levels {
			seen[l] = true
		}
		minLevel := levels[0]
		maxLevel := levels[0]
		for _, l := range levels {
			if l < minLevel {
				minLevel = l
			}
			if l > maxLevel {
				maxLevel = l
			}
		}
		for l := minLevel + 1; l <= maxLevel; l++ {
			if !seen[l] {
				issues = append(issues, fmt.Sprintf("Page %s skips heading level h%d (has h%d and h%d)", path, l, l-1, l+1))
				break
			}
		}
	}

	return issues
}

// htmlTagStripRe strips HTML tags for plain text extraction.
var htmlTagStripRe = regexp.MustCompile(`<[^>]*>`)

// validateContentLength warns about pages with very little visible text content.
func validateContentLength(db *sql.DB) []string {
	var issues []string

	rows, err := db.Query("SELECT path, content FROM pages WHERE is_deleted = 0 AND content IS NOT NULL")
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var path, content string
		if rows.Scan(&path, &content) != nil {
			continue
		}

		// Skip pages that are intentionally short.
		if path == "/404" || path == "/contact" || path == "/login" || path == "/register" {
			continue
		}

		// Strip scripts, then all HTML tags, then collapse whitespace.
		text := stripScripts(content)
		text = htmlTagStripRe.ReplaceAllString(text, " ")
		text = strings.Join(strings.Fields(text), " ")

		if len(text) < 100 {
			issues = append(issues, fmt.Sprintf("Page %s has very little content (%d chars of visible text)", path, len(text)))
		}
	}

	return issues
}

// validateMetadata checks that pages have valid meta descriptions.
// Only flags descriptions that exist but have wrong length. Missing descriptions
// are common during initial build and would create too many issues.
func validateMetadata(db *sql.DB) []string {
	var issues []string

	rows, err := db.Query("SELECT path, metadata FROM pages WHERE is_deleted = 0 AND metadata IS NOT NULL AND metadata != '' AND metadata != '{}'")
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var path, metadataJSON string
		if rows.Scan(&path, &metadataJSON) != nil {
			continue
		}

		var meta map[string]interface{}
		if json.Unmarshal([]byte(metadataJSON), &meta) != nil {
			continue
		}

		desc, _ := meta["description"].(string)
		desc = strings.TrimSpace(desc)
		if len(desc) > 0 && len(desc) < 50 {
			issues = append(issues, fmt.Sprintf("Page %s meta description is too short (%d chars, recommend 50-160)", path, len(desc)))
		} else if len(desc) > 160 {
			issues = append(issues, fmt.Sprintf("Page %s meta description is too long (%d chars, recommend 50-160)", path, len(desc)))
		}
	}

	return issues
}

// sectionTagRe matches <section> tags in HTML content.
var sectionTagRe = regexp.MustCompile(`(?i)<section[\s>]`)

// validateSections checks that pages contain roughly the number of sections
// planned in the SitePlan. Loads the plan from pipeline_state.
func validateSections(db *sql.DB) []string {
	var planJSON sql.NullString
	db.QueryRow("SELECT plan_json FROM pipeline_state WHERE id = 1").Scan(&planJSON)
	if !planJSON.Valid || planJSON.String == "" {
		return nil
	}

	var plan SitePlan
	if json.Unmarshal([]byte(planJSON.String), &plan) != nil {
		return nil
	}

	var issues []string
	for _, pagePlan := range plan.Pages {
		if len(pagePlan.Sections) < 2 {
			continue // too few planned sections to validate meaningfully
		}

		var content sql.NullString
		db.QueryRow("SELECT content FROM pages WHERE path = ? AND is_deleted = 0", pagePlan.Path).Scan(&content)
		if !content.Valid || content.String == "" {
			continue
		}

		sectionCount := len(sectionTagRe.FindAllString(content.String, -1))
		planned := len(pagePlan.Sections)

		// Allow flexibility: warn only if significantly fewer sections than planned.
		if sectionCount < planned-1 {
			issues = append(issues, fmt.Sprintf("Page %s has %d sections but %d were planned (%s)",
				pagePlan.Path, sectionCount, planned, strings.Join(pagePlan.Sections, ", ")))
		}
	}

	return issues
}

// loadGlobalCSS reads the global CSS file content from disk for use in prompts.
func loadGlobalCSS(db *sql.DB) string {
	var storagePath sql.NullString
	db.QueryRow(
		"SELECT storage_path FROM assets WHERE scope = 'global' AND filename LIKE '%.css' ORDER BY filename LIMIT 1",
	).Scan(&storagePath)
	if !storagePath.Valid || storagePath.String == "" {
		return ""
	}
	data, err := os.ReadFile(storagePath.String)
	if err != nil {
		return ""
	}
	return string(data)
}
