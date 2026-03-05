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

		// Find classes used in HTML that aren't in CSS.
		var undefined []string
		for cls := range pageClasses {
			if !cssClasses[cls] {
				undefined = append(undefined, cls)
			}
		}

		// Report if >40% of classes are undefined (allows some semantic/utility classes).
		if len(undefined) > 0 && float64(len(undefined))/float64(len(pageClasses)) > 0.4 {
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
