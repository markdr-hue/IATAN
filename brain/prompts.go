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

	"github.com/markdr-hue/IATAN/chat"
	"github.com/markdr-hue/IATAN/db/models"
)

// --- Platform Capabilities Reference ---
// Injected into ANALYZE and BUILD prompts so the LLM knows what tools exist.

func buildPlatformCapabilitiesRef() string {
	var b strings.Builder

	b.WriteString(`## Platform Capabilities Reference

### Endpoint Types (manage_endpoints tool)
- **create_api**: CRUD REST endpoint bound to a dynamic table.
  Creates: GET /api/{path} (list with ?sort, ?order, ?limit, ?offset, ?col=val filters), GET /api/{path}/{id}, POST, PUT, DELETE.
  GET list returns: {data: [...], count, limit, offset}. GET by ID returns bare object.
  Options: requires_auth (JWT-protected), public_read (GET is public, writes need auth), public_columns (fields exposed).
- **create_auth**: JWT authentication endpoint for a table with a PASSWORD column.
  Creates: POST /api/{path}/login → {token}, POST /api/{path}/register → {token}, GET /api/{path}/me → user object.
  All protected endpoints require header: Authorization: Bearer <token>
  Requires: username_column, password_column (must be type PASSWORD in schema).
  Options: default_role, role_column, jwt_expiry_hours.
- **create_websocket**: Real-time bidirectional messaging via WebSocket.
  Creates: /api/{path}/ws (upgrade to WebSocket). Connect with: new WebSocket(proto + '://' + host + '/api/{path}/ws?token=JWT&room=VALUE')
  Options: event_types (table events to broadcast), receive_event_type, write_to_table (auto-persist received messages),
  room_column (scope connections to a specific column value — clients connect with ?room=VALUE).
  Messages arrive as: {type: "event_type", payload: {data: {...your fields...}, client_id: "uuid"}}.
  Access the actual data via msg.payload.data. ECHO SUPPRESSION: the server does NOT send your message back to you —
  you must optimistically append your own sent message to the DOM immediately after ws.send().
- **create_stream**: Server-sent events (SSE) for one-way real-time data.
  Creates: /api/{path}/stream (SSE endpoint). Connect with: new EventSource('/api/{path}/stream')
  Options: event_types (table events to broadcast), requires_auth.
- **create_upload**: File upload endpoint (multipart form).
  Creates: POST /api/{path}/upload. Send as FormData with 'file' field. Returns: {url, filename, size, type}.
  Options: allowed_types, max_size_mb, table_name (auto-store metadata), requires_auth.

### Dynamic Tables (manage_schema tool)
- Column types: TEXT, INTEGER, REAL, BOOLEAN, PASSWORD (bcrypt-hashed), ENCRYPTED (AES).
- id, created_at are auto-added. Do NOT include them in column definitions.
- Use PASSWORD type for login credentials. Use ENCRYPTED for sensitive data like API keys.

### Layout System (manage_layout tool)
- The server wraps page content in <main>...</main> automatically.
- Layouts provide: head_content (fonts, meta), body_before_main (typically nav), body_after_main (typically footer).
- When a page uses a layout (default or custom), the server wraps the content. Pages with layout="none" get no wrapping.
- "default" layout applies to all pages unless overridden.

### Asset System (manage_files tool)
- scope="global": CSS/JS auto-injected on ALL pages by the server (in <head> for CSS, end of <body> for JS).
- scope="page": only loaded when the page lists it in its assets param.
- Use manage_files(action="save", storage="assets", scope="global") for site-wide CSS/JS.

### Parameterized Routes
- Pages with paths like /thread/:id match requests like /thread/42.
- The server injects window.__routeParams = {id: "42"} on the page so your JS can access route params.
- Only pages with :param segments receive this injection. Static paths like /about do not have __routeParams.

### Email (manage_email tool)
- Configure provider (sendgrid, mailgun, resend, ses, generic), then send or save templates.

### Payments (manage_payments tool)
- Configure provider (stripe, paypal, mollie, square, generic), create checkout sessions.

### Communication (manage_communication tool)
- ask: Ask the site owner a question when you need information you cannot determine on your own (missing credentials, design preferences, ambiguous requirements).
- check: Check if the owner has answered pending questions.
- Do NOT ask questions you can answer yourself.
`)
	return b.String()
}

// --- ANALYZE stage prompt ---

func buildAnalyzePrompt(site *models.Site, ownerName, answers string) string {
	var b strings.Builder

	b.WriteString("You are IATAN, an AI that analyzes website requirements. Your job is to understand what the user wants and map it to the platform's specific capabilities. Respond with ONLY a JSON object — no markdown, no explanation.\n\n")

	if ownerName != "" {
		b.WriteString(fmt.Sprintf("Site owner: %s\n", ownerName))
	}
	b.WriteString(fmt.Sprintf("Date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	b.WriteString("## Site Info\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
	if site.Description != nil && *site.Description != "" {
		b.WriteString(fmt.Sprintf("- Description: <user_input>%s</user_input>\n", *site.Description))
	}
	if site.Direction != nil && *site.Direction != "" {
		b.WriteString(fmt.Sprintf("- Owner Direction: <user_input>%s</user_input>\n", *site.Direction))
	}
	b.WriteString("\n")

	if answers != "" {
		b.WriteString("## Owner's Answers to Your Questions\n")
		b.WriteString(fmt.Sprintf("<user_input>%s</user_input>\n\n", answers))
	}

	b.WriteString(buildPlatformCapabilitiesRef())

	b.WriteString(`## Instructions

Analyze what the user wants and produce a JSON Analysis object. Focus on BEHAVIORS, not pages.

### Example 1: Chat app (SPA with real-time)
{
  "app_type": "chat-app",
  "architecture": "spa",
  "core_behaviors": ["real-time messaging", "channel-based rooms", "anonymous username via localStorage"],
  "requires_auth": false,
  "auth_strategy": "none",
  "realtime_needs": [
    {
      "purpose": "chat messages in channels",
      "type": "websocket",
      "path": "chat",
      "room_scoped": true,
      "room_column": "channel_id",
      "write_table": "messages"
    }
  ],
  "data_needs": [
    {
      "table_name": "messages",
      "purpose": "store chat messages with channel and username",
      "columns": [
        {"name": "content", "type": "TEXT", "required": true},
        {"name": "username", "type": "TEXT", "required": true},
        {"name": "channel_id", "type": "INTEGER", "required": true}
      ],
      "needs_api": true,
      "needs_auth": false,
      "public_read": false,
      "seed_data": false
    }
  ],
  "exclusions": ["no auth endpoints", "no login/register forms", "no OAuth"],
  "design_mood": "dark-retro-terminal",
  "questions": []
}

### Example 2: Portfolio site (multi-page, static)
{
  "app_type": "portfolio",
  "architecture": "multi-page",
  "core_behaviors": ["showcase projects with images and descriptions", "about section", "contact information"],
  "requires_auth": false,
  "auth_strategy": "none",
  "realtime_needs": [],
  "data_needs": [],
  "exclusions": ["no auth endpoints", "no real-time features", "no database tables"],
  "design_mood": "clean-minimal",
  "questions": []
}

### Example 3: Landing page (single-page, no routing)
{
  "app_type": "landing-page",
  "architecture": "single-page",
  "core_behaviors": ["hero section with headline", "feature showcase", "pricing table", "call-to-action signup"],
  "requires_auth": false,
  "auth_strategy": "none",
  "realtime_needs": [],
  "data_needs": [],
  "exclusions": ["no auth endpoints", "no real-time features", "no navigation between pages"],
  "design_mood": "modern-bold",
  "questions": []
}

## Critical Rules

1. Focus on WHAT the site should DO, not what pages to create (that's the next step).
2. Auth strategy:
   - "jwt": full login/register with PASSWORD columns and auth endpoints
   - "localStorage-only": store user preferences (like nickname) in localStorage, no server auth
   - "none": no user identity at all
   - If user says "no signup", "no auth", "no login", "anonymous", or implies casual/anonymous usage:
     set requires_auth=false, auth_strategy="localStorage-only" or "none", and add "no auth endpoints" to exclusions.
3. Real-time features:
   - Chat, messaging, live collaboration → type="websocket" with room_scoped=true if there are channels/rooms.
   - Live feeds, dashboards, notifications → type="sse".
   - If no real-time needed, omit realtime_needs entirely.
4. Data needs: define MINIMAL tables. Do NOT create tables for things that can live in localStorage.
   - Do NOT include id or created_at columns — they are auto-added.
   - If the site is purely static (portfolio, brochure, landing page), omit data_needs entirely.
5. Exclusions: explicitly list things that should NOT be built. This prevents the builder from adding unwanted features.
   - For static sites, ALWAYS include: "no auth endpoints", "no real-time features", "no database tables".
6. design_mood: a brief creative direction (2-4 words) that captures the visual feel.
7. Questions: only ask if the description is too vague to determine core behaviors. Keep to 2-3 max.
8. Architecture — decide based on user intent:
   - "multi-page": traditional websites with multiple HTML pages and full page loads. DEFAULT for most sites.
   - "spa": app-like sites with client-side routing and shared state between views. For dashboards, chat apps, tools.
   - "single-page": everything on ONE page with no navigation. For landing pages, calculators, single-screen tools.
   - When in doubt, choose "multi-page".
`)

	return b.String()
}

// --- BLUEPRINT stage prompt ---

func buildBlueprintPrompt(analysis *Analysis, site *models.Site) string {
	var b strings.Builder

	b.WriteString("You are IATAN, an AI that creates detailed website blueprints. Respond with ONLY a JSON object — no markdown, no explanation.\n\n")
	b.WriteString(fmt.Sprintf("Date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	b.WriteString("## Site Info\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
	if site.Description != nil && *site.Description != "" {
		b.WriteString(fmt.Sprintf("- About: <user_input>%s</user_input>\n", *site.Description))
	}
	if site.Direction != nil && *site.Direction != "" {
		b.WriteString(fmt.Sprintf("- Owner Direction: <user_input>%s</user_input>\n", *site.Direction))
	}
	b.WriteString("\n")

	b.WriteString("## Analysis (from previous step)\n```json\n")
	analysisJSON, _ := json.MarshalIndent(analysis, "", "  ")
	b.Write(analysisJSON)
	b.WriteString("\n```\n\n")

	b.WriteString(buildPlatformCapabilitiesRef())

	b.WriteString(`## Instructions

Transform the Analysis into a complete Blueprint. This is the build spec that drives everything.

{
  "app_type": "from analysis",
  "auth_strategy": "from analysis",
  "architecture": "from analysis (spa, multi-page, or single-page)",
  "color_scheme": {
    "primary": "#hex", "secondary": "#hex", "accent": "#hex",
    "background": "#hex", "surface": "#hex", "text": "#hex", "text_muted": "#hex"
  },
  "typography": {
    "heading_font": "Font Name", "body_font": "Font Name", "scale": "1.25"
  },
  "design_notes": "Brief design approach based on design_mood",
  "endpoints": [
    {"action": "create_api", "path": "resource-name", "table_name": "table_name", "requires_auth": false, "public_read": true},
    {"action": "create_auth", "path": "auth-path", "table_name": "users_table", "username_column": "username", "password_column": "password"},
    {"action": "create_websocket", "path": "ws-path", "room_column": "channel_id", "write_to_table": "messages"},
    {"action": "create_stream", "path": "stream-path", "event_types": ["update", "delete"]}
  ],
  "data_tables": [
    {"name": "table_name", "purpose": "what this table stores", "columns": [{"name": "col", "type": "TEXT"}], "seed_data": false}
  ],
  "pages": [
    {
      "path": "/",
      "title": "Home",
      "purpose": "Primary page — derive from Analysis.core_behaviors",
      "sections": ["section-ids-that-match-the-page-purpose"],
      "uses_endpoints": ["list endpoints this page calls, e.g. GET /api/items"],
      "uses_auth": false,
      "tech_notes": "Specific technical instructions: what API calls to make, how to handle state, what NOT to do."
    }
  ],
  "nav_items": ["/"],
  "exclusions": ["from analysis"]
}

## Rules

1. Include at least a homepage at "/". A 404 page is optional.
2. Architecture — carry forward from Analysis.architecture:
   - "multi-page": traditional websites with full page loads, no client-side routing.
   - "spa": client-side routing — page transitions without reloads, shared state between views.
   - "single-page": everything on ONE page, no navigation between pages.
   - If architecture is "multi-page": pages use standard HTML links with full page loads.
   - If architecture is "single-page": everything lives on one page — no inter-page navigation needed, nav_items should be empty or contain only "/".
3. Color scheme — choose based on design_mood:
   - background and text must have WCAG AA contrast (>= 4.5:1)
   - primary: vibrant, stands out (buttons, links)
   - surface: subtle offset from background (cards, panels)
   - All hex values (#rrggbb)
4. Google Fonts that match the design mood.
5. Typography scale: 1.0-2.0 (tighter for dense UIs, wider for editorial).
6. data_tables: map from Analysis.data_needs. IMPORTANT: use "name" (not "table_name") for the table name field.
   - Each entry MUST have a non-empty "name", "purpose", and at least one column.
   - Column types: TEXT, INTEGER, REAL, BOOLEAN, PASSWORD, ENCRYPTED.
   - Do NOT include "id" or "created_at" columns — they are auto-added.
7. Endpoints: map DIRECTLY from Analysis.realtime_needs and Analysis.data_needs.
   - endpoint.action must be one of: create_api, create_auth, create_websocket, create_stream, create_upload.
   - Do NOT create endpoints not implied by the Analysis.
   - Respect Analysis.exclusions — if "no auth endpoints" is listed, do NOT add create_auth endpoints.
8. Pages: include tech_notes for EVERY page. Tech_notes should be specific technical instructions:
   - Which API endpoints to call and how
   - How to handle state and navigation
   - What NOT to do (e.g., "do NOT navigate to separate pages for each channel")
9. uses_endpoints: list the specific API calls this page makes.
10. Exclusions: carry forward from Analysis. The BUILD stage checks these.
11. Keep pages minimal. Most single-purpose apps need 2-3 pages.
`)

	return b.String()
}

// --- BUILD stage prompt (single-phase: data layer + CSS + layout + pages) ---

func buildBuildPrompt(bp *Blueprint, site *models.Site) string {
	var b strings.Builder

	b.WriteString("You are IATAN, an AI that builds complete websites. You have all the tools needed to create the data layer, design system, and pages in ONE session.\n\n")
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	// Site context.
	b.WriteString("## Site Context\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
	if site.Description != nil && *site.Description != "" {
		b.WriteString(fmt.Sprintf("- About: <user_input>%s</user_input>\n", *site.Description))
	}
	if site.Direction != nil && *site.Direction != "" {
		b.WriteString(fmt.Sprintf("- Owner Direction: <user_input>%s</user_input>\n", *site.Direction))
	}
	b.WriteString(fmt.Sprintf("- Auth strategy: %s\n", bp.AuthStrategy))
	b.WriteString(fmt.Sprintf("- Architecture: %s\n", bp.Architecture))
	b.WriteString("\n")

	// Full blueprint.
	b.WriteString("## Blueprint\n```json\n")
	bpJSON, _ := json.MarshalIndent(bp, "", "  ")
	b.Write(bpJSON)
	b.WriteString("\n```\n\n")

	// Exclusions — repeated prominently.
	if len(bp.Exclusions) > 0 {
		b.WriteString("## EXCLUSIONS — DO NOT CREATE THESE\n")
		for _, ex := range bp.Exclusions {
			b.WriteString("- " + ex + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(buildPlatformCapabilitiesRef())

	b.WriteString(`## Build Order

Execute in this order:

### Step 1: DATA LAYER (if Blueprint has data_tables or endpoints)
1. Create tables with manage_schema(action="create") — follow Blueprint.data_tables exactly.
   - Column types: TEXT, INTEGER, REAL, BOOLEAN, PASSWORD (bcrypt), ENCRYPTED (AES).
   - id and created_at are auto-added. Do NOT include them.
2. Create endpoints with manage_endpoints — follow Blueprint.endpoints exactly.
   - Use the EXACT action, path, table_name, and options from each EndpointSpec.
   - Do NOT create endpoints not listed in the Blueprint.
   - For create_api: set public_columns to exclude sensitive fields (never expose PASSWORD/ENCRYPTED).
   - For create_websocket: include room_column and write_to_table if specified.
   - For create_auth: include username_column and password_column.
3. Seed data with manage_data(action="insert", rows=[...]) for tables with seed_data=true.
   - Use realistic, relevant data (5-10 rows). Vary content.

### Step 2: DESIGN SYSTEM
4. Create global CSS with manage_files(action="save", storage="assets", scope="global"):
   - Use Blueprint.color_scheme for your CSS custom properties / variables
   - CSS reset/normalize, base typography, component classes, responsive design
   - Design the CSS with ALL planned pages in mind

### Step 3: LAYOUTS (optional but recommended)
5. Create a layout with manage_layout(action="save"):
   - head_content: font imports, meta tags
   - body_before_main: navigation, header
   - body_after_main: footer
   - The server wraps page content in <main> and adds layout before/after it
   - Pages with layout="none" get no wrapping — useful for standalone pages
6. Create any custom layouts referenced by pages.

### Step 4: PAGES
7. Create each page with manage_pages(action="save"):
   - path, title, content (HTML that goes inside <main> when using a layout), status="published"
   - metadata: {"description": "...", "keywords": "..."}
   - Read each page's tech_notes for specific technical instructions
   - You have full freedom with JS: inline scripts, external JS files, or even a framework
`)

	// Page-specific instructions from TechNotes.
	b.WriteString("\n### Page Build Instructions\n")
	for _, page := range bp.Pages {
		b.WriteString(fmt.Sprintf("\n**%s** (%s)\n", page.Path, page.Title))
		b.WriteString(fmt.Sprintf("- Purpose: %s\n", page.Purpose))
		if len(page.Sections) > 0 {
			b.WriteString(fmt.Sprintf("- Sections: %s\n", strings.Join(page.Sections, ", ")))
		}
		if page.TechNotes != "" {
			b.WriteString(fmt.Sprintf("- Tech: %s\n", page.TechNotes))
		}
		if len(page.UsesEndpoints) > 0 {
			b.WriteString(fmt.Sprintf("- Uses: %s\n", strings.Join(page.UsesEndpoints, ", ")))
		}
		if page.Layout != "" && page.Layout != "default" {
			b.WriteString(fmt.Sprintf("- Layout: %s\n", page.Layout))
			if page.Layout == "none" {
				b.WriteString("  (No nav/footer — include any navigation directly in page content)\n")
			}
		}
	}

	b.WriteString("\n\n## Critical Rules\n\n")
	b.WriteString("1. EXCLUSIONS: ")
	if len(bp.Exclusions) > 0 {
		b.WriteString(strings.Join(bp.Exclusions, ", "))
	} else {
		b.WriteString("none")
	}
	b.WriteString(" — NEVER create these features.\n")
	b.WriteString("2. When using a layout, pages contain <main> content ONLY — the server wraps pages with the layout's nav and footer. Pages with layout=\"none\" are standalone.\n")
	b.WriteString("3. Architecture is \"" + bp.Architecture + "\" — design your navigation approach accordingly.\n")
	b.WriteString("4. Auth strategy is \"" + bp.AuthStrategy + "\"")
	if bp.AuthStrategy == "none" || bp.AuthStrategy == "localStorage-only" {
		b.WriteString(" — no server auth, no login/register forms.")
	} else {
		b.WriteString(" — use the auth endpoints (POST /api/{path}/login, /register, GET /me) for authentication flows.")
	}
	b.WriteString(`
5. Write real content — no lorem ipsum or placeholder text.
6. Accessible: alt text, ARIA labels, proper heading hierarchy, focus styles.
7. Every button, link, and form must be FULLY FUNCTIONAL with available endpoints. Do NOT stub features.
8. For page-scoped assets: save with manage_files(scope="page"), then list in manage_pages assets param.
9. Parameterized routes: the server injects window.__routeParams for /path/:param routes.
`)

	return b.String()
}

// --- VALIDATE stage fixup prompt ---

func buildValidateFixupPrompt(issues []string, bp *Blueprint, site *models.Site) string {
	var b strings.Builder

	b.WriteString("You are IATAN. The build completed but some blueprint items are missing. Create them now.\n\n")
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	if site != nil {
		b.WriteString(fmt.Sprintf("## Site: %s\n", site.Name))
	}
	if bp != nil {
		b.WriteString(fmt.Sprintf("- Architecture: %s\n", bp.Architecture))
		b.WriteString(fmt.Sprintf("- Auth: %s\n\n", bp.AuthStrategy))
	}

	b.WriteString("## Missing Items\n")
	for _, issue := range issues {
		b.WriteString("- " + issue + "\n")
	}
	b.WriteString("\n")

	b.WriteString(`## Instructions
For each missing item, create it directly:
- Missing page → create with manage_pages(action="save")
- Missing table → create with manage_schema(action="create")
- Missing API endpoint → create with manage_endpoints(action="create_api")
- Missing WebSocket endpoint → create with manage_endpoints(action="create_websocket")
- Missing stream endpoint → create with manage_endpoints(action="create_stream")
- Missing auth endpoint → create with manage_endpoints(action="create_auth")
- Missing layout → create with manage_layout(action="save")

Do NOT list, read, or query existing items. They are already built and working.
Create ONLY what is listed as missing above, then stop.
`)

	return b.String()
}

// --- POST-BUILD FIXUP prompt ---

func buildPostBuildFixupPrompt(issues []string, bp *Blueprint, scope string) string {
	var b strings.Builder

	b.WriteString("You are IATAN. The build phase just completed but a post-build check found missing items.\n")
	b.WriteString("The site is ALREADY BUILT — do NOT investigate or re-read existing pages/tables/endpoints.\n")
	b.WriteString("Create ONLY the specific missing items listed below.\n\n")
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	if bp != nil {
		b.WriteString(fmt.Sprintf("## Site: %s (architecture: %s)\n\n", bp.AppType, bp.Architecture))
	}

	b.WriteString("## Missing Items\n")
	for _, issue := range issues {
		b.WriteString("- " + issue + "\n")
	}
	b.WriteString("\n")

	b.WriteString(`## Instructions
For each missing item, create it directly:
- Missing CSS → create global CSS file with manage_files(action="save", scope="global")
- Missing layout → create with manage_layout(action="save")
- Missing page → create with manage_pages(action="save")
- Missing table → create with manage_schema(action="create")
- Missing endpoint → create with manage_endpoints

Do NOT list, read, or query existing items. They are already built and working.
Create ONLY what is listed as missing above, then stop.
`)

	return b.String()
}

// --- UPDATE_BLUEPRINT prompt ---

func buildUpdateBlueprintPrompt(existingBP *Blueprint, site *models.Site, changeDescription string) string {
	var b strings.Builder

	b.WriteString("You are IATAN, planning incremental changes to an existing site. Respond with ONLY a JSON BlueprintPatch object.\n\n")
	b.WriteString(fmt.Sprintf("Date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	if site != nil {
		b.WriteString("## Site\n")
		b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
		if site.Description != nil && *site.Description != "" {
			b.WriteString(fmt.Sprintf("- About: <user_input>%s</user_input>\n", *site.Description))
		}
		b.WriteString("\n")
	}

	if changeDescription != "" {
		b.WriteString("## Requested Changes\n")
		b.WriteString(changeDescription + "\n\n")
	}

	b.WriteString("## Current Blueprint\n```json\n")
	if existingBP != nil {
		bpJSON, _ := json.MarshalIndent(existingBP, "", "  ")
		b.Write(bpJSON)
	}
	b.WriteString("\n```\n\n")

	b.WriteString(buildPlatformCapabilitiesRef())

	b.WriteString(`## Instructions

Create a BlueprintPatch JSON describing only the changes needed:

{
  "add_pages": [{"path": "/blog", "title": "Blog", "purpose": "...", "sections": [...], "tech_notes": "..."}],
  "modify_pages": [{"path": "/", "title": "Homepage", "purpose": "...", "sections": [...], "tech_notes": "..."}],
  "remove_pages": ["/old-page"],
  "add_endpoints": [{"action": "create_api", "path": "posts", "table_name": "posts"}],
  "add_tables": [{"name": "posts", "purpose": "...", "columns": [...]}],
  "update_nav": true,
  "update_css": false
}

Rules:
- Only include fields that actually change
- add_pages: new pages with full PageBlueprint format (include tech_notes!)
- modify_pages: existing pages to rebuild (path must match existing)
- remove_pages: paths to delete
- add_endpoints: new API/WebSocket/SSE endpoints to create
- add_tables: new data tables
- update_nav: true if navigation needs updating
- update_css: true if CSS design system needs changes
- Respect existing Blueprint.exclusions — do NOT add excluded features
`)

	return b.String()
}

// --- MONITORING prompt ---

func buildMonitoringPrompt(site *models.Site, siteDB *sql.DB, bp *Blueprint) string {
	var b strings.Builder

	b.WriteString("You are IATAN, monitoring a live website. Be brief and only act if needed.\n\n")
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	b.WriteString("## Site\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
	b.WriteString("- Mode: monitoring\n")
	if bp != nil {
		b.WriteString(fmt.Sprintf("- Type: %s, Architecture: %s, Auth: %s\n", bp.AppType, bp.Architecture, bp.AuthStrategy))
		b.WriteString(fmt.Sprintf("- Blueprint: %d pages, %d endpoints, %d tables\n", len(bp.Pages), len(bp.Endpoints), len(bp.DataTables)))
	}
	b.WriteString("\n")

	analytics := loadAnalyticsSummary(siteDB)
	if analytics != "" {
		b.WriteString("## Analytics (Last 7 Days)\n")
		b.WriteString(analytics + "\n")
	}

	errors := loadRecentErrors(siteDB)
	if len(errors) > 0 {
		b.WriteString("## Recent Errors\n")
		for _, e := range errors {
			b.WriteString("- " + e + "\n")
		}
		b.WriteString("\n")
	}

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

// --- CHAT-WAKE prompt ---

func buildChatWakePrompt(site *models.Site, siteDB *sql.DB, userMessage string, bp *Blueprint) string {
	var b strings.Builder

	b.WriteString("You are IATAN, responding to the site owner's message. The site is live and in monitoring mode.\n")
	b.WriteString("The owner has sent you a message — read it carefully and take action if needed.\n\n")
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	b.WriteString("## Site\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
	if bp != nil {
		b.WriteString(fmt.Sprintf("- Type: %s, Architecture: %s, Auth: %s\n", bp.AppType, bp.Architecture, bp.AuthStrategy))
		b.WriteString(fmt.Sprintf("- Blueprint: %d pages, %d endpoints, %d tables\n", len(bp.Pages), len(bp.Endpoints), len(bp.DataTables)))
	}
	b.WriteString("\n")

	manifest := loadSiteManifest(siteDB)
	if manifest != "" {
		b.WriteString(manifest)
	}

	css := loadGlobalCSS(siteDB)
	if css != "" {
		if len(css) > 4096 {
			css = css[:4096] + "\n/* ... truncated ... */"
		}
		b.WriteString("## CSS Reference\n```css\n")
		b.WriteString(css)
		b.WriteString("\n```\n\n")
	}

	b.WriteString(chat.BuildDataLayerSummary(siteDB))

	b.WriteString(`## Instructions

- Read the owner's message and determine what needs fixing
- Prefer manage_pages(action="patch", patches='[{"search":"...","replace":"..."}]') for targeted fixes — avoids rewriting the entire page
- Only read pages you actually need to fix. Do NOT read pages "just to check"
- Do NOT re-read a page you already read in this conversation
- Fix only what the owner asked for — no bonus improvements or unrelated changes
- Use manage_files to update CSS or JS assets (scope: "global" or "page")
- Use manage_endpoints to create/modify API, WebSocket, SSE, or upload endpoints
- Use manage_schema to add/modify database tables and columns
- Use manage_data to query/insert/update rows in data tables
- Use manage_layout to fix navigation or footer issues
- Use manage_diagnostics to check site health if needed
- After making changes, briefly confirm what you did
- Do NOT rebuild the entire site — make targeted fixes only
- If the request requires a major restructure, use manage_communication to tell the owner they should trigger a rebuild instead
`)

	return b.String()
}

// --- Scheduled task prompt ---

func buildScheduledTaskPrompt(globalDB, siteDB *sql.DB, siteID int) string {
	var b strings.Builder

	b.WriteString("You are IATAN, executing a scheduled task. Use the available tools to complete the task.\n\n")
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	site := loadSiteContext(globalDB, siteID)
	if site != nil {
		b.WriteString(fmt.Sprintf("Site: %s (mode: %s)\n\n", site.name, site.mode))
	}

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

func loadGlobalCSS(db *sql.DB) string {
	var content sql.NullString
	db.QueryRow(
		"SELECT content FROM assets WHERE scope = 'global' AND filename LIKE '%.css' ORDER BY filename LIMIT 1",
	).Scan(&content)
	if content.Valid {
		return content.String
	}
	return ""
}
