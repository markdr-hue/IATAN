/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package chat

import (
	"database/sql"
	"fmt"
	"strings"
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

	sb.WriteString("You can use the available tools to look up details, manage pages, data, etc.\n")

	return sb.String()
}
