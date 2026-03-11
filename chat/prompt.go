/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package chat

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// BuildChatSystemPrompt creates a compact system prompt for user chat sessions
// that includes site context (memory keys, pages, pending questions).
// This gives the chat LLM enough awareness to answer user questions about their site.
func BuildChatSystemPrompt(globalDB, siteDB *sql.DB, siteID int) string {
	var sb strings.Builder

	// Load base system prompt from settings.
	var customPrompt string
	if err := globalDB.QueryRow("SELECT value FROM settings WHERE key = ?", "default_system_prompt").Scan(&customPrompt); err == nil && customPrompt != "" {
		sb.WriteString(customPrompt)
		sb.WriteString("\n\n")
	}

	sb.WriteString(fmt.Sprintf("Current date: %s\n\n", time.Now().UTC().Format("2006-01-02")))

	// Owner personalization.
	var ownerName string
	_ = globalDB.QueryRow("SELECT display_name FROM users WHERE role = 'admin' ORDER BY id LIMIT 1").Scan(&ownerName)
	if ownerName != "" {
		sb.WriteString(fmt.Sprintf("The user's name is %s. Address them by name when appropriate.\n\n", ownerName))
	}

	// Site info.
	var siteName, siteMode, siteDesc sql.NullString
	_ = globalDB.QueryRow("SELECT name, mode, description FROM sites WHERE id = ?", siteID).Scan(&siteName, &siteMode, &siteDesc)
	if siteName.Valid {
		sb.WriteString(fmt.Sprintf("## Current Site: %s\n", siteName.String))
		if siteMode.Valid {
			sb.WriteString(fmt.Sprintf("Mode: %s\n", siteMode.String))
		}
		if siteDesc.Valid && siteDesc.String != "" {
			sb.WriteString(siteDesc.String + "\n")
		}
		sb.WriteString("\n")
	}

	// Pages summary.
	var pageCount int
	_ = siteDB.QueryRow("SELECT COUNT(*) FROM pages WHERE is_deleted = 0").Scan(&pageCount)
	if pageCount > 0 {
		sb.WriteString(fmt.Sprintf("## Pages (%d)\n", pageCount))
		if rows, err := siteDB.Query("SELECT path, title FROM pages WHERE is_deleted = 0 ORDER BY path LIMIT 15"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var path string
				var title sql.NullString
				if rows.Scan(&path, &title) == nil {
					if title.Valid && title.String != "" {
						sb.WriteString(fmt.Sprintf("- %s (%s)\n", path, title.String))
					} else {
						sb.WriteString(fmt.Sprintf("- %s\n", path))
					}
				}
			}
			if pageCount > 15 {
				sb.WriteString(fmt.Sprintf("- ... and %d more\n", pageCount-15))
			}
		}
		sb.WriteString("\n")
	}

	// Data layer — API endpoints, WebSocket, SSE, uploads.
	sb.WriteString(BuildDataLayerSummary(siteDB))

	// Memory keys (just keys for awareness, not full values).
	if rows, err := siteDB.Query("SELECT key FROM memory ORDER BY updated_at DESC LIMIT 10"); err == nil {
		defer rows.Close()
		var keys []string
		for rows.Next() {
			var key string
			if rows.Scan(&key) == nil {
				keys = append(keys, key)
			}
		}
		if len(keys) > 0 {
			sb.WriteString("## Memory Keys\n")
			sb.WriteString(strings.Join(keys, ", "))
			sb.WriteString("\n(Use manage_memory to recall details)\n\n")
		}
	}

	// Pending questions.
	if rows, err := siteDB.Query("SELECT question FROM questions WHERE status = 'pending' ORDER BY id LIMIT 5"); err == nil {
		defer rows.Close()
		var pending []string
		for rows.Next() {
			var q string
			if rows.Scan(&q) == nil {
				pending = append(pending, "- "+q)
			}
		}
		if len(pending) > 0 {
			sb.WriteString("## Pending Questions (awaiting owner)\n")
			sb.WriteString(strings.Join(pending, "\n"))
			sb.WriteString("\n\n")
		}
	}

	sb.WriteString(`## Tool Guide
- manage_pages: read/update page HTML and JS
- manage_files: update CSS or JS assets (scope: "global" or "page")
- manage_endpoints: create/modify API, WebSocket, SSE, or upload endpoints
- manage_data: query/insert/update rows in data tables
- manage_schema: add/modify database tables and columns
- manage_layout: fix navigation or footer
- manage_memory: read/store site context
- manage_communication: ask the owner questions or suggest a rebuild
- After making changes, briefly confirm what you did
- Do NOT rebuild the entire site — make targeted fixes only
`)

	return sb.String()
}

// BuildDataLayerSummary generates a concise summary of the site's API endpoints,
// WebSocket endpoints, SSE streams, and upload endpoints for inclusion in prompts.
// Exported so brain/prompts.go can reuse it for the chat-wake prompt.
func BuildDataLayerSummary(siteDB *sql.DB) string {
	var sb strings.Builder

	// CRUD API endpoints.
	var apiLines []string
	if rows, err := siteDB.Query(`
		SELECT e.path, e.requires_auth, e.public_read, COALESCE(t.schema_def, '{}')
		FROM api_endpoints e
		LEFT JOIN dynamic_tables t ON e.table_name = t.table_name
		ORDER BY e.path`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var path, schemaDef string
			var requiresAuth, publicRead bool
			if rows.Scan(&path, &requiresAuth, &publicRead, &schemaDef) != nil {
				continue
			}

			// Parse schema fields.
			fields := ""
			if schemaDef != "" && schemaDef != "{}" {
				var cols map[string]string
				if json.Unmarshal([]byte(schemaDef), &cols) == nil {
					var ff []string
					for col, typ := range cols {
						if strings.EqualFold(typ, "PASSWORD") {
							continue
						}
						ff = append(ff, col)
					}
					sort.Strings(ff)
					fields = " — fields: " + strings.Join(ff, ", ")
				}
			}

			flags := ""
			if requiresAuth && publicRead {
				flags = " [AUTH] [PUBLIC_READ]"
			} else if requiresAuth {
				flags = " [AUTH]"
			}

			apiLines = append(apiLines, fmt.Sprintf("/api/%s%s%s", path, flags, fields))
		}
	}

	// WebSocket endpoints.
	var wsLines []string
	if rows, err := siteDB.Query("SELECT path, COALESCE(room_column, '') FROM ws_endpoints"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var path, roomCol string
			if rows.Scan(&path, &roomCol) != nil {
				continue
			}
			room := ""
			if roomCol != "" {
				room = fmt.Sprintf(" (rooms by %s)", roomCol)
			}
			wsLines = append(wsLines, fmt.Sprintf("/api/%s/ws%s", path, room))
		}
	}

	// SSE stream endpoints.
	var sseLines []string
	if rows, err := siteDB.Query("SELECT path, COALESCE(event_types, '') FROM stream_endpoints"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var path, events string
			if rows.Scan(&path, &events) != nil {
				continue
			}
			sseLines = append(sseLines, fmt.Sprintf("/api/%s/stream — events: %s", path, events))
		}
	}

	// Upload endpoints.
	var uploadLines []string
	if rows, err := siteDB.Query("SELECT path, COALESCE(allowed_types, '') FROM upload_endpoints"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var path, types string
			if rows.Scan(&path, &types) != nil {
				continue
			}
			uploadLines = append(uploadLines, fmt.Sprintf("POST /api/%s/upload — accepts: %s", path, types))
		}
	}

	// Only emit section if there's something to show.
	if len(apiLines) == 0 && len(wsLines) == 0 && len(sseLines) == 0 && len(uploadLines) == 0 {
		return ""
	}

	sb.WriteString("## Data Layer\n")
	if len(apiLines) > 0 {
		sb.WriteString("### API Endpoints\n")
		sb.WriteString(strings.Join(apiLines, "\n") + "\n\n")
	}
	if len(wsLines) > 0 {
		sb.WriteString("### WebSocket Endpoints\n")
		sb.WriteString(strings.Join(wsLines, "\n") + "\n\n")
	}
	if len(sseLines) > 0 {
		sb.WriteString("### SSE Stream Endpoints\n")
		sb.WriteString(strings.Join(sseLines, "\n") + "\n\n")
	}
	if len(uploadLines) > 0 {
		sb.WriteString("### Upload Endpoints\n")
		sb.WriteString(strings.Join(uploadLines, "\n") + "\n\n")
	}

	return sb.String()
}
