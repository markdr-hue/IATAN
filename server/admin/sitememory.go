/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// SiteMemoryHandler handles brain memory listing/deletion for admin.
type SiteMemoryHandler struct {
	deps *Deps
}

type memoryEntry struct {
	ID        int       `json:"id"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Category  string    `json:"category"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// List returns all memory entries for a site, optionally filtered by category.
func (h *SiteMemoryHandler) List(w http.ResponseWriter, r *http.Request) {
	_, siteDB := requireSiteDB(w, r, h.deps.SiteDBManager)
	if siteDB == nil {
		return
	}

	category := r.URL.Query().Get("category")

	var query string
	var args []interface{}
	if category != "" {
		query = "SELECT id, key, value, category, created_at, updated_at FROM memory WHERE category = ? ORDER BY category, key"
		args = []interface{}{category}
	} else {
		query = "SELECT id, key, value, category, created_at, updated_at FROM memory ORDER BY category, key"
	}

	rows, err := siteDB.Query(query, args...)
	if err != nil {
		writeJSON(w, http.StatusOK, []memoryEntry{})
		return
	}
	defer rows.Close()

	var memories []memoryEntry
	for rows.Next() {
		var m memoryEntry
		if err := rows.Scan(&m.ID, &m.Key, &m.Value, &m.Category, &m.CreatedAt, &m.UpdatedAt); err != nil {
			continue
		}
		memories = append(memories, m)
	}

	if memories == nil {
		memories = []memoryEntry{}
	}

	writeJSON(w, http.StatusOK, memories)
}

// Delete removes a memory entry by ID.
func (h *SiteMemoryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	_, siteDB := requireSiteDB(w, r, h.deps.SiteDBManager)
	if siteDB == nil {
		return
	}

	memoryID, err := strconv.Atoi(chi.URLParam(r, "memoryID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid memory ID")
		return
	}

	res, err := siteDB.ExecWrite(
		"DELETE FROM memory WHERE id = ?",
		memoryID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete memory")
		return
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "memory entry not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": memoryID})
}
