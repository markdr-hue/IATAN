/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package admin

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// SiteEndpointsHandler handles API endpoint browsing for site detail views.
type SiteEndpointsHandler struct {
	deps *Deps
}

type siteEndpoint struct {
	ID            int       `json:"id"`
	SiteID        int       `json:"site_id"`
	Path          string    `json:"path"`
	TableName     string    `json:"table_name"`
	Methods       string    `json:"methods"`
	PublicColumns *string   `json:"public_columns"`
	RequiresAuth  bool      `json:"requires_auth"`
	RateLimit     int       `json:"rate_limit"`
	CreatedAt     time.Time `json:"created_at"`
}

// List returns all API endpoints for a site.
func (h *SiteEndpointsHandler) List(w http.ResponseWriter, r *http.Request) {
	siteID, siteDB := requireSiteDB(w, r, h.deps.SiteDBManager)
	if siteDB == nil {
		return
	}

	rows, err := siteDB.Query(
		`SELECT id, path, table_name, methods, public_columns, requires_auth, rate_limit, created_at
		 FROM api_endpoints ORDER BY path ASC`,
	)
	if err != nil {
		writeJSON(w, http.StatusOK, []siteEndpoint{})
		return
	}
	defer rows.Close()

	var endpoints []siteEndpoint
	for rows.Next() {
		var e siteEndpoint
		e.SiteID = siteID
		var publicCols sql.NullString
		if err := rows.Scan(&e.ID, &e.Path, &e.TableName, &e.Methods, &publicCols, &e.RequiresAuth, &e.RateLimit, &e.CreatedAt); err != nil {
			continue
		}
		if publicCols.Valid {
			e.PublicColumns = &publicCols.String
		}
		endpoints = append(endpoints, e)
	}

	if endpoints == nil {
		endpoints = []siteEndpoint{}
	}

	writeJSON(w, http.StatusOK, endpoints)
}

// Delete removes an API endpoint by ID.
func (h *SiteEndpointsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	_, siteDB := requireSiteDB(w, r, h.deps.SiteDBManager)
	if siteDB == nil {
		return
	}

	endpointID, err := strconv.Atoi(chi.URLParam(r, "endpointID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid endpoint ID")
		return
	}

	_, err = siteDB.ExecWrite(
		"DELETE FROM api_endpoints WHERE id = ?",
		endpointID,
	)
	if err != nil {
		h.deps.Logger.Error("failed to delete endpoint", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete endpoint")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
