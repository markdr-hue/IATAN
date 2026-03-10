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
      "purpose": "2-3 sentences: what this page shows, its layout pattern, and the user's goal",
      "sections": ["hero", "features", "cta"],
      "component_hints": ["hero-cta", "feature-grid-3col", "cta-banner"],
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
      "public_read": false,
      "seed_data": true
    }
  ],
  "nav_items": ["/", "/about", "/services", "/contact"],
  "design_notes": "Brief notes about the design approach",
  "questions": []
}

## Rules

1. ALWAYS include a homepage at path "/" and a 404 page at "/404"
2. Architecture: use "spa" for app-like sites with navigation between views, "multi-page" for content/brochure sites
3. Color scheme — choose light or dark theme based on the site's brand and audience:
   - background and text must have strong contrast (WCAG AA ratio >= 4.5:1)
   - primary: vibrant, stands out against background (buttons, links, accents)
   - secondary: complementary to primary, used for less prominent elements
   - accent: distinct from both primary and secondary (highlights, badges, hover states)
   - surface: subtle offset from background for cards, panels, modals (slightly lighter for dark themes, slightly darker for light themes)
   - text_muted: lower contrast than text but still readable against background (ratio >= 3:1)
   - All colors as hex values (#rrggbb format)
4. Choose 1-2 Google Fonts that complement each other (one for headings, one for body)
5. "scale" is the modular type scale ratio: "1.125" (minor second), "1.2" (minor third), "1.25" (major third), "1.333" (perfect fourth). Use 1.2-1.25 for most sites.
6. Each page should have 2-6 meaningful sections (not just "content")
7. "purpose" must be 2-3 sentences describing: what the page shows, the layout pattern (hero+grid, sidebar+content, full-width form), and the user's goal on this page
8. "component_hints" suggests UI patterns for each page (e.g. "hero-cta", "feature-grid-3col", "pricing-table", "testimonial-cards", "contact-form", "blog-list", "sidebar-nav"). These guide the design stage.
9. "links_to" should reference paths of other pages in the plan. For parameterised routes use the base path (e.g. "/thread" to link to "/thread/:id")
10. "nav_items" is the ordered list of page paths for the main navigation (exclude /404)
11. If the site needs dynamic data (products, blog posts, user accounts), set "needs_data_layer": true and define tables
12. For data_tables, each column is {"name": "col_name", "type": "TYPE"}. Types: TEXT, INTEGER, REAL, BOOLEAN, PASSWORD, ENCRYPTED. Optional: "primary": true, "required": true
13. Set "data_tables": [] and "needs_data_layer": false if the site doesn't need dynamic data
14. Each page's "data_tables" is an array of table names the page needs (e.g. ["articles", "comments"]). A page can use multiple tables.
15. Each page can specify a "layout" name. Options: "default" (standard nav+footer), "none" (no layout — for landing pages), or a custom name like "blog" (with sidebar). Most pages should use "default". The DESIGN stage will create all referenced layouts.
16. If the site needs any of: authentication, payments, file uploads, real-time updates, social login, or user roles — set "needs_data_layer": true and define the relevant tables. The DATA_LAYER stage handles all implementation details (endpoints, auth, providers, etc.).
17. Pages with login/register forms MUST set "needs_data": true and include the user table in "data_tables".
18. For user tables with auth: include a PASSWORD column. For social login: add "has_oauth": ["google"]. For user roles: add "roles": ["user", "admin"] and a TEXT column named "role".
19. Set "public_read": true on tables where unauthenticated visitors should see data (posts, products, articles). Keep false for private data.

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
   - File: css/styles.css (use EXACTLY this filename)
   - Include CSS custom properties from the plan's color_scheme and typography:
     --primary, --secondary, --accent, --bg, --surface, --text, --text-muted
     --font-heading, --font-body, --scale
   - Include a CSS reset/normalize
   - Base typography styles using the scale
   - Utility classes for common patterns (containers, grids, cards, buttons)
   - Mobile-first responsive design with these breakpoints:
     @media (min-width: 640px) — sm (landscape phones)
     @media (min-width: 768px) — md (tablets)
     @media (min-width: 1024px) — lg (desktops)
     @media (min-width: 1280px) — xl (wide screens)
   - Color scheme using the planned colors (respect the theme — dark or light)

2. CREATE LAYOUTS (manage_layout, action="save"):
   - Create the "default" layout (name="default"):
     - body_before_main: Skip-to-content link + <nav> with links from nav_items
     - body_after_main: <footer> with site info and copyright using the current year (see date above)
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

	b.WriteString(`3. OPTIONAL: CREATE DECORATIVE SVGs (manage_files, action="save", storage="assets"):
   - Prefer CSS gradients, shapes, and borders over SVG illustrations when possible
   - Only create SVGs if the design truly benefits from them (icons, simple decorations)
   - If you do create SVGs, save each one via manage_files — pages can ONLY reference SVGs that were actually saved
   - Keep SVGs small (< 3KB each) — abstract shapes, geometric patterns
   - Use CSS custom properties: fill="var(--primary)", stroke="var(--accent)"
   - Name them descriptively: svg/hero-decoration.svg, svg/feature-icon.svg
   - Use scope="global" for SVGs reused across pages, scope="page" for single-use

`)

	b.WriteString(`4. STORE DESIGN DECISIONS in memory:`)
	b.WriteString(`
   - manage_memory(action="remember", key="site_architecture", value="` + plan.Architecture + `")
   - manage_memory(action="remember", key="design_summary", value="<brief design summary>")

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
	var hasOAuth, hasRoles bool

	b.WriteString("## Data Tables to Create\n\n")
	for _, t := range plan.DataTables {
		if len(t.HasOAuth) > 0 {
			hasOAuth = true
		}
		if len(t.Roles) > 0 {
			hasRoles = true
		}
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
				if t.PublicRead {
					b.WriteString("Needs auth protection: yes — set requires_auth=true AND public_read=true on its API endpoint\n")
					b.WriteString("  (GET requests are public, POST/PUT/DELETE require auth)\n")
				} else {
					b.WriteString("Needs auth protection: yes — set requires_auth=true on its API endpoint\n")
				}
				authProtectedTables = append(authProtectedTables, t.Name)
			}
		}
		if len(t.HasOAuth) > 0 {
			b.WriteString(fmt.Sprintf("**Needs OAuth (social login): %s** — create OAuth providers after auth endpoint\n", strings.Join(t.HasOAuth, ", ")))
		}
		if len(t.Roles) > 0 {
			b.WriteString(fmt.Sprintf("**User roles: %s** — set default_role on auth endpoint, use required_role on protected API endpoints\n", strings.Join(t.Roles, ", ")))
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
   - Set public_columns to exclude sensitive fields (NEVER expose password, email, or encrypted columns)
   - **public_read rule** — ANY table whose data appears on public-facing pages MUST have public_read=true:
     • User/profile tables (author names, avatars, profile pages)
     • Content tables (blog posts, products, forum threads, reviews)
     • Category/tag tables referenced by public listings
     Without public_read, GET returns 401 for logged-out visitors and pages break.
     Set requires_auth=true AND public_read=true → GET is public, POST/PUT/DELETE require auth.
   - For fully private data (admin logs, private settings): set requires_auth=true only
   - Example: manage_endpoints(action="create_api", path="users", table_name="users", requires_auth=true, public_read=true, public_columns=["id", "username", "avatar", "created_at"])

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

5. Create real-time endpoints based on the site's needs:
   - For server→client updates (live feeds, notifications, dashboards):
     manage_endpoints(action="create_stream", path="messages", event_types=["data.insert", "data.update"])
     This creates GET /api/messages/stream (SSE endpoint)
     Frontend: const source = new EventSource('/api/messages/stream');
     source.addEventListener('data.insert', (e) => { const data = JSON.parse(e.data); ... });
   - For bidirectional real-time (chat, collaborative features, interactive apps):
     manage_endpoints(action="create_websocket", path="chat", event_types=["data.insert"], receive_event_type="chat.message", write_to_table="messages")
     This creates GET /api/chat/ws (WebSocket endpoint)
     Frontend: const ws = new WebSocket((location.protocol==='https:'?'wss:':'ws:') + '//' + location.host + '/api/chat/ws');
     ws.onmessage = (e) => { const data = JSON.parse(e.data); ... };
     ws.send(JSON.stringify({content: "hello"}));
   - Choose SSE when clients only need to receive updates (simpler, auto-reconnect built in)
   - Choose WebSocket when clients need to SEND data in real-time (chat messages, live collaboration)

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
   - Use the rows parameter with an array of row objects for bulk insert (5-10 rows per table)
   - Include diverse, realistic data: vary categories, dates, content lengths, and styles
   - Example: manage_data(action="insert", table_name="posts", rows=[{"title": "Getting Started", "body": "A comprehensive guide...", "category": "tutorial"}, ...])
   - For blog/article tables: vary post lengths (short, medium, long), include different authors/categories
   - For product tables: vary prices, categories, and include realistic descriptions

9. If the site needs to receive webhooks from external services (payment confirmations, form submissions):
   - Create incoming webhooks: manage_webhooks(action="create", path="stripe-events", secret_name="stripe_webhook_secret")
   - This creates POST /api/webhooks/{path} that validates the signature and stores events
   - Only create webhooks if the site actually integrates with external services that send them

`)

	if hasOAuth {
		b.WriteString(`10. For OAuth (social login), after creating the auth endpoint:
   - Ask the site owner for ALL OAuth credentials in ONE question using structured fields:
     manage_communication(action="ask", question="Please provide OAuth credentials for the providers below.", context="Register apps at these URLs and set the callback URL to /api/{auth_path}/oauth/{provider}/callback\nGoogle: https://console.cloud.google.com\nGitHub: https://github.com/settings/developers\nDiscord: https://discord.com/developers", fields="[{\"name\":\"google_client_id\",\"label\":\"Google Client ID\",\"type\":\"text\"},{\"name\":\"google_client_secret\",\"label\":\"Google Client Secret\",\"type\":\"secret\",\"secret_name\":\"google_oauth_secret\"},{\"name\":\"github_client_id\",\"label\":\"GitHub Client ID\",\"type\":\"text\"},{\"name\":\"github_client_secret\",\"label\":\"GitHub Client Secret\",\"type\":\"secret\",\"secret_name\":\"github_oauth_secret\"}]")
   - Only include fields for providers the site actually needs (check the plan)
   - After the owner answers, read the field values from memory (for client_id) and secrets (for client_secret)
   - Then create each provider: manage_endpoints(action="create_oauth", provider_name="google", display_name="Google", client_id="...", client_secret_name="google_oauth_secret", ...)
   Common provider URLs:
   - Google: authorize=https://accounts.google.com/o/oauth2/v2/auth, token=https://oauth2.googleapis.com/token, userinfo=https://www.googleapis.com/oauth2/v3/userinfo
   - GitHub: authorize=https://github.com/login/oauth/authorize, token=https://github.com/login/oauth/access_token, userinfo=https://api.github.com/user, username_field="login"
   - Discord: authorize=https://discord.com/api/oauth2/authorize, token=https://discord.com/api/oauth2/token, userinfo=https://discord.com/api/users/@me, scopes="identify email"

`)
	}

	if hasRoles {
		b.WriteString(`11. For role-based access control (RBAC):
   - Include a "role" TEXT column in the user table schema
   - Set default_role on create_auth: manage_endpoints(action="create_auth", ..., default_role="user", role_column="role")
   - For admin-only API endpoints: manage_endpoints(action="create_api", ..., requires_auth=true, required_role="admin")
   - Role is embedded in the JWT token and checked automatically by the API middleware

`)
	}

	return b.String()
}

// --- BUILD_PAGES (per-page) prompt ---

func buildPagePrompt(page PagePlan, plan *SitePlan, allPaths []string, layoutSummary, cssClassMap, siteDescription, apiContract, authContract, svgAssets string, contentTerms, previousWarnings []string) string {
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
		b.WriteString("  Use these names as id or class on your <section> elements (e.g. <section id=\"hero\">, <section class=\"features\">). The validator checks for these.\n")
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

	// Compact CSS class reference — avoids sending full CSS to every page build.
	if cssClassMap != "" {
		b.WriteString("## CSS Class Reference (use ONLY these classes)\n")
		b.WriteString(cssClassMap + "\n\n")
	}

	b.WriteString("## Layout\n")
	b.WriteString(layoutSummary + "\n\n")

	b.WriteString("## Existing Pages\n")
	b.WriteString(strings.Join(allPaths, ", ") + "\n\n")

	if svgAssets != "" {
		b.WriteString("## Available SVG Illustrations\n")
		b.WriteString(svgAssets)
		b.WriteString("Use these in hero sections and feature areas via <img src=\"/assets/svg/...\"> or inline SVG.\n")
		b.WriteString("Only reference SVG filenames listed above. If you need a custom SVG, create it first via manage_files(action=\"save\", storage=\"assets\") then reference it.\n\n")
	} else {
		b.WriteString("No SVG assets are available. Use CSS gradients, borders, and shapes for decorative elements. You can also create SVGs via manage_files(action=\"save\", storage=\"assets\") if needed.\n\n")
	}

	if apiContract != "" && page.NeedsData {
		b.WriteString("## API Reference (LIVE — already seeded with real data)\n")
		b.WriteString(apiContract + "\n\n")
	} else if authContract != "" {
		// Pages without needs_data still need auth endpoints for login/signup forms.
		b.WriteString("## Auth Endpoints (for login/register forms)\n")
		b.WriteString(authContract + "\n\n")
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
   - If you create page-scoped assets, list them in the assets param as a JSON array: ["js/page-chart.js", "css/page-gallery.css"]`)

	if page.Layout != "" && page.Layout != "default" {
		b.WriteString(fmt.Sprintf("\n   - layout: \"%s\"", page.Layout))
	}

	b.WriteString(`

2. Content is MAIN-CONTENT-ONLY:
   - No <nav>, <footer>, <html>, <head>, <body>, <main> tags
   - No <link> or <script> tags for shared assets (auto-injected by server)
   - No inline styles — use CSS classes from the global stylesheet above
   - CRITICAL: Use ONLY CSS classes from the Global Stylesheet above. Before using any class, verify it exists in the stylesheet.
     If you need a style not covered, create a page-scoped CSS file via manage_files(action="save", storage="assets", scope="page").
     NEVER invent class names. The REVIEW stage flags every undefined class as a validation error.
   - Use semantic HTML: sections, articles, headings (h1 down)
   - Use CSS custom properties: var(--primary), var(--text), etc.

3. Content quality:
   - Write real, meaningful content (not lorem ipsum)
   - Include all sections listed in the plan
   - Use internal links to other pages: <a href="/about">About</a>
   - Accessible: alt text, ARIA labels, proper heading hierarchy
   - Mobile-friendly: the CSS is mobile-first
`)

	// Built-in JS runtime reference (App.*)
	b.WriteString(`
4. JavaScript Runtime (built-in — use these APIs, do NOT use raw fetch for /api/ calls):
   // API calls (auth header automatically included if user is logged in):
   App.fetch('/api/products')                                              // GET list → {data: [...], count, limit, offset}
   App.fetch('/api/products/' + id)                                        // GET single
   App.fetch('/api/products', {method:'POST', body: {title:'...'}})        // CREATE
   App.fetch('/api/products/'+id, {method:'PUT', body: {title:'...'}})     // UPDATE
   App.fetch('/api/products/'+id, {method:'DELETE'})                       // DELETE
`)

	if authContract != "" || (apiContract != "" && (strings.Contains(apiContract, "[AUTH]") || strings.Contains(apiContract, "/login"))) {
		b.WriteString(`
   // Auth — App.auth appends /login and /register automatically.
   // IMPORTANT: pass the BASE path only (see "Auth base path" in the API Reference above).
   //   CORRECT:   App.auth.register('/api/auth', {...})
   //   WRONG:     App.auth.register('/api/auth/register', {...})  ← doubled path!
   App.auth.login('/api/{auth_path}', {email:'...', password:'...'})       // → POST /api/{auth_path}/login
   App.auth.register('/api/{auth_path}', {email:'...', password:'...'})    // → POST /api/{auth_path}/register
   App.auth.logout()                  // clears token
   App.auth.isLoggedIn()              // boolean
   App.auth.getRole()                 // reads role from JWT ('admin', 'user', etc.)
   App.auth.onAuthChange(fn)          // called on login/logout
   // OAuth: just link to /api/{auth_path}/oauth/{provider} — token captured automatically
`)
	}

	if plan.Architecture == "spa" {
		b.WriteString(`
   // State (share data between pages):
   App.set('cart:items', items)       // set + notify listeners
   App.get('cart:items')              // read
   App.on('cart:items', fn)           // subscribe to changes

   // Navigation: App.navigate('/about') or just use <a href="/about">
   // SPA page scripts: wrap in IIFE with data-page-asset:
   // <script data-page-asset>(function(){ ... })();</script>
`)
	}

	b.WriteString(`
   Use EXACT endpoint paths from the API Reference. Do NOT invent URLs. Do NOT use raw fetch() for /api/ calls — use App.fetch().
   Always handle loading and empty states. Pagination: offset += res.data.length; hide "load more" when res.data.length < limit.

   Auth-aware data loading:
   - [AUTH] endpoints return 401 if not logged in. NEVER call them without checking App.auth.isLoggedIn() first.
   - [AUTH] [PUBLIC_READ] endpoints allow GET without auth (anyone can read), but POST/PUT/DELETE require auth.
     → Safe to call for reading data on public pages. Gate write operations behind App.auth.isLoggedIn().
   - On public pages (homepage, listing pages), ONLY use [PUBLIC_READ] endpoints or non-auth endpoints for data.
   - If a page needs auth-only data, check App.auth.isLoggedIn() first and show a login prompt if not authenticated.
`)

	b.WriteString(`
5. Functional completeness:
   - Only include interactive elements you can implement with available API endpoints
   - If no endpoint exists for a feature, omit the feature entirely — do NOT stub it or mark it "Coming Soon"
   - Every button, link, and form on the page must be fully functional
`)

	return b.String()
}

// --- REVIEW stage prompt ---

func buildReviewPrompt(issues []string, siteDB *sql.DB, plan *SitePlan, cssClassMap string) string {
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
		if cssClassMap != "" {
			b.WriteString("## Available CSS Classes (use ONLY these — do not invent new ones)\n")
			b.WriteString(cssClassMap)
			b.WriteString("\n\n")
		}
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
- Every section from the page plan should be present in the page HTML — if a planned section is missing, add it
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

	// Always include compact CSS class map so the LLM can reference styles for any fix.
	cssClassMap := extractCSSClassMap(loadGlobalCSS(siteDB))
	if cssClassMap != "" {
		b.WriteString("## CSS Class Reference\n")
		b.WriteString(cssClassMap + "\n\n")
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

func buildUpdatePlanPrompt(existingPlan *SitePlan, site *models.Site, changeDescription string) string {
	var b strings.Builder

	b.WriteString("You are IATAN, planning incremental changes to an existing site. Respond with ONLY a JSON PlanPatch object.\n\n")

	if site != nil {
		b.WriteString("## Site\n")
		b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
		if site.Description != nil && *site.Description != "" {
			b.WriteString(fmt.Sprintf("- About: %s\n", *site.Description))
		}
		b.WriteString("\n")
	}

	if changeDescription != "" {
		b.WriteString("## Requested Changes\n")
		b.WriteString(changeDescription + "\n\n")
	}

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

func buildDataLayerFixupPrompt(issues []string, plan *SitePlan) string {
	var b strings.Builder
	b.WriteString("You are IATAN, fixing missing data layer items. Create ONLY the missing items listed below.\n\n")
	b.WriteString("## Missing Items\n")
	for _, issue := range issues {
		b.WriteString("- " + issue + "\n")
	}

	// Include table schemas so the LLM knows column names/types.
	if len(plan.DataTables) > 0 {
		b.WriteString("\n## Table Schemas (from plan)\n")
		for _, t := range plan.DataTables {
			b.WriteString(fmt.Sprintf("### %s (has_api=%v, has_auth=%v, public_read=%v)\n", t.Name, t.HasAPI, t.HasAuth, t.PublicRead))
			for _, c := range t.Columns {
				b.WriteString(fmt.Sprintf("  - %s %s", c.Name, c.Type))
				if c.Required {
					b.WriteString(" (required)")
				}
				b.WriteString("\n")
			}
		}
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

// --- Context loaders ---

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
