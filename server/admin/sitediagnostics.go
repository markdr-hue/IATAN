/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package admin

import (
	"net/http"
	"runtime"
	"strconv"
	"time"
)

// SiteDiagnosticsHandler handles health/integrity/errors for admin.
type SiteDiagnosticsHandler struct {
	deps *Deps
}

// Health returns runtime and site-level stats.
func (h *SiteDiagnosticsHandler) Health(w http.ResponseWriter, r *http.Request) {
	_, siteDB := requireSiteDB(w, r, h.deps.SiteDBManager)
	if siteDB == nil {
		return
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	var pageCount, tableCount int
	_ = siteDB.QueryRow("SELECT COUNT(*) FROM pages WHERE is_deleted = 0").Scan(&pageCount)
	_ = siteDB.QueryRow("SELECT COUNT(*) FROM dynamic_tables").Scan(&tableCount)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"runtime": map[string]interface{}{
			"go_version": runtime.Version(),
			"goroutines": runtime.NumGoroutine(),
			"cpus":       runtime.NumCPU(),
			"os":         runtime.GOOS,
			"arch":       runtime.GOARCH,
		},
		"memory": map[string]interface{}{
			"alloc_mb":    mem.Alloc / 1024 / 1024,
			"total_alloc": mem.TotalAlloc / 1024 / 1024,
			"sys_mb":      mem.Sys / 1024 / 1024,
			"num_gc":      mem.NumGC,
		},
		"site": map[string]interface{}{
			"pages":  pageCount,
			"tables": tableCount,
		},
	})
}

type integrityIssue struct {
	Level   string `json:"level"` // "warning" or "error"
	Message string `json:"message"`
}

// Integrity checks layout, pages, and asset consistency.
func (h *SiteDiagnosticsHandler) Integrity(w http.ResponseWriter, r *http.Request) {
	_, siteDB := requireSiteDB(w, r, h.deps.SiteDBManager)
	if siteDB == nil {
		return
	}

	var issues []integrityIssue

	// Check current pipeline stage — suppress structural warnings during early build
	// stages where layouts/assets/memories haven't been created yet.
	var currentStage string
	_ = siteDB.QueryRow("SELECT stage FROM pipeline_state WHERE id = 1").Scan(&currentStage)
	buildComplete := currentStage == "MONITORING" || currentStage == "COMPLETE" || currentStage == ""

	// Check layout exists
	var layoutCount int
	_ = siteDB.QueryRow("SELECT COUNT(*) FROM layouts").Scan(&layoutCount)
	if layoutCount == 0 {
		if buildComplete {
			issues = append(issues, integrityIssue{"error", "No layouts found. The site has no page structure."})
		} else {
			issues = append(issues, integrityIssue{"info", "No layouts yet — brain is still building (stage: " + currentStage + ")"})
		}
	}

	// Check pages reference valid layouts (only meaningful after layouts exist)
	if layoutCount > 0 {
		rows, err := siteDB.Query(
			"SELECT p.path, p.layout FROM pages p WHERE p.is_deleted = 0 AND p.layout != '' AND p.layout NOT IN (SELECT name FROM layouts)",
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var path, layout string
				if rows.Scan(&path, &layout) == nil {
					issues = append(issues, integrityIssue{"warning", "Page '" + path + "' references missing layout '" + layout + "'"})
				}
			}
		}

	}

	// Check CSS/JS assets exist
	var assetCount int
	_ = siteDB.QueryRow("SELECT COUNT(*) FROM assets").Scan(&assetCount)
	if assetCount == 0 {
		if buildComplete {
			issues = append(issues, integrityIssue{"warning", "No CSS/JS assets found. The site may have no styling."})
		} else {
			issues = append(issues, integrityIssue{"info", "No assets yet — brain is still building (stage: " + currentStage + ")"})
		}
	}

	if issues == nil {
		issues = []integrityIssue{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"issues": issues,
		"ok":     len(issues) == 0,
	})
}

type errorLogEntry struct {
	ID         int       `json:"id"`
	EventType  string    `json:"event_type"`
	Summary    string    `json:"summary"`
	Details    string    `json:"details"`
	TokensUsed int       `json:"tokens_used"`
	Model      string    `json:"model"`
	DurationMs int       `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

// Errors returns recent error entries from brain_log.
func (h *SiteDiagnosticsHandler) Errors(w http.ResponseWriter, r *http.Request) {
	_, siteDB := requireSiteDB(w, r, h.deps.SiteDBManager)
	if siteDB == nil {
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	rows, err := siteDB.Query(
		"SELECT id, event_type, summary, details, tokens_used, model, duration_ms, created_at FROM brain_log WHERE event_type LIKE '%error%' OR event_type LIKE '%fail%' ORDER BY created_at DESC LIMIT ?",
		limit,
	)
	if err != nil {
		writeJSON(w, http.StatusOK, []errorLogEntry{})
		return
	}
	defer rows.Close()

	var entries []errorLogEntry
	for rows.Next() {
		var e errorLogEntry
		if rows.Scan(&e.ID, &e.EventType, &e.Summary, &e.Details, &e.TokensUsed, &e.Model, &e.DurationMs, &e.CreatedAt) == nil {
			entries = append(entries, e)
		}
	}

	if entries == nil {
		entries = []errorLogEntry{}
	}

	writeJSON(w, http.StatusOK, entries)
}
