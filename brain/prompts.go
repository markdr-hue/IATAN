/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package brain

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/markdr-hue/IATAN/db/models"
)

// --- PLAN stage prompt ---

func buildPlanPrompt(site *models.Site, ownerName, answers string) string {
	var b strings.Builder

	b.WriteString("You are IATAN, an AI that plans websites. Respond with ONLY a JSON object — no markdown, no explanation.\n\n")

	if ownerName != "" {
		b.WriteString(fmt.Sprintf("Site owner: %s\n", ownerName))
	}
	b.WriteString(fmt.Sprintf("Date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	b.WriteString("## Site Info\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
	if site.Description != nil && *site.Description != "" {
		b.WriteString(fmt.Sprintf("- Description: %s\n", *site.Description))
	}
	if site.Direction != nil && *site.Direction != "" {
		b.WriteString(fmt.Sprintf("- Owner Direction: %s\n", *site.Direction))
	}
	b.WriteString("\n")

	if answers != "" {
		b.WriteString("## Owner's Answers to Your Questions\n")
		b.WriteString(answers + "\n\n")
	}

	b.WriteString(`## Instructions

Create a complete site plan as a JSON object with this exact structure:

{
  "architecture": "spa" or "multi-page",
  "color_scheme": {
    "primary": "#hex", "secondary": "#hex", "accent": "#hex",
    "background": "#hex", "surface": "#hex", "text": "#hex", "text_muted": "#hex"
  },
  "typography": {
    "heading_font": "Font Name", "body_font": "Font Name", "scale": "1.25"
  },
  "pages": [
    {
      "path": "/",
      "title": "Page Title",
      "purpose": "Brief description of what this page does",
      "sections": ["hero", "features", "cta"],
      "links_to": ["/about", "/services"],
      "needs_data": false,
      "data_tables": [],
      "page_assets": [],
      "layout": "default"
    }
  ],
  "needs_data_layer": false,
  "data_tables": [
    {
      "name": "products",
      "columns": [
        {"name": "title", "type": "TEXT"},
        {"name": "price", "type": "REAL"},
        {"name": "description", "type": "TEXT"}
      ],
      "has_api": true,
      "has_auth": false,
      "seed_data": true
    }
  ],
  "nav_items": ["/", "/about", "/services", "/contact"],
  "design_notes": "Brief notes about the design approach",
  "questions": []
}

## Rules

1. ALWAYS include a homepage at path "/" and a 404 page at "/404"
2. Architecture: use "spa" for app-like sites, "multi-page" for content sites
3. Choose colors that work well together — use analogous, complementary, or triadic schemes
4. Choose 1-2 Google Fonts that complement each other
5. "scale" is the modular type scale ratio (1.125=minor second, 1.25=major third, 1.333=perfect fourth)
6. Each page should have 2-6 meaningful sections (not just "content")
7. "links_to" should reference paths of other pages in the plan
8. "nav_items" is the ordered list of page paths for the main navigation (exclude /404)
9. If the site needs dynamic data (products, blog posts, user accounts), set "needs_data_layer": true and define tables
10. For data_tables, each column is {"name": "col_name", "type": "TYPE"}. Types: TEXT, INTEGER, REAL, BOOLEAN, PASSWORD, ENCRYPTED. Optional: "primary": true, "required": true
11. Set "data_tables": [] and "needs_data_layer": false if the site doesn't need dynamic data
12. Each page's "data_tables" is an array of table names the page needs (e.g. ["articles", "comments"]). A page can use multiple tables.
13. Each page can specify a "layout" name. Options: "default" (standard nav+footer), "none" (no layout — for landing pages), or a custom name like "blog" (with sidebar). Most pages should use "default". The DESIGN stage will create all referenced layouts.
14. If the site needs payments (e-commerce, donations, subscriptions), set "needs_data_layer": true — the DATA_LAYER stage will configure the payment provider.
15. If the site needs file uploads (profile pics, attachments, form submissions), set "needs_data_layer": true — the DATA_LAYER stage will create upload endpoints.
16. If the site needs real-time updates (chat, live feeds, notifications), set "needs_data_layer": true — the DATA_LAYER stage will create SSE stream endpoints.

## Vague Descriptions

If the site description is too vague to make good design decisions (e.g., just "a website" with no details), add questions to the "questions" array:

{
  "questions": [
    {"question": "What is the purpose of this website?", "options": ["Portfolio", "Business", "Blog", "E-commerce", "Landing page"]},
    {"question": "What style do you prefer?", "options": ["Modern/minimal", "Bold/colorful", "Professional/corporate", "Creative/artistic"]}
  ]
}

Keep questions to 2-3 max. Only ask if truly needed — if you have enough info, produce the plan directly.
`)

	return b.String()
}

// --- DESIGN stage prompt ---

func buildDesignPrompt(plan *SitePlan, site *models.Site) string {
	var b strings.Builder

	b.WriteString("You are IATAN, an AI that creates design systems for websites. Use the tools to create all shared assets.\n\n")
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	b.WriteString("## Site Plan\n```json\n")
	b.Write(planJSON)
	b.WriteString("\n```\n\n")

	b.WriteString(`## Your Tasks

1. CREATE GLOBAL CSS (manage_files, action="save", storage="assets", scope="global"):
   - File: css/styles.css (or similar)
   - Include CSS custom properties from the plan's color_scheme and typography:
     --primary, --secondary, --accent, --bg, --surface, --text, --text-muted
     --font-heading, --font-body, --scale
   - Include a CSS reset/normalize
   - Base typography styles using the scale
   - Utility classes for common patterns (containers, grids, cards, buttons)
   - Mobile-first responsive design with breakpoints
   - Dark-first color scheme using the planned colors

2. CREATE LAYOUTS (manage_layout, action="save"):
   - Create the "default" layout (name="default"):
     - body_before_main: Skip-to-content link + <nav> with links from nav_items
     - body_after_main: <footer> with site info
     - head_content: Google Fonts import if using web fonts
   - If any page uses layout="none", no extra layout is needed (pages with layout "none" render without nav/footer)
   - If any page uses a custom layout name (e.g. "blog"), create that layout too with manage_layout(action="save", name="blog")
     - Custom layouts should maintain the same design language but adapt structure (e.g. sidebar, minimal header)
   - The server wraps page content in <main>...</main> automatically
   - Do NOT include <main> tags or shared asset tags in layouts

`)

	// List unique layouts referenced by pages (excluding "default" and "none").
	layoutSet := make(map[string]bool)
	for _, page := range plan.Pages {
		if page.Layout != "" && page.Layout != "default" && page.Layout != "none" {
			layoutSet[page.Layout] = true
		}
	}
	if len(layoutSet) > 0 {
		b.WriteString("   Custom layouts to create: ")
		first := true
		for layout := range layoutSet {
			if !first {
				b.WriteString(", ")
			}
			b.WriteString(fmt.Sprintf(`"%s"`, layout))
			first = false
		}
		b.WriteString("\n\n")
	}

	if plan.Architecture == "spa" {
		b.WriteString(`3. CREATE SPA ROUTER (manage_files, action="save", storage="assets", scope="global"):
   - File: js/router.js
   - Intercept same-origin <a> clicks (skip external, #anchors, ctrl/meta+click)
   - Fetch JSON: fetch('/api/page?path=' + encodeURIComponent(href))
   - Swap: document.querySelector('main').innerHTML = response.content
   - Handle page-scoped assets: remove [data-page-asset], load response.page_css + page_js
   - Evaluate inline <script> tags in new content (create new script elements, do NOT set defer)
   - history.pushState() + popstate handler
   - Update document.title from response.title
   IMPORTANT: When dynamically loading page_js scripts, do NOT set script.defer = true.
   Dynamically appended scripts should execute immediately so page initialization runs.

   Include a global state manager at the TOP of router.js (before the router logic):
   window.IATAN = {
     state: {},
     _listeners: {},
     set: function(key, value) { this.state[key] = value; this.emit(key, value); },
     get: function(key) { return this.state[key]; },
     on: function(key, fn) { (this._listeners[key] = this._listeners[key] || []).push(fn); },
     off: function(key, fn) { this._listeners[key] = (this._listeners[key]||[]).filter(function(f){return f!==fn;}); },
     emit: function(key, value) { (this._listeners[key]||[]).forEach(function(fn){ fn(value); }); }
   };
   Pages use IATAN.set('auth:token', token) after login and IATAN.on('auth:token', fn) to react.
   On navigation, emit IATAN.emit('navigate', newPath) so pages can clean up listeners.

`)
	}

	// Task numbering adjusts based on whether SPA router was task 3.
	nextTask := 3
	if plan.Architecture == "spa" {
		nextTask = 4
	}

	b.WriteString(fmt.Sprintf(`%d. CREATE DECORATIVE SVGs (manage_files, action="save", storage="assets", scope="page"):
   - Create 2-3 simple, abstract SVG illustrations for hero sections and feature highlights
   - Use CSS custom properties for colors so they match the theme: fill="var(--primary)", stroke="var(--accent)"
   - Keep SVGs small (< 3KB each) — abstract shapes, geometric patterns, decorative icons
   - Name them descriptively: svg/hero-decoration.svg, svg/feature-icon-1.svg
   - Pages can reference these using inline SVG or <img src="/assets/svg/...">
   - Focus on decorative, abstract designs (waves, circles, grid patterns, abstract shapes)

`, nextTask))
	nextTask++

	b.WriteString(fmt.Sprintf(`%d. STORE DESIGN DECISIONS in memory:`, nextTask))
	b.WriteString(`
   - manage_memory(action="remember", key="site_architecture", value="` + plan.Architecture + `")
   - manage_memory(action="remember", key="site_blueprint", value="<brief design summary>")

## Asset Scoping Rules
- scope="global": auto-injected on every page by the server (CSS in <head>, JS at body end)
- scope="page": only injected when a page lists the filename in its assets param
- Pages contain ONLY <main> content — no nav, footer, or shared asset tags

## Quality Standards
- Use semantic HTML (nav, header, main, section, article, aside, footer)
- Mobile-first CSS (min-width media queries)
- Accessible: skip-to-content link, ARIA labels, focus styles, color contrast
- No inline styles — everything in CSS files
- Images: use inline SVGs or CSS shapes, no external hotlinks
`)

	return b.String()
}

// --- DATA_LAYER stage prompt ---

func buildDataLayerPrompt(plan *SitePlan, site *models.Site) string {
	var b strings.Builder

	b.WriteString("You are IATAN, an AI that creates database schemas and API endpoints. Use the tools to create all tables and endpoints.\n\n")

	// Site context so seed data is relevant to the site's topic.
	if site != nil {
		b.WriteString("## Site Context\n")
		b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
		if site.Description != nil && *site.Description != "" {
			b.WriteString(fmt.Sprintf("- About: %s\n", *site.Description))
		}
		if plan.DesignNotes != "" {
			b.WriteString(fmt.Sprintf("- Design direction: %s\n", plan.DesignNotes))
		}
		b.WriteString("\nIMPORTANT: All seed data MUST be relevant to this site's topic. Do NOT use generic placeholder content.\n\n")
	}

	var authProviderTables []string // tables with PASSWORD column → need create_auth
	var authProtectedTables []string // tables without PASSWORD column but HasAuth → need requires_auth on API

	b.WriteString("## Data Tables to Create\n\n")
	for _, t := range plan.DataTables {
		colJSON, _ := json.Marshal(t.Columns)
		b.WriteString(fmt.Sprintf("### Table: %s\n", t.Name))
		b.WriteString(fmt.Sprintf("Columns: %s\n", string(colJSON)))
		if t.HasAPI {
			b.WriteString("Needs API endpoint: yes\n")
		}
		if t.HasAuth {
			hasPasswordCol := false
			for _, col := range t.Columns {
				if strings.EqualFold(col.Type, "PASSWORD") {
					hasPasswordCol = true
					break
				}
			}
			if hasPasswordCol {
				b.WriteString("**Needs auth endpoint (create_auth): YES** — this table has a PASSWORD column\n")
				authProviderTables = append(authProviderTables, t.Name)
			} else {
				b.WriteString("Needs auth protection: yes — set requires_auth=true on its API endpoint\n")
				authProtectedTables = append(authProtectedTables, t.Name)
			}
		}
		if t.SeedData {
			b.WriteString("Needs seed data: yes (use bulk insert with rows parameter)\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(`## Instructions

1. Create each table using manage_schema(action="create")
   - Column types: TEXT, INTEGER, REAL, BOOLEAN, PASSWORD (bcrypt), ENCRYPTED (AES)
   - id and created_at are auto-added

2. Create API endpoints using manage_endpoints(action="create_api")
   - Set public_columns to exclude sensitive fields
   - For auth-protected tables, set requires_auth=true
   - Example: manage_endpoints(action="create_api", path="products", table_name="products", public_columns=["id", "title", "price", "created_at"])

3. Create auth endpoints using manage_endpoints(action="create_auth") ONLY for tables that have a PASSWORD column
   - Requires a username_column and password_column from the SAME table
   - Do NOT create auth endpoints for tables without a PASSWORD column

`)

	if len(authProviderTables) > 0 {
		b.WriteString(fmt.Sprintf("**CRITICAL: Create auth endpoints for: %s** (these have PASSWORD columns)\n", strings.Join(authProviderTables, ", ")))
		b.WriteString("Use manage_endpoints(action=\"create_auth\", table_name=\"...\", username_column=\"...\", password_column=\"password\")\n\n")
	}

	if len(authProtectedTables) > 0 {
		b.WriteString(fmt.Sprintf("**Auth-protected tables: %s** — set requires_auth=true on their API endpoints (do NOT use create_auth for these)\n\n", strings.Join(authProtectedTables, ", ")))
	}

	b.WriteString(`4. Create upload endpoints if any page needs file uploads (profile pics, attachments, form files):
   - manage_endpoints(action="create_upload", path="photos", allowed_types=["image/*"], max_size_mb=5)
   - This creates POST /api/photos/upload (multipart form, field "file")
   - Returns: {"url": "/files/uploads/...", "filename": "...", "size": 1234, "type": "image/png"}
   - Optional: set table_name to auto-store metadata, requires_auth for protected uploads

5. Create stream endpoints if any page needs real-time updates (chat, live feeds, notifications):
   - manage_endpoints(action="create_stream", path="messages", event_types=["data.insert", "data.update"])
   - This creates GET /api/messages/stream (SSE endpoint)
   - Frontend: const source = new EventSource('/api/messages/stream');
   - source.addEventListener('data.insert', (e) => { const data = JSON.parse(e.data); ... });

6. If the site needs to send emails (contact forms, notifications, welcome emails):
   - First ensure an email provider exists: manage_providers(action="add", name="email", base_url="https://api.sendgrid.com/v3", ...)
   - Then configure: manage_email(action="configure", provider_name="email", provider_type="sendgrid", from_address="noreply@site.com")
   - Optionally save templates: manage_email(action="save_template", template_name="welcome", subject="Welcome {{name}}", body_html="<h1>Hi {{name}}!</h1>...")
   - Provider types: sendgrid, mailgun, resend, ses, generic

7. If the site needs payments (checkout, donations, e-commerce):
   - First create a payment provider: manage_providers(action="add", name="payments", base_url="https://api.stripe.com", ...)
   - Then configure: manage_payments(action="configure", provider_name="payments", provider_type="stripe", currency="usd")
   - Provider types: stripe, paypal, mollie, square, generic
   - The frontend will call a custom API endpoint that uses create_checkout to get a checkout URL and redirect

8. Seed data using manage_data(action="insert", rows=[{...}, {...}]) if seed_data is true
   - Use the rows parameter with an array of row objects for bulk insert (3-5 rows per table)
   - Example: manage_data(action="insert", table_name="posts", rows=[{"title": "First Post", "body": "Hello"}, {"title": "Second Post", "body": "World"}])
`)

	return b.String()
}

// --- BUILD_PAGES (per-page) prompt ---

func buildPagePrompt(page PagePlan, plan *SitePlan, allPaths []string, layoutSummary, cssClassMap, siteDescription, tableSchema, svgAssets string, contentTerms, previousWarnings []string) string {
	var b strings.Builder

	b.WriteString("You are IATAN, an AI that builds web pages. Create ONE page using manage_pages.\n\n")
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	// Site context for content coherence.
	if siteDescription != "" || plan.DesignNotes != "" {
		b.WriteString("## Site Context\n")
		if siteDescription != "" {
			b.WriteString(fmt.Sprintf("- About: %s\n", siteDescription))
		}
		if plan.DesignNotes != "" {
			b.WriteString(fmt.Sprintf("- Design direction: %s\n", plan.DesignNotes))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Page to Build\n")
	b.WriteString(fmt.Sprintf("- Path: %s\n", page.Path))
	b.WriteString(fmt.Sprintf("- Title: %s\n", page.Title))
	b.WriteString(fmt.Sprintf("- Purpose: %s\n", page.Purpose))
	if page.Layout != "" && page.Layout != "default" {
		b.WriteString(fmt.Sprintf("- Layout: %s\n", page.Layout))
		if page.Layout == "none" {
			b.WriteString("  (This page has NO layout — no nav or footer will be injected. Include any navigation you need directly in the page content.)\n")
		}
	}
	if len(page.Sections) > 0 {
		b.WriteString(fmt.Sprintf("- Sections: %s\n", strings.Join(page.Sections, ", ")))
	}
	if len(page.LinksTo) > 0 {
		b.WriteString(fmt.Sprintf("- Links to: %s\n", strings.Join(page.LinksTo, ", ")))
	}
	b.WriteString("\n")

	b.WriteString("## Design Tokens\n")
	b.WriteString(fmt.Sprintf("- Colors: primary=%s, secondary=%s, accent=%s, bg=%s, text=%s\n",
		plan.ColorScheme.Primary, plan.ColorScheme.Secondary, plan.ColorScheme.Accent,
		plan.ColorScheme.Background, plan.ColorScheme.Text))
	b.WriteString(fmt.Sprintf("- Fonts: heading=%s, body=%s\n", plan.Typography.HeadingFont, plan.Typography.BodyFont))
	b.WriteString(fmt.Sprintf("- Architecture: %s\n", plan.Architecture))
	b.WriteString("\n")

	// Compact CSS class reference instead of full stylesheet (~80% smaller).
	if cssClassMap != "" {
		b.WriteString("## Global CSS Reference\n")
		b.WriteString(cssClassMap)
		b.WriteString("\nIMPORTANT: Use ONLY the CSS classes and custom properties listed above. Do NOT invent new class names.\n\n")
	}

	b.WriteString("## Layout\n")
	b.WriteString(layoutSummary + "\n\n")

	b.WriteString("## Existing Pages\n")
	b.WriteString(strings.Join(allPaths, ", ") + "\n\n")

	if svgAssets != "" {
		b.WriteString("## Available SVG Illustrations\n")
		b.WriteString(svgAssets)
		b.WriteString("Use these in hero sections and feature areas via <img src=\"/assets/svg/...\"> or inline SVG.\n\n")
	}

	if tableSchema != "" {
		b.WriteString("## Data Available (LIVE — already seeded with real data)\n")
		b.WriteString(tableSchema + "\n\n")
	}

	if len(contentTerms) > 0 {
		b.WriteString("## Content Reference (use these exact terms for consistency)\n")
		for _, term := range contentTerms {
			b.WriteString("- " + term + "\n")
		}
		b.WriteString("\n")
	}

	if len(previousWarnings) > 0 {
		b.WriteString("## Avoid These Issues (from previous pages)\n")
		for _, w := range previousWarnings {
			b.WriteString("- " + w + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(`## Rules

1. Call manage_pages(action="save") with:
   - path, title, content (HTML for inside <main> only), status="published"
   - metadata: {"description": "...", "keywords": "..."}
   - If you create page-scoped assets, list them in the assets param`)

	if page.Layout != "" && page.Layout != "default" {
		b.WriteString(fmt.Sprintf("\n   - layout: \"%s\"", page.Layout))
	}

	b.WriteString(`

2. Content is MAIN-CONTENT-ONLY:
   - No <nav>, <footer>, <html>, <head>, <body>, <main> tags
   - No <link> or <script> tags for shared assets (auto-injected by server)
   - No inline styles — use CSS classes from the global stylesheet above
   - Use ONLY classes that exist in the stylesheet — do NOT invent new class names
   - Use semantic HTML: sections, articles, headings (h1 down)
   - Use CSS custom properties: var(--primary), var(--text), etc.

3. Content quality:
   - Write real, meaningful content (not lorem ipsum)
   - Include all sections listed in the plan
   - Use internal links to other pages: <a href="/about">About</a>
   - Accessible: alt text, ARIA labels, proper heading hierarchy
   - Mobile-friendly: the CSS is mobile-first
`)

	if plan.Architecture == "spa" {
		b.WriteString(`
4. SPA Rules:
   - Inline <script> must be wrapped in an IIFE: (function(){ ... })();
   - The router handles navigation — just use normal <a href="/path"> links
   - Use window.IATAN for shared state across pages:
     - IATAN.set('auth:token', token) after login
     - IATAN.get('auth:token') to check auth state
     - IATAN.on('auth:token', function(token){ ... }) to react to auth changes
     - IATAN.on('navigate', function(path){ /* cleanup */ }) for page teardown
`)
	}

	if page.NeedsData && tableSchema != "" {
		b.WriteString(`
5. Data Integration (CRITICAL):
   - The API endpoint above is LIVE and contains real seed data — use it directly
   - Do NOT use placeholder data, mock arrays, or TODO comments — fetch from the real API
   - Use fetch() with the exact endpoint path shown in "Data Available" above
   - Response format: GET /api/{path} → {"data":[...],"count":N,"limit":N,"offset":N}
   - Single item: GET /api/{path}/{id} → bare object
   - Filtering: /api/{path}?column=value&sort=column&order=asc|desc
   - Aggregation: GET /api/{path}/_stats?fn=count|sum|avg|min|max&column=col&group_by=col1,col2&filter_col=value
     Returns: {"function":"count","result":42} or with group_by: {"function":"count","data":[{"category":"A","result":10},...]}
   - File upload: POST /api/{path}/upload with multipart/form-data, field "file"
     Returns: {"url":"/files/uploads/...","filename":"orig.png","size":1234,"type":"image/png"}
   - Real-time SSE: const source = new EventSource('/api/{path}/stream');
     source.addEventListener('data.insert', (e) => { const data = JSON.parse(e.data); /* new row */ });
     Events: data.insert, data.update, data.delete — each has {table, id} payload
   - Example: fetch('/api/articles').then(r=>r.json()).then(res => { res.data.forEach(item => { ... }) })
   - Always handle loading states and empty states
   - If you create a JS asset file (via manage_files), it MUST use the real API endpoint — no placeholders or TODOs
`)

		// Add auth instructions if any endpoint requires auth.
		if strings.Contains(tableSchema, "REQUIRES AUTH") || strings.Contains(tableSchema, "Auth endpoint") {
			b.WriteString(`
6. Auth-Protected Endpoints (CRITICAL):
   - Endpoints marked "REQUIRES AUTH" return 401 without a valid JWT token
   - Store the JWT token in localStorage after login: localStorage.setItem('token', data.token)
   - Include it in every fetch to protected endpoints:
     fetch('/api/path', { headers: { 'Authorization': 'Bearer ' + localStorage.getItem('token') } })
   - If fetch returns 401, redirect the user to the login page
   - The login flow: POST /api/{auth_path}/login — see "Login/register JSON field" in Data Available above for the exact JSON key
   - Registration: POST /api/{auth_path}/register with the same JSON body format
   - CRITICAL: The JSON key for login/register MUST match the field name shown above (e.g. "email" not "username", unless the field IS "username")
   - Check if user is logged in before showing protected content
`)
		}
	}

	b.WriteString(`
7. Functional completeness:
   - Do NOT create links, buttons, or UI elements that have no backing functionality
   - Example: do NOT add a "Forgot Password" link unless you implement the reset flow
   - Example: do NOT add a "Search" input unless you implement the search functionality
   - Every interactive element must DO something when clicked/submitted
   - If a feature is beyond scope, leave it out entirely — do NOT add dead UI elements
`)

	return b.String()
}

// --- REVIEW stage prompt ---

func buildReviewPrompt(issues []string, siteDB *sql.DB, plan *SitePlan) string {
	var b strings.Builder

	b.WriteString("You are IATAN, an AI that reviews and fixes websites. Fix the issues listed below.\n\n")

	// Include design context so fixes are design-aware.
	if plan != nil {
		b.WriteString("## Design Context\n")
		b.WriteString(fmt.Sprintf("- Colors: primary=%s, secondary=%s, accent=%s, bg=%s, text=%s\n",
			plan.ColorScheme.Primary, plan.ColorScheme.Secondary, plan.ColorScheme.Accent,
			plan.ColorScheme.Background, plan.ColorScheme.Text))
		b.WriteString(fmt.Sprintf("- Fonts: heading=%s, body=%s\n", plan.Typography.HeadingFont, plan.Typography.BodyFont))
		b.WriteString(fmt.Sprintf("- Architecture: %s\n", plan.Architecture))
		b.WriteString("- Page purposes:\n")
		for _, p := range plan.Pages {
			b.WriteString(fmt.Sprintf("  - %s: %s\n", p.Path, p.Purpose))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Issues Found\n")
	for _, issue := range issues {
		b.WriteString("- " + issue + "\n")
	}
	b.WriteString("\n")

	// Include CSS content so the LLM can fix alignment issues.
	hasCSSIssues := false
	for _, issue := range issues {
		if strings.Contains(issue, "CSS class") || strings.Contains(issue, "stylesheet") || strings.Contains(issue, "unstyled") {
			hasCSSIssues = true
			break
		}
	}
	if hasCSSIssues {
		cssContent := loadGlobalCSS(siteDB)
		if cssContent != "" {
			b.WriteString("## Global Stylesheet (for reference)\n```css\n")
			b.WriteString(cssContent)
			b.WriteString("\n```\n\n")
		}
	}

	b.WriteString(`## Instructions

1. Read the affected pages/layout using manage_pages(action="get") or manage_layout(action="get")
2. Fix each issue:
   - Dead links: update the href to point to an existing page, or remove the link
   - Missing nav links: update the layout nav to include the missing page
   - Missing assets: create the asset with manage_files
   - HTML structure issues: update the page content
   - CSS class mismatches: update HTML to use ONLY classes defined in the global stylesheet, or add missing classes to the CSS file
3. After fixing, summarize what you changed.

## Rules
- Pages contain ONLY <main> content (no nav/footer/shared assets)
- Don't change the design system
- You may create missing pages if they are referenced by links or navigation
- When fixing CSS issues, prefer updating HTML to use existing CSS classes rather than adding new ones
`)

	return b.String()
}

// --- MONITORING prompt ---

func buildMonitoringPrompt(site *models.Site, siteDB *sql.DB) string {
	var b strings.Builder

	b.WriteString("You are IATAN, monitoring a live website. Be brief and only act if needed.\n\n")
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	b.WriteString("## Site\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
	b.WriteString(fmt.Sprintf("- Mode: monitoring\n\n"))

	// Analytics.
	analytics := loadAnalyticsSummary(siteDB)
	if analytics != "" {
		b.WriteString("## Analytics (Last 7 Days)\n")
		b.WriteString(analytics + "\n")
	}

	// Recent errors.
	errors := loadRecentErrors(siteDB)
	if len(errors) > 0 {
		b.WriteString("## Recent Errors\n")
		for _, e := range errors {
			b.WriteString("- " + e + "\n")
		}
		b.WriteString("\n")
	}

	// Site manifest for context.
	manifest := loadSiteManifest(siteDB)
	if manifest != "" {
		b.WriteString(manifest)
	}

	b.WriteString(`## Instructions
- Review the reported issues and assess severity
- Use manage_diagnostics for system health details
- Use manage_analytics for traffic patterns
- Do NOT modify pages, layout, files, or site settings — monitoring is read-only
- If a critical issue requires fixes, use manage_communication to notify the owner
- Be brief in your response
`)

	return b.String()
}

// --- CHAT-WAKE prompt (monitoring + write tools) ---

func buildChatWakePrompt(site *models.Site, siteDB *sql.DB, userMessage string) string {
	var b strings.Builder

	b.WriteString("You are IATAN, responding to the site owner's message. The site is live and in monitoring mode.\n")
	b.WriteString("The owner has sent you a message — read it carefully and take action if needed.\n\n")
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	b.WriteString("## Site\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n\n", site.Name))

	manifest := loadSiteManifest(siteDB)
	if manifest != "" {
		b.WriteString(manifest)
	}

	// Only include CSS when the message is about styling (saves ~500-1500 tokens).
	lowerMsg := strings.ToLower(userMessage)
	needsCSS := strings.Contains(lowerMsg, "css") || strings.Contains(lowerMsg, "style") ||
		strings.Contains(lowerMsg, "color") || strings.Contains(lowerMsg, "font") ||
		strings.Contains(lowerMsg, "design") || strings.Contains(lowerMsg, "theme") ||
		strings.Contains(lowerMsg, "layout") || strings.Contains(lowerMsg, "look")
	if needsCSS {
		cssContent := loadGlobalCSS(siteDB)
		if cssContent != "" {
			b.WriteString("## Global Stylesheet\n```css\n")
			b.WriteString(cssContent)
			b.WriteString("\n```\n\n")
		}
	}

	b.WriteString(`## Instructions

- Read the owner's message and determine what needs fixing
- Use manage_pages to read and update pages
- Use manage_files to update CSS or JS files
- Use manage_layout to fix navigation or footer issues
- Use manage_data to fix data issues
- Use manage_diagnostics to check site health if needed
- After making changes, briefly confirm what you did
- Do NOT rebuild the entire site — make targeted fixes only
- If the request requires a major restructure, use manage_communication to tell the owner they should trigger a rebuild instead
`)

	return b.String()
}

// --- UPDATE_PLAN prompt ---

func buildUpdatePlanPrompt(existingPlan *SitePlan, site *models.Site) string {
	var b strings.Builder

	b.WriteString("You are IATAN, planning incremental changes to an existing site. Respond with ONLY a JSON PlanPatch object.\n\n")

	b.WriteString("## Current Site Plan\n```json\n")
	if existingPlan != nil {
		planJSON, _ := json.MarshalIndent(existingPlan, "", "  ")
		b.Write(planJSON)
	}
	b.WriteString("\n```\n\n")

	b.WriteString(`## Instructions

Create a PlanPatch JSON describing only the changes needed:

{
  "add_pages": [{"path": "/blog", "title": "Blog", ...}],
  "modify_pages": [{"path": "/", "title": "Homepage", ...}],
  "remove_pages": ["/old-page"],
  "update_nav": true,
  "update_css": false,
  "add_tables": []
}

Rules:
- Only include fields that actually change
- add_pages: new pages to create (same format as PagePlan)
- modify_pages: existing pages to rebuild (path must match)
- remove_pages: pages to delete
- update_nav: true if navigation needs updating
- update_css: true if CSS design system needs changes
- add_tables: new data tables needed
`)

	return b.String()
}

// --- DATA_LAYER fix-up prompt (lightweight) ---

func buildDataLayerFixupPrompt(issues []string) string {
	var b strings.Builder
	b.WriteString("You are IATAN, fixing missing data layer items. Create ONLY the missing items listed below.\n\n")
	b.WriteString("## Missing Items\n")
	for _, issue := range issues {
		b.WriteString("- " + issue + "\n")
	}
	b.WriteString(`
## Rules
- Do NOT recreate tables or endpoints that already exist
- For missing auth endpoints, use manage_endpoints(action="create_auth") with username_column and password_column
- For missing API endpoints, use manage_endpoints(action="create_api")
- For missing tables, use manage_schema(action="create")
`)
	return b.String()
}

// --- Scheduled task prompt ---

func buildScheduledTaskPrompt(globalDB, siteDB *sql.DB, siteID int) string {
	var b strings.Builder

	b.WriteString("You are IATAN, executing a scheduled task. Use the available tools to complete the task.\n\n")

	// Site context.
	site := loadSiteContext(globalDB, siteID)
	if site != nil {
		b.WriteString(fmt.Sprintf("Site: %s (mode: %s)\n\n", site.name, site.mode))
	}

	// Compact manifest.
	manifest := loadSiteManifest(siteDB)
	if manifest != "" {
		b.WriteString(manifest)
	}

	return b.String()
}

// --- Context loaders (reused from old prompt.go) ---

type siteContext struct {
	name, domain, mode, description, direction string
}

type questionInfo struct {
	question string
	urgency  string
}

func loadRows[T any](db *sql.DB, query string, args []interface{}, scan func(*sql.Rows) (T, bool)) []T {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var result []T
	for rows.Next() {
		if v, ok := scan(rows); ok {
			result = append(result, v)
		}
	}
	return result
}

func loadSiteContext(db *sql.DB, siteID int) *siteContext {
	var s siteContext
	var domain, description, direction sql.NullString
	err := db.QueryRow(
		"SELECT name, domain, mode, description, direction FROM sites WHERE id = ?",
		siteID,
	).Scan(&s.name, &domain, &s.mode, &description, &direction)
	if err != nil {
		return nil
	}
	s.domain = domain.String
	s.description = description.String
	s.direction = direction.String
	return &s
}

func loadRecentErrors(db *sql.DB) []string {
	return loadRows(db,
		"SELECT COALESCE(summary, '') FROM brain_log WHERE event_type = 'error' AND summary != '' ORDER BY created_at DESC LIMIT 5",
		nil, func(r *sql.Rows) (string, bool) {
			var s string
			return s, r.Scan(&s) == nil && s != ""
		})
}

func loadAnalyticsSummary(db *sql.DB) string {
	var totalViews, uniqueVisitors int
	db.QueryRow(
		"SELECT COUNT(*), COUNT(DISTINCT visitor_hash) FROM analytics WHERE created_at >= datetime('now', '-7 days')",
	).Scan(&totalViews, &uniqueVisitors)

	if totalViews == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("- Views: %d, Unique: %d\n", totalViews, uniqueVisitors))

	topPages := loadRows(db,
		"SELECT page_path, COUNT(*) as views FROM analytics WHERE created_at >= datetime('now', '-7 days') GROUP BY page_path ORDER BY views DESC LIMIT 5",
		nil, func(r *sql.Rows) (string, bool) {
			var path string
			var views int
			if r.Scan(&path, &views) == nil {
				return fmt.Sprintf("%s (%d)", path, views), true
			}
			return "", false
		})
	if len(topPages) > 0 {
		sb.WriteString("- Top: " + strings.Join(topPages, ", ") + "\n")
	}
	return sb.String()
}

func loadSiteManifest(db *sql.DB) string {
	var b strings.Builder
	hasContent := false

	type pageEntry struct{ path, title, status string }
	pages := loadRows(db,
		"SELECT path, COALESCE(title, ''), status FROM pages WHERE is_deleted = 0 ORDER BY path LIMIT 50",
		nil, func(r *sql.Rows) (pageEntry, bool) {
			var p pageEntry
			return p, r.Scan(&p.path, &p.title, &p.status) == nil
		})
	if len(pages) > 0 {
		if !hasContent {
			b.WriteString("## Site Map\n")
			hasContent = true
		}
		b.WriteString("Pages: ")
		for i, p := range pages {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(p.path)
			if p.title != "" {
				b.WriteString(fmt.Sprintf(" \"%s\"", p.title))
			}
			if p.status == "draft" {
				b.WriteString(" [draft]")
			}
		}
		b.WriteString("\n")
	}

	type assetEntry struct{ filename, scope string }
	assets := loadRows(db,
		"SELECT filename, COALESCE(scope, 'global') FROM assets ORDER BY scope, filename LIMIT 50",
		nil, func(r *sql.Rows) (assetEntry, bool) {
			var a assetEntry
			return a, r.Scan(&a.filename, &a.scope) == nil
		})
	if len(assets) > 0 {
		if !hasContent {
			b.WriteString("## Site Map\n")
			hasContent = true
		}
		var global, paged []string
		for _, a := range assets {
			if a.scope == "page" {
				paged = append(paged, a.filename)
			} else {
				global = append(global, a.filename)
			}
		}
		if len(global) > 0 {
			b.WriteString("Global assets: " + strings.Join(global, ", ") + "\n")
		}
		if len(paged) > 0 {
			b.WriteString("Page assets: " + strings.Join(paged, ", ") + "\n")
		}
	}

	if hasContent {
		b.WriteString("\n")
	}
	return b.String()
}

func loadPendingQuestions(db *sql.DB) []questionInfo {
	return loadRows(db,
		"SELECT question, urgency FROM questions WHERE status = 'pending' ORDER BY id LIMIT 10",
		nil, func(r *sql.Rows) (questionInfo, bool) {
			var q questionInfo
			return q, r.Scan(&q.question, &q.urgency) == nil
		})
}
