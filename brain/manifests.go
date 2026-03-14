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
)

// --- Manifest types: ground-truth snapshots extracted from DB after each build sub-phase ---

type SchemaManifest struct {
	Tables []ManifestTable `json:"tables"`
}

type ManifestTable struct {
	Name    string           `json:"name"`
	Columns []ManifestColumn `json:"columns"`
}

type ManifestColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type EndpointManifest struct {
	API    []ManifestAPI  `json:"api"`
	Auth   []ManifestAuth `json:"auth,omitempty"`
	WS     []ManifestWS   `json:"ws,omitempty"`
	Stream []ManifestWS   `json:"stream,omitempty"`
	Upload []ManifestWS   `json:"upload,omitempty"`
}

type ManifestAPI struct {
	Path          string   `json:"path"`
	Table         string   `json:"table"`
	Columns       []string `json:"columns"`
	Methods       string   `json:"methods,omitempty"`
	PublicColumns []string `json:"public_columns,omitempty"`
	RequiresAuth  bool     `json:"requires_auth,omitempty"`
	PublicRead    bool     `json:"public_read,omitempty"`
}

type ManifestAuth struct {
	Path           string `json:"path"`
	Table          string `json:"table"`
	UsernameColumn string `json:"username_column"`
}

type ManifestWS struct {
	Path  string `json:"path"`
	Table string `json:"table,omitempty"`
}

// --- Extraction functions: query real DB state after each sub-phase ---

// extractSchemaManifest reads all dynamic tables and their columns from the DB.
func extractSchemaManifest(db *sql.DB) (*SchemaManifest, error) {
	rows, err := db.Query("SELECT table_name FROM dynamic_tables ORDER BY table_name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var manifest SchemaManifest
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			continue
		}
		cols := getTableColumnsTyped(db, tableName)
		manifest.Tables = append(manifest.Tables, ManifestTable{
			Name:    tableName,
			Columns: cols,
		})
	}
	return &manifest, nil
}

// getTableColumns returns the set of column names for a table.
func getTableColumns(db *sql.DB, tableName string) map[string]bool {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return nil
	}
	defer rows.Close()
	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk)
		cols[name] = true
	}
	return cols
}

// getTableColumnsTyped returns column names and types for a table via PRAGMA.
func getTableColumnsTyped(db *sql.DB, tableName string) []ManifestColumn {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return nil
	}
	defer rows.Close()

	var cols []ManifestColumn
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			continue
		}
		cols = append(cols, ManifestColumn{Name: name, Type: colType})
	}
	return cols
}

// extractEndpointManifest reads all endpoint types from the DB and resolves their bound table columns.
func extractEndpointManifest(db *sql.DB) (*EndpointManifest, error) {
	var manifest EndpointManifest

	// API endpoints.
	apiRows, err := db.Query("SELECT path, table_name, methods, requires_auth, public_read, COALESCE(public_columns, '') FROM api_endpoints ORDER BY path")
	if err == nil {
		defer apiRows.Close()
		for apiRows.Next() {
			var path, tableName string
			var methods, publicColumnsJSON sql.NullString
			var requiresAuth, publicRead bool
			apiRows.Scan(&path, &tableName, &methods, &requiresAuth, &publicRead, &publicColumnsJSON)
			cols := getTableColumns(db, tableName)
			colNames := make([]string, 0, len(cols))
			for c := range cols {
				colNames = append(colNames, c)
			}
			api := ManifestAPI{
				Path:         path,
				Table:        tableName,
				Columns:      colNames,
				RequiresAuth: requiresAuth,
				PublicRead:   publicRead,
			}
			if methods.Valid {
				api.Methods = methods.String
			}
			// Parse public_columns JSON array.
			if publicColumnsJSON.Valid && publicColumnsJSON.String != "" {
				var pubCols []string
				if json.Unmarshal([]byte(publicColumnsJSON.String), &pubCols) == nil && len(pubCols) > 0 {
					api.PublicColumns = pubCols
				}
			}
			manifest.API = append(manifest.API, api)
		}
	}

	// Auth endpoints.
	authRows, err := db.Query("SELECT path, table_name, username_column FROM auth_endpoints ORDER BY path")
	if err == nil {
		defer authRows.Close()
		for authRows.Next() {
			var path, tableName, usernameCol string
			authRows.Scan(&path, &tableName, &usernameCol)
			manifest.Auth = append(manifest.Auth, ManifestAuth{
				Path:           path,
				Table:          tableName,
				UsernameColumn: usernameCol,
			})
		}
	}

	// WebSocket endpoints.
	wsRows, err := db.Query("SELECT path, COALESCE(write_to_table, '') FROM ws_endpoints ORDER BY path")
	if err == nil {
		defer wsRows.Close()
		for wsRows.Next() {
			var path, table string
			wsRows.Scan(&path, &table)
			manifest.WS = append(manifest.WS, ManifestWS{Path: path, Table: table})
		}
	}

	// Stream (SSE) endpoints.
	streamRows, err := db.Query("SELECT path FROM stream_endpoints ORDER BY path")
	if err == nil {
		defer streamRows.Close()
		for streamRows.Next() {
			var path string
			streamRows.Scan(&path)
			manifest.Stream = append(manifest.Stream, ManifestWS{Path: path})
		}
	}

	// Upload endpoints.
	uploadRows, err := db.Query("SELECT path, COALESCE(table_name, '') FROM upload_endpoints ORDER BY path")
	if err == nil {
		defer uploadRows.Close()
		for uploadRows.Next() {
			var path, table string
			uploadRows.Scan(&path, &table)
			manifest.Upload = append(manifest.Upload, ManifestWS{Path: path, Table: table})
		}
	}

	return &manifest, nil
}

// --- Manifest serialization for prompt injection ---

// buildCrashRecoveryManifest queries the DB for everything already built and
// returns a compact text block the BUILD prompt can inject so the LLM knows
// what to skip on resume. Returns "" if nothing has been built yet.
func buildCrashRecoveryManifest(db *sql.DB) string {
	var parts []string

	// Tables.
	if schema, err := extractSchemaManifest(db); err == nil && len(schema.Tables) > 0 {
		var names []string
		for _, t := range schema.Tables {
			var cols []string
			for _, c := range t.Columns {
				cols = append(cols, c.Name)
			}
			names = append(names, fmt.Sprintf("%s(%s)", t.Name, strings.Join(cols, ",")))
		}
		parts = append(parts, "Tables: "+strings.Join(names, "; "))
	}

	// Endpoints.
	if ep, err := extractEndpointManifest(db); err == nil {
		var epParts []string
		for _, a := range ep.API {
			epParts = append(epParts, "API:"+a.Path+"→"+a.Table)
		}
		for _, a := range ep.Auth {
			epParts = append(epParts, "Auth:"+a.Path+"→"+a.Table)
		}
		for _, w := range ep.WS {
			epParts = append(epParts, "WS:"+w.Path)
		}
		for _, s := range ep.Stream {
			epParts = append(epParts, "Stream:"+s.Path)
		}
		for _, u := range ep.Upload {
			epParts = append(epParts, "Upload:"+u.Path)
		}
		if len(epParts) > 0 {
			parts = append(parts, "Endpoints: "+strings.Join(epParts, "; "))
		}
	}

	// Pages.
	pageRows, err := db.Query("SELECT path, title FROM pages WHERE is_deleted = 0 ORDER BY path")
	if err == nil {
		defer pageRows.Close()
		var pages []string
		for pageRows.Next() {
			var path, title string
			pageRows.Scan(&path, &title)
			pages = append(pages, path+" ("+title+")")
		}
		if len(pages) > 0 {
			parts = append(parts, "Pages: "+strings.Join(pages, "; "))
		}
	}

	// Layouts.
	layoutRows, err := db.Query("SELECT name FROM layouts ORDER BY name")
	if err == nil {
		defer layoutRows.Close()
		var layouts []string
		for layoutRows.Next() {
			var name string
			layoutRows.Scan(&name)
			layouts = append(layouts, name)
		}
		if len(layouts) > 0 {
			parts = append(parts, "Layouts: "+strings.Join(layouts, ", "))
		}
	}

	// CSS files.
	cssRows, err := db.Query("SELECT filename FROM assets WHERE filename LIKE '%.css' ORDER BY filename")
	if err == nil {
		defer cssRows.Close()
		var css []string
		for cssRows.Next() {
			var name string
			cssRows.Scan(&name)
			css = append(css, name)
		}
		if len(css) > 0 {
			parts = append(parts, "CSS: "+strings.Join(css, ", "))
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return "## Already Built (resume from here — do NOT recreate these)\n" + strings.Join(parts, "\n")
}

