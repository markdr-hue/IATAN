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

const cssPromptLimit = 4096 // max CSS chars injected into prompts

// buildPlatformContracts returns cross-cutting platform behaviors that are
// not owned by any single tool. Tool-specific docs come from Guide() methods.
func buildPlatformContracts() string {
	return `### Parameterized Routes
- Paths like /thread/:id match /thread/42. Server injects window.__routeParams = {id: "42"}.

### SPA JSON API
- GET /api/page?path=/foo -> {content, title, layout, page_css, page_js, params}.
- SPA router: intercept clicks -> fetch -> replace main -> load assets -> update title -> handle popstate.

### WebSocket Architecture
The WS server is a pure relay — it broadcasts messages to the room, not back to the sender. There is no server-side game logic. All coordination must happen client-side.

**Patterns for real-time apps (games, collaboration, chat):**
- Use a "type" field in every message to distinguish message kinds: {type: "move", ...}, {type: "state", ...}, {type: "join", ...}.
- Track players/peers client-side: maintain a Map of _sender IDs seen. Announce presence by sending a {type: "join", username: "..."} message on connect.
- For games: one client acts as host (e.g. first to join). The host makes authoritative decisions (matchmaking, scoring, game over). Non-hosts send inputs, host broadcasts game state.
- For state sync: send periodic {type: "state", ...} snapshots so late joiners or reconnectors can catch up.
- Handle disconnects: use ws.onclose + exponential backoff reconnection. Re-send a join message after reconnecting.
- The server does NOT send join/leave events — implement heartbeat messages ({type: "ping"}) and timeout detection client-side.
`
}

// --- PLAN stage prompt ---

func buildPlanPrompt(site *models.Site, ownerName, answers, capabilitiesRef string) string {
	var b strings.Builder

	b.WriteString("You are IATAN, an AI that plans and designs sites and applications. Your job is to understand what the user wants, map it to platform capabilities, and produce a complete build plan. Respond with ONLY a JSON object — no markdown, no explanation.\n\n")

	if ownerName != "" {
		b.WriteString(fmt.Sprintf("Site owner: %s\n", ownerName))
	}
	b.WriteString(fmt.Sprintf("Date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	b.WriteString("## Site Info\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
	if site.Description != nil && *site.Description != "" {
		b.WriteString(fmt.Sprintf("- Description: \"%s\"\n", *site.Description))
	}
	b.WriteString("\n")

	if answers != "" {
		b.WriteString("## Owner's Answers to Your Questions\n")
		b.WriteString(fmt.Sprintf("\"%s\"\n\n", answers))
	}

	b.WriteString("## Platform Capabilities Reference\nThis platform can build any kind of web application. The capabilities below are your building blocks.\n\n")
	b.WriteString(capabilitiesRef)
	b.WriteString(buildPlatformContracts())

	b.WriteString(`## Instructions

Analyze the requirements and produce a complete Plan JSON — the build specification that drives everything. Think creatively about the best way to build this using the platform's capabilities.

### JSON Shape
{
  "app_type": "string (dashboard, marketplace, portfolio, community, tool, cms, etc.)",
  "architecture": "spa | multi-page | single-page",
  "auth_strategy": "jwt | localStorage-only | none",
  "design_system": {
    "colors": {"primary": "#hex", "secondary": "#hex", "bg": "#hex", "surface": "#hex", "text": "#hex", "muted": "#hex", "accent": "#hex", "error": "#hex", "success": "#hex"},
    "typography": {"heading_font": "Font Name", "body_font": "Font Name", "scale": "tight|normal|loose"},
    "spacing": "compact|comfortable|spacious",
    "radius": "none|sm|md|lg",
    "dark_mode": true,
    "components": {
      "card": "<div class='card surface radius-md p-4'>...</div>",
      "button": "<button class='btn btn-primary radius-sm'>...</button>"
    }
  },
  "layout": {"style": "topnav|sidebar|minimal", "nav_items": ["/", "/about"]},
  "tables": [
    {"name": "table_name", "purpose": "what it stores", "columns": [{"name": "col", "type": "TEXT|INTEGER|REAL|BOOLEAN|PASSWORD|ENCRYPTED", "required": true}], "searchable_columns": ["col"], "seed_data": [{"col": "example1"}, {"col": "example2"}]}
  ],
  "endpoints": [
    {"action": "create_api|create_auth|create_websocket|create_stream|create_upload", "path": "resource", "table_name": "table_name", ...}
  ],
  "pages": [
    {"path": "/", "title": "Home", "purpose": "what this page does", "sections": [
      {"name": "hero", "purpose": "Full-width intro with CTA button"},
      {"name": "features", "purpose": "3-column card grid", "endpoints": ["GET /api/features"], "data_flow": "fetch on load, render as cards"}
    ], "endpoints": ["GET /api/features"], "auth": false, "notes": "technical build details"}
  ],
  "exclusions": ["things NOT to build"],
  "webhooks": [{"name": "...", "direction": "incoming|outgoing", "event_types": [...]}],
  "scheduled_tasks": [{"name": "...", "description": "...", "prompt": "...", "cron": "0 8 * * *"}],
  "questions": [{"question": "...", "type": "single_choice|multiple_choice|open", "options": ["..."]}]
}

## When to Ask Questions
If the site description is vague or missing critical details, use the "questions" field to ask the owner BEFORE producing the plan. Return ONLY the questions — no pages, tables, or endpoints yet. The pipeline will pause, collect answers, and re-run the plan stage with the answers included.
Ask when: the core purpose is unclear, the target audience is ambiguous, key features could go multiple ways, or design preferences are unknown.
Do NOT ask when: the description is specific enough to make reasonable decisions. When in doubt, make a good default choice and build — the owner can request changes later via chat.
Keep it to 2-3 questions max. Use single_choice or multiple_choice with clear options when possible.

## Guidelines
- layout.style: "topnav" for most apps, "sidebar" for dashboards/admin panels, "minimal" for landing pages.
- sections: each section should describe what it does and which APIs it calls. This drives the build.
- Do NOT include id or created_at columns — they are auto-added.
- auth_strategy: "jwt" for server-side login/register, "localStorage-only" for client-side preferences, "none" for no identity.
- Design the best site/app you can. Add features and polish that serve the user's goals.
- pages.notes: actionable build instructions — API calls, state management, key interactions.
- seed_data: include 2-3 example rows MAX to show the shape. The build stage will insert the full dataset — do NOT put dozens of rows here.

## Required Fields
- app_type, design_system (with at least colors.primary and colors.bg), pages (at least one at "/")

## Optional Fields
- architecture, auth_strategy, layout, tables, endpoints, exclusions, webhooks, scheduled_tasks, questions
`)

	return b.String()
}

// --- BUILD prompt ---

func writeSiteHeader(b *strings.Builder, site *models.Site, ownerName string) {
	if ownerName != "" {
		b.WriteString(fmt.Sprintf("Site owner: %s\n", ownerName))
	}
	b.WriteString(fmt.Sprintf("Current date: %s\n", time.Now().UTC().Format("2006-01-02")))
	b.WriteString(fmt.Sprintf("Site: %s", site.Name))
	if site.Description != nil && *site.Description != "" {
		b.WriteString(fmt.Sprintf(" — %s", *site.Description))
	}
	b.WriteString("\n\n")
}

// buildOrderChecklist computes a deterministic build order from the Plan.
// Tables → Endpoints → CSS/Layout → Pages. Pure Go, zero LLM tokens.
func buildOrderChecklist(plan *Plan) string {
	var b strings.Builder
	b.WriteString("## Build Order (follow this sequence)\n")
	step := 1

	for _, t := range plan.Tables {
		b.WriteString(fmt.Sprintf("%d. Create table: %s\n", step, t.Name))
		step++
	}
	for _, ep := range plan.Endpoints {
		entry := fmt.Sprintf("%d. Create endpoint: %s %s", step, ep.Action, ep.Path)
		if ep.TableName != "" {
			entry += fmt.Sprintf(" (table: %s)", ep.TableName)
		}
		b.WriteString(entry + "\n")
		step++
	}
	b.WriteString(fmt.Sprintf("%d. Create global CSS (design system)\n", step))
	step++
	b.WriteString(fmt.Sprintf("%d. Create shared JS utilities (auth, fetch helpers, etc.) with scope=\"global\"\n", step))
	step++
	b.WriteString(fmt.Sprintf("%d. Create layout\n", step))
	step++
	for _, pg := range plan.Pages {
		b.WriteString(fmt.Sprintf("%d. Create page: %s (%s) — then immediately create its page-specific .js file (scope=\"page\") and list it in the page's assets array\n", step, pg.Path, pg.Title))
		step++
	}
	b.WriteString("\n")
	return b.String()
}

// buildBuildPrompt creates the prompt for the single unified BUILD session.
func buildBuildPrompt(plan *Plan, site *models.Site, ownerName, existingManifest, toolGuide string) string {
	var b strings.Builder

	b.WriteString("You are IATAN. Build this complete site in one session: database tables, API endpoints, CSS design system, page layout, and all pages. You have all tools available. Follow the build order below. When everything is built, stop calling tools.\n\n")
	writeSiteHeader(&b, site, ownerName)

	b.WriteString("## Plan\n```json\n")
	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	b.Write(planJSON)
	b.WriteString("\n```\n\n")

	// Inject design system tokens as a binding CSS contract.
	if plan.DesignSystem != nil && len(plan.DesignSystem.Colors) > 0 {
		b.WriteString("## Design System Tokens (binding contract)\n")
		b.WriteString("Implement these as CSS custom properties in the global CSS file. Use them for ALL styling.\n```json\n")
		dsJSON, _ := json.MarshalIndent(plan.DesignSystem, "", "  ")
		b.Write(dsJSON)
		b.WriteString("\n```\n")
		b.WriteString("Map colors to: --color-primary, --color-secondary, --color-bg, --color-surface, --color-text, --color-muted, etc.\n")
		b.WriteString("Use var(--color-*) throughout all CSS — never hardcode hex values that duplicate a token.\n\n")

		if len(plan.DesignSystem.Components) > 0 {
			b.WriteString("## Component HTML Signatures (binding contract)\n")
			b.WriteString("You MUST use these exact HTML structures when building pages. Do NOT invent new class structures for these components.\n```json\n")
			compJSON, _ := json.MarshalIndent(plan.DesignSystem.Components, "", "  ")
			b.Write(compJSON)
			b.WriteString("\n```\n\n")
		}
	}

	b.WriteString(buildOrderChecklist(plan))

	if existingManifest != "" {
		b.WriteString("## Already Built (crash recovery — do NOT recreate these)\n")
		b.WriteString(existingManifest)
		b.WriteString("\n\n")
	}

	b.WriteString("## Platform Contracts\n\n")
	b.WriteString(toolGuide)
	b.WriteString(buildPlatformContracts())

	b.WriteString(`## Build Phases (follow this order strictly)
1. DATA LAYER: Create all tables, then all endpoints, then populate tables with rich seed data (the plan only has 2-3 examples — expand to a full realistic dataset). These must exist before pages reference them.
2. DESIGN SYSTEM: Create the global CSS file. Define ALL design tokens as custom properties AND all reusable component classes (cards, buttons, sections, grids, forms, badges, etc.) in this single file. This CSS is the design vocabulary — pages will consume it.
3. SHARED JS: Create global utility .js files (auth, fetch helpers, SPA nav, icons) with manage_files using scope="global". These are auto-injected on every page.
4. LAYOUT: Create the layout (nav/footer) using classes from the global CSS. Use body_before_main (nav/header) and body_after_main (footer). Server wraps page content in <main>.
5. PAGES (one at a time): For each page:
   a. Create the page HTML with manage_pages (sections, forms, containers with specific IDs/classes).
   b. Immediately after, create its page-specific .js file with manage_files (scope="page") that targets the exact IDs/classes you just wrote in the HTML.
   c. CRITICAL: set the page's assets array to include the .js filename: assets='["page-name.js"]'
   The JS file is created AFTER the HTML so you know the exact DOM structure to target. No guessing selectors.

## Coherence Rules
- Every page MUST use the same structural patterns. If the first page uses <section class="hero"><div class="container">...</div></section>, all hero sections must follow this same pattern.
- Consistent heading hierarchy: h1 for page title, h2 for sections, h3 for card/item titles.

## JavaScript & Interactivity Rules
Pages are NOT static HTML mockups — they must be fully functional.

**CRITICAL: Do NOT put JavaScript inside page HTML.** All JS must be in external .js files saved with manage_files:
- Shared logic (tabs, modals, CRUD helpers, fetch utilities, auth): save as a **global-scope** .js file (auto-injected on all pages).
- Page-specific logic: create the page HTML FIRST, then save a **page-scope** .js file targeting the exact IDs/classes from that page. List the filename in the page's assets array.
- The ONLY exception: a tiny inline <script> (under 3 lines) to call an init function with page-specific args.
- Wiring checklist: page HTML uses id="user-list" → page JS uses document.getElementById('user-list'). Never guess — match exactly.

Functionality requirements:
- Each page that lists endpoints in the plan MUST have JavaScript that fetches from those endpoints and renders the data.
- Fetch pattern: fetch('/api/resource').then(r => r.json()).then(data => { /* render */ })
- Tabs, accordions, modals: add event listeners that actually switch/toggle content. Use data attributes to link triggers to panels.
- CRUD admin pages: implement create (form submit → POST), read (fetch → render list), update (edit button → populate form → PUT), delete (delete button → confirm → DELETE).
- Forms: prevent default, collect values, POST/PUT to the correct endpoint, show success/error feedback.
- Dynamic pages with route params: use window.__routeParams (e.g. window.__routeParams.id) to fetch and display the specific item.
- SPA navigation: internal links are handled automatically by the SPA router. Just use normal <a href="/path"> links.
- Only link to pages that exist in the plan. Do NOT invent links to pages that aren't being built.

## Canvas & Interactive Apps
For games, visualizations, or drawing tools that need a <canvas>:
- Create the rendering engine as an external .js file with manage_files (e.g. game-engine.js).
- Place a <canvas> element in the page HTML with explicit width/height attributes.
- Use requestAnimationFrame for smooth rendering loops. Structure as: init() → gameLoop() → update() → draw().
- Keep game logic (state, physics, collision) separate from rendering code.
- For multiplayer: use WebSocket to sync state between clients. See "WebSocket Architecture" in Platform Contracts.
- Do NOT use inline <script> for the game engine — it will be too large. Use external .js files.
- Canvas pages still use the design system for surrounding UI (scores, controls, menus) — only the game area itself is canvas-rendered.

## Assets & Files
- manage_files can save any text file: .js, .css, .svg, .html, .json
- Use SVG files for icons, logos, and illustrations — save with manage_files and reference as /assets/filename.svg
- Binary images: use manage_files with the base64 data parameter, or make_http_request to fetch and save.

## Auth & Token Rules
- Store JWT tokens in localStorage using the key 'auth_token'. Each site runs on its own subdomain so tokens are naturally isolated.
- Auth header: fetch('/api/resource', { headers: { Authorization: 'Bearer ' + localStorage.getItem('auth_token') } })
- Login flow: POST to auth endpoint → store token → redirect. Logout: remove token → redirect to login.
- The /me endpoint returns the current user: fetch('/api/{auth_path}/me', { headers: { Authorization: 'Bearer ' + token } })

## Technical Rules
- Pages: content goes inside <main> only. No <!DOCTYPE>/<html>/<head>/<body>. No <nav>/<footer> (layout handles these). No <link>/<script src> for /assets/ files (auto-injected).
- NEVER hardcode the year in copyright notices or footers. Use JavaScript: <script>document.querySelectorAll('[data-year]').forEach(el => el.textContent = new Date().getFullYear())</script> with <span data-year></span> in the layout footer.
- API sorting: use ?sort=column&order=asc|desc (not order_by/direction).
- If a tool call fails, read the error, fix your input, and retry once.
- When everything is built, stop.
`)

	return b.String()
}

// --- UPDATE_PLAN prompt ---

func buildUpdatePlanPrompt(existingPlan *Plan, site *models.Site, changeDescription, ownerName, capabilitiesRef string) string {
	var b strings.Builder

	b.WriteString("You are IATAN, planning incremental changes to an existing site. Respond with ONLY a JSON PlanPatch object.\n\n")
	if ownerName != "" {
		b.WriteString(fmt.Sprintf("Site owner: %s\n", ownerName))
	}
	b.WriteString(fmt.Sprintf("Date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	if site != nil {
		b.WriteString("## Site\n")
		b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
		if site.Description != nil && *site.Description != "" {
			b.WriteString(fmt.Sprintf("- About: \"%s\"\n", *site.Description))
		}
		b.WriteString("\n")
	}

	if changeDescription != "" {
		b.WriteString("## Requested Changes\n")
		b.WriteString(changeDescription + "\n\n")
	}

	b.WriteString("## Current Plan\n```json\n")
	if existingPlan != nil {
		planJSON, _ := json.MarshalIndent(existingPlan, "", "  ")
		b.Write(planJSON)
	}
	b.WriteString("\n```\n\n")

	b.WriteString("## Platform Capabilities Reference\n\n")
	b.WriteString(capabilitiesRef)
	b.WriteString(buildPlatformContracts())

	b.WriteString(`## Instructions

Create a PlanPatch JSON describing only the changes needed:

{
  "add_pages": [{"path": "/blog", "title": "Blog", "purpose": "...", "sections": [{"name": "...", "purpose": "..."}], "notes": "..."}],
  "modify_pages": [{"path": "/", "title": "Homepage", "purpose": "...", "notes": "..."}],
  "remove_pages": ["/old-page"],
  "add_endpoints": [{"action": "create_api", "path": "posts", "table_name": "posts"}],
  "add_tables": [{"name": "posts", "purpose": "...", "columns": [...]}],
  "add_webhooks": [{"name": "...", "direction": "incoming|outgoing", "event_types": [...]}],
  "add_scheduled_tasks": [{"name": "...", "description": "...", "prompt": "...", "cron": "..."}],
  "update_nav": true,
  "update_css": false,
  "update_auth_strategy": "jwt|localStorage-only|none",
  "update_design_system": {"colors": {"primary": "#hex", ...}, "dark_mode": true},
  "update_layout": {"style": "sidebar", "nav_items": [...]}
}

Rules:
- Only include fields that actually change
- Respect existing exclusions — do NOT add excluded features
- update_design_system: only include if colors or design tokens need changing
`)

	return b.String()
}

// --- MONITORING prompt ---

func buildMonitoringPrompt(site *models.Site, siteDB *sql.DB, plan *Plan, ownerName string) string {
	var b strings.Builder

	b.WriteString("You are IATAN, monitoring a live website. Be brief and only act if needed.\n\n")
	if ownerName != "" {
		b.WriteString(fmt.Sprintf("Site owner: %s\n", ownerName))
	}
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	b.WriteString("## Site\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
	b.WriteString("- Mode: monitoring\n")
	if plan != nil {
		b.WriteString(fmt.Sprintf("- Type: %s, Architecture: %s, Auth: %s\n", plan.AppType, plan.Architecture, plan.AuthStrategy))
		b.WriteString(fmt.Sprintf("- Plan: %d pages, %d endpoints, %d tables\n", len(plan.Pages), len(plan.Endpoints), len(plan.Tables)))
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

func buildChatWakePrompt(site *models.Site, siteDB *sql.DB, userMessage string, plan *Plan, ownerName string) string {
	var b strings.Builder

	b.WriteString("You are IATAN, responding to the site owner's message. The site is live and in monitoring mode.\n")
	if ownerName != "" {
		b.WriteString(fmt.Sprintf("The owner (%s) has sent you a message — read it carefully and take action if needed.\n\n", ownerName))
	} else {
		b.WriteString("The owner has sent you a message — read it carefully and take action if needed.\n\n")
	}
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	b.WriteString("## Site\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n", site.Name))
	if plan != nil {
		b.WriteString(fmt.Sprintf("- Type: %s, Architecture: %s, Auth: %s\n", plan.AppType, plan.Architecture, plan.AuthStrategy))
		b.WriteString(fmt.Sprintf("- Plan: %d pages, %d endpoints, %d tables\n", len(plan.Pages), len(plan.Endpoints), len(plan.Tables)))
	}
	b.WriteString("\n")

	manifest := loadSiteManifest(siteDB)
	if manifest != "" {
		b.WriteString(manifest)
	}

	// Inject design tokens if available, so the LLM knows the intended design.
	if plan != nil && plan.DesignSystem != nil && len(plan.DesignSystem.Colors) > 0 {
		b.WriteString("## Design System Tokens\n```json\n")
		dsJSON, _ := json.MarshalIndent(plan.DesignSystem, "", "  ")
		b.Write(dsJSON)
		b.WriteString("\n```\n\n")
	}

	css := loadGlobalCSS(siteDB)
	if css != "" {
		if len(css) > cssPromptLimit {
			css = css[:cssPromptLimit] + "\n/* ... truncated ... */"
		}
		b.WriteString("## CSS Reference\n```css\n")
		b.WriteString(css)
		b.WriteString("\n```\n\n")
	}

	b.WriteString(chat.BuildDataLayerSummary(siteDB))

	b.WriteString(`## Instructions
- Read the owner's message and determine what needs fixing
- Prefer patch actions for targeted fixes — avoid rewriting entire files:
  - manage_pages(action="patch", patches='[{"search":"...","replace":"..."}]') for page HTML/JS
  - manage_files(action="patch", patches='[{"search":"...","replace":"..."}]') for CSS/JS files
  - manage_layout(action="patch", patches='[{"search":"...","replace":"..."}]') for nav/footer
- Only read pages/files you actually need to fix. Do NOT read things "just to check"
- Fix only what the owner asked for — no bonus improvements
- Use the design system tokens (CSS custom properties) for any new styling
- After making changes, briefly confirm what you did
- Do NOT rebuild the entire site — make targeted fixes only
- If the request requires a major restructure, use manage_communication to tell the owner they should trigger a rebuild
`)

	return b.String()
}

// --- Scheduled task prompt ---

func buildScheduledTaskPrompt(globalDB, siteDB *sql.DB, siteID int, toolGuide string) string {
	var b strings.Builder

	b.WriteString("You are IATAN, executing a scheduled task. Use the available tools to complete the task described in the user message.\n\n")
	b.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	site := loadSiteContext(globalDB, siteID)
	if site != nil {
		b.WriteString(fmt.Sprintf("## Site: %s (mode: %s)\n", site.name, site.mode))
		if site.description != "" {
			b.WriteString(fmt.Sprintf("- About: %s\n", site.description))
		}
		b.WriteString("\n")
	}

	// Plan summary so the LLM knows the site's structure.
	planSummary := loadPlanSummary(siteDB)
	if planSummary != "" {
		b.WriteString(planSummary)
	}

	manifest := loadSiteManifest(siteDB)
	if manifest != "" {
		b.WriteString(manifest)
	}

	b.WriteString(chat.BuildDataLayerSummary(siteDB))

	css := loadGlobalCSS(siteDB)
	if css != "" {
		if len(css) > cssPromptLimit {
			css = css[:cssPromptLimit] + "\n/* ... truncated ... */"
		}
		b.WriteString("## CSS Reference\n```css\n")
		b.WriteString(css)
		b.WriteString("\n```\n\n")
	}

	// Include design tokens and exclusions from plan if available.
	var planJSON sql.NullString
	siteDB.QueryRow("SELECT plan_json FROM pipeline_state WHERE id = 1").Scan(&planJSON)
	if planJSON.Valid && planJSON.String != "" {
		if p, err := ParsePlan(planJSON.String); err == nil {
			if p.DesignSystem != nil && len(p.DesignSystem.Colors) > 0 {
				b.WriteString("## Design System Tokens\n```json\n")
				dsJSON, _ := json.MarshalIndent(p.DesignSystem, "", "  ")
				b.Write(dsJSON)
				b.WriteString("\n```\n\n")
			}
			if len(p.Exclusions) > 0 {
				b.WriteString("## Exclusions — do NOT create these\n")
				for _, ex := range p.Exclusions {
					b.WriteString("- " + ex + "\n")
				}
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("## Platform Contracts\n\n")
	b.WriteString(toolGuide)
	b.WriteString(buildPlatformContracts())

	b.WriteString(`## Instructions
- Execute the task described in the user message
- Use tools as needed: query data, send emails, update pages, make HTTP requests
- Be concise — scheduled tasks run without an audience
- If you need information from the owner, use manage_communication to ask
`)

	return b.String()
}

// --- Context loaders ---

type siteContext struct {
	name, domain, mode, description string
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
	var domain, description sql.NullString
	err := db.QueryRow(
		"SELECT name, domain, mode, description FROM sites WHERE id = ?",
		siteID,
	).Scan(&s.name, &domain, &s.mode, &description)
	if err != nil {
		return nil
	}
	s.domain = domain.String
	s.description = description.String
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

func loadPlanSummary(db *sql.DB) string {
	var planJSON sql.NullString
	db.QueryRow("SELECT plan_json FROM pipeline_state WHERE id = 1").Scan(&planJSON)
	if !planJSON.Valid || planJSON.String == "" {
		return ""
	}
	plan, err := ParsePlan(planJSON.String)
	if err != nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Plan Summary\n")
	b.WriteString(fmt.Sprintf("- Type: %s, Architecture: %s, Auth: %s\n", plan.AppType, plan.Architecture, plan.AuthStrategy))

	if len(plan.Endpoints) > 0 {
		b.WriteString("- Endpoints: ")
		for i, ep := range plan.Endpoints {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(fmt.Sprintf("%s /api/%s", ep.Action, ep.Path))
		}
		b.WriteString("\n")
	}
	if len(plan.Tables) > 0 {
		b.WriteString("- Tables: ")
		for i, t := range plan.Tables {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(t.Name)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
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
