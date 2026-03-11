/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	js_parser "github.com/dop251/goja/parser"
)

const maxFileSize = 10 << 20    // 10 MB
const maxFileVersions = 10      // keep last N versions per file

// storageConfig holds the table/path/URL differences between "assets" and "files".
type storageConfig struct {
	table    string // DB table name
	dirName  string // subdirectory under data/sites/{id}/
	urlBase  string // URL prefix for serving
	metaCol  string // metadata column name (alt_text for assets, description for files)
	metaDesc string // human label for the metadata field
}

var storageConfigs = map[string]storageConfig{
	"assets": {table: "assets", dirName: "assets", urlBase: "/assets/", metaCol: "alt_text", metaDesc: "Alt text for images"},
	"files":  {table: "files", dirName: "files", urlBase: "/files/", metaCol: "description", metaDesc: "Description of the file"},
}

func getStorageConfig(args map[string]interface{}) (storageConfig, error) {
	s, _ := args["storage"].(string)
	if s == "" {
		s = "assets" // default
	}
	cfg, ok := storageConfigs[s]
	if !ok {
		return storageConfig{}, fmt.Errorf("invalid storage: %q (use \"assets\" or \"files\")", s)
	}
	return cfg, nil
}

// storageDir returns the storage directory for a site, creating it if needed.
func storageDir(siteID int, dirName string) (string, error) {
	dir := filepath.Join("data", "sites", fmt.Sprintf("%d", siteID), dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s directory: %w", dirName, err)
	}
	return dir, nil
}

// sanitizePath cleans a filename/path for safe storage.
// Allows forward-slash-separated subdirectories but rejects path traversal.
func sanitizePath(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") {
		name = strings.TrimLeft(name, "/")
	}
	parts := strings.Split(name, "/")
	var clean []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "." || p == ".." {
			continue
		}
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return ""
	}
	return strings.Join(clean, "/")
}

// sanitizeFilename sanitizes a path and strips a redundant storage prefix.
// LLMs often send "assets/css/styles.css" when the filename should be
// "css/styles.css" (the "/assets/" prefix is added by the URL routing).
func sanitizeFilename(name, storageDir string) string {
	name = sanitizePath(name)
	if prefix := storageDir + "/"; strings.HasPrefix(name, prefix) {
		name = name[len(prefix):]
	}
	return name
}

// inferContentType returns a MIME type based on the file extension.
func inferContentType(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".json":
		return "application/json"
	case ".html", ".htm":
		return "text/html"
	case ".xml":
		return "application/xml"
	case ".txt":
		return "text/plain"
	case ".csv":
		return "text/csv"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".pdf":
		return "application/pdf"
	case ".ico":
		return "image/x-icon"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".zip":
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}

// isTextContentType returns true if the content type is text-based.
func isTextContentType(ct string) bool {
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/javascript", "application/json", "application/xml", "image/svg+xml":
		return true
	}
	return false
}

// decodeFileData decodes content (text) or data (base64) into bytes.
func decodeFileData(content, dataB64 string) ([]byte, error) {
	if content != "" {
		return []byte(content), nil
	}
	// Strip data URI prefix if present.
	if idx := strings.Index(dataB64, ","); idx != -1 && strings.Contains(dataB64[:idx], "base64") {
		dataB64 = dataB64[idx+1:]
	}
	return base64.StdEncoding.DecodeString(dataB64)
}

// writeFileToDisk writes data to the storage directory, creating subdirs as needed.
func writeFileToDisk(siteID int, dirName, filename string, data []byte) (string, error) {
	dir, err := storageDir(siteID, dirName)
	if err != nil {
		return "", err
	}
	storagePath := filepath.Join(dir, filepath.FromSlash(filename))
	if subDir := filepath.Dir(storagePath); subDir != dir {
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			return "", fmt.Errorf("creating subdirectory: %w", err)
		}
	}
	if err := os.WriteFile(storagePath, data, 0o644); err != nil {
		return "", fmt.Errorf("writing file: %w", err)
	}
	return storagePath, nil
}

// storageParam is the shared parameter definition for the storage field.
var storageParam = map[string]interface{}{
	"type":        "string",
	"description": "Storage type: \"assets\" (design files at /assets/) or \"files\" (downloads at /files/). Default: assets.",
	"enum":        []string{"assets", "files"},
}

// ---------------------------------------------------------------------------
// FilesTool — unified manage_files tool
// ---------------------------------------------------------------------------

// FilesTool consolidates save, get, list, delete, and rename into a single tool.
type FilesTool struct{}

func (t *FilesTool) Name() string { return "manage_files" }
func (t *FilesTool) Description() string {
	return `Manage site files. Actions: save (create/update file), get (read file), list (list all), delete (remove file), rename, history (view versions), rollback (restore version). Text files (CSS/JS/SVG) are automatically versioned on save. Use storage="assets" for CSS/JS/images (served at /assets/), storage="files" for downloads (served at /files/).`
}

func (t *FilesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"save", "get", "list", "delete", "rename", "history", "rollback"},
			},
			"storage":      storageParam,
			"filename":     map[string]interface{}{"type": "string", "description": "Path (e.g. styles.css, css/main.css)"},
			"content_type": map[string]interface{}{"type": "string", "description": "MIME type. Auto-detected if omitted."},
			"content":      map[string]interface{}{"type": "string", "description": "Text content (CSS, JS, SVG, HTML)"},
			"data":         map[string]interface{}{"type": "string", "description": "Base64 content for binary files"},
			"scope":        map[string]interface{}{"type": "string", "description": "Asset scope: \"global\" (auto-injected on every page, default) or \"page\" (only injected when a page lists it in its assets array)", "enum": []string{"global", "page"}},
			"description":  map[string]interface{}{"type": "string", "description": "Alt text (images) or description"},
			"old_filename": map[string]interface{}{"type": "string", "description": "Current filename (rename)"},
			"new_filename": map[string]interface{}{"type": "string", "description": "New filename (rename)"},
			"version":      map[string]interface{}{"type": "integer", "description": "Version number to restore (rollback)"},
		},
		"required": []string{"action"},
	}
}

func (t *FilesTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	return DispatchAction(ctx, args, map[string]ActionHandler{
		"save":     t.executeSave,
		"get":      t.executeGet,
		"list":     t.executeList,
		"delete":   t.executeDelete,
		"rename":   t.executeRename,
		"history":  t.executeHistory,
		"rollback": t.executeRollback,
	}, func(a map[string]interface{}) string {
		if _, has := a["version"]; has {
			return "rollback"
		}
		if _, has := a["content"]; has {
			return "save"
		}
		if _, has := a["data"]; has {
			return "save"
		}
		if _, has := a["new_filename"]; has {
			return "rename"
		}
		if _, has := a["filename"]; has {
			return "get"
		}
		return "list"
	})
}

func (t *FilesTool) executeSave(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	cfg, err := getStorageConfig(args)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	filename, _ := args["filename"].(string)
	if filename == "" {
		return &Result{Success: false, Error: "filename is required"}, nil
	}
	content, _ := args["content"].(string)
	dataB64, _ := args["data"].(string)
	if content == "" && dataB64 == "" {
		return &Result{Success: false, Error: "either content or data (base64) is required"}, nil
	}
	contentType, _ := args["content_type"].(string)
	desc, _ := args["description"].(string)
	scope, _ := args["scope"].(string)
	if scope == "" {
		scope = "global"
	}

	filename = sanitizeFilename(filename, cfg.dirName)
	if filename == "" {
		return &Result{Success: false, Error: "invalid filename"}, nil
	}

	fileData, err := decodeFileData(content, dataB64)
	if err != nil {
		return &Result{Success: false, Error: "invalid base64 data"}, nil
	}
	if len(fileData) > maxFileSize {
		return &Result{Success: false, Error: fmt.Sprintf("file too large (%d bytes, max %d)", len(fileData), maxFileSize)}, nil
	}

	// Always infer from extension for known types — LLMs sometimes pass
	// content_type: "text/plain" for CSS/JS/SVG which breaks browser rendering.
	if inferred := inferContentType(filename); inferred != "application/octet-stream" {
		contentType = inferred
	} else if contentType == "" {
		contentType = inferred
	}

	// Before overwrite: capture existing text file into version history.
	if isTextContentType(contentType) {
		var oldStoragePath sql.NullString
		qErr := ctx.DB.QueryRow(
			fmt.Sprintf("SELECT storage_path FROM %s WHERE filename = ?", cfg.table),
			filename,
		).Scan(&oldStoragePath)
		if qErr == nil {
			// File exists — read current content from disk and save version.
			oldPath := oldStoragePath.String
			if oldPath == "" {
				oldPath = filepath.Join("data", "sites", fmt.Sprintf("%d", ctx.SiteID), cfg.dirName, filepath.FromSlash(filename))
			}
			if oldData, readErr := os.ReadFile(oldPath); readErr == nil {
				var maxVer int
				ctx.DB.QueryRow(
					"SELECT COALESCE(MAX(version_number), 0) FROM file_versions WHERE storage_type = ? AND filename = ?",
					cfg.table, filename,
				).Scan(&maxVer)
				ctx.DB.Exec(
					`INSERT INTO file_versions (storage_type, filename, content, content_type, size, version_number, changed_by)
					 VALUES (?, ?, ?, ?, ?, ?, 'brain')`,
					cfg.table, filename, string(oldData), contentType, len(oldData), maxVer+1,
				)
				// Prune: keep only last 10 versions.
				if maxVer+1 > maxFileVersions {
					ctx.DB.Exec(
						"DELETE FROM file_versions WHERE storage_type = ? AND filename = ? AND version_number <= ?",
						cfg.table, filename, maxVer+1-maxFileVersions,
					)
				}
			}
		}
	}

	storagePath, err := writeFileToDisk(ctx.SiteID, cfg.dirName, filename, fileData)
	if err != nil {
		return nil, err
	}

	if cfg.table == "assets" {
		_, err = ctx.DB.Exec(
			`INSERT INTO assets (filename, content_type, size, storage_path, alt_text, scope)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(filename) DO UPDATE SET
			   content_type = excluded.content_type,
			   size = excluded.size,
			   storage_path = excluded.storage_path,
			   alt_text = excluded.alt_text,
			   scope = excluded.scope`,
			filename, contentType, len(fileData), storagePath, desc, scope,
		)
	} else {
		_, err = ctx.DB.Exec(
			fmt.Sprintf(
				`INSERT INTO %s (filename, content_type, size, storage_path, %s)
				 VALUES (?, ?, ?, ?, ?)
				 ON CONFLICT(filename) DO UPDATE SET
				   content_type = excluded.content_type,
				   size = excluded.size,
				   storage_path = excluded.storage_path,
				   %s = excluded.%s`,
				cfg.table, cfg.metaCol, cfg.metaCol, cfg.metaCol,
			),
			filename, contentType, len(fileData), storagePath, desc,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("saving record: %w", err)
	}

	resultData := map[string]interface{}{
		"filename":     filename,
		"content_type": contentType,
		"size":         len(fileData),
		"url":          cfg.urlBase + filename,
	}
	if cfg.table == "assets" {
		resultData["scope"] = scope
	}

	// Post-save validation for JS files — syntax errors block the save.
	if strings.HasSuffix(strings.ToLower(filename), ".js") {
		if jsErrors := validateJSContent(filename, fileData); len(jsErrors) > 0 {
			resultData["js_errors"] = jsErrors
			return &Result{
				Success: false,
				Error:   "JS syntax errors — fix and re-save: " + jsErrors[0],
				Data:    resultData,
			}, nil
		}
	}

	// Post-save validation for CSS files.
	if strings.HasSuffix(strings.ToLower(filename), ".css") {
		if warnings := validateCSSContent(filename, fileData); len(warnings) > 0 {
			resultData["warnings"] = warnings
		}
	}

	return &Result{Success: true, Data: resultData}, nil
}

func (t *FilesTool) executeGet(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	cfg, err := getStorageConfig(args)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}
	filename, _ := args["filename"].(string)
	if filename == "" {
		return &Result{Success: false, Error: "filename is required"}, nil
	}
	filename = sanitizeFilename(filename, cfg.dirName)
	if filename == "" {
		return &Result{Success: false, Error: "invalid filename"}, nil
	}

	var storagePath string
	var contentType sql.NullString
	var size sql.NullInt64
	err = ctx.DB.QueryRow(
		fmt.Sprintf("SELECT storage_path, content_type, size FROM %s WHERE filename = ?", cfg.table),
		filename,
	).Scan(&storagePath, &contentType, &size)
	if err == sql.ErrNoRows {
		return &Result{Success: false, Error: "not found"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying %s: %w", cfg.table, err)
	}

	if storagePath == "" {
		storagePath = filepath.Join("data", "sites", fmt.Sprintf("%d", ctx.SiteID), cfg.dirName, filepath.FromSlash(filename))
	}
	fileData, err := os.ReadFile(storagePath)
	if err != nil {
		return &Result{Success: false, Error: "file not found on disk"}, nil
	}

	ct := contentType.String
	result := map[string]interface{}{
		"filename":     filename,
		"content_type": ct,
		"size":         len(fileData),
		"url":          cfg.urlBase + filename,
	}
	if isTextContentType(ct) {
		result["content"] = string(fileData)
	} else {
		result["data"] = base64.StdEncoding.EncodeToString(fileData)
	}

	return &Result{Success: true, Data: result}, nil
}

func (t *FilesTool) executeList(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	cfg, err := getStorageConfig(args)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	var items []map[string]interface{}
	if cfg.table == "assets" {
		rows, err := ctx.DB.Query("SELECT id, filename, content_type, size, alt_text, scope, created_at FROM assets ORDER BY filename")
		if err != nil {
			return nil, fmt.Errorf("listing assets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id int
			var filename string
			var contentType, meta, scope sql.NullString
			var size sql.NullInt64
			var createdAt time.Time
			if err := rows.Scan(&id, &filename, &contentType, &size, &meta, &scope, &createdAt); err != nil {
				return nil, fmt.Errorf("scanning assets: %w", err)
			}
			items = append(items, map[string]interface{}{
				"id":           id,
				"filename":     filename,
				"content_type": contentType.String,
				"size":         size.Int64,
				"description":  meta.String,
				"scope":        scope.String,
				"url":          cfg.urlBase + filename,
				"created_at":   createdAt,
			})
		}
	} else {
		rows, err := ctx.DB.Query(
			fmt.Sprintf("SELECT id, filename, content_type, size, %s, created_at FROM %s ORDER BY filename", cfg.metaCol, cfg.table),
		)
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", cfg.table, err)
		}
		defer rows.Close()
		for rows.Next() {
			var id int
			var filename string
			var contentType, meta sql.NullString
			var size sql.NullInt64
			var createdAt time.Time
			if err := rows.Scan(&id, &filename, &contentType, &size, &meta, &createdAt); err != nil {
				return nil, fmt.Errorf("scanning %s: %w", cfg.table, err)
			}
			items = append(items, map[string]interface{}{
				"id":           id,
				"filename":     filename,
				"content_type": contentType.String,
				"size":         size.Int64,
				"description":  meta.String,
				"url":          cfg.urlBase + filename,
				"created_at":   createdAt,
			})
		}
	}

	return &Result{Success: true, Data: items}, nil
}

func (t *FilesTool) executeDelete(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	cfg, err := getStorageConfig(args)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}
	filename, _ := args["filename"].(string)
	if filename == "" {
		return &Result{Success: false, Error: "filename is required"}, nil
	}
	filename = sanitizeFilename(filename, cfg.dirName)
	if filename == "" {
		return &Result{Success: false, Error: "invalid filename"}, nil
	}

	var storagePath string
	err = ctx.DB.QueryRow(
		fmt.Sprintf("SELECT storage_path FROM %s WHERE filename = ?", cfg.table),
		filename,
	).Scan(&storagePath)
	if err == sql.ErrNoRows {
		return &Result{Success: false, Error: "not found"}, nil
	}

	_, err = ctx.DB.Exec(
		fmt.Sprintf("DELETE FROM %s WHERE filename = ?", cfg.table),
		filename,
	)
	if err != nil {
		return nil, fmt.Errorf("deleting record: %w", err)
	}

	if storagePath != "" {
		if err := os.Remove(storagePath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("removing file from disk: %w", err)
		}
	}

	LogDestructiveAction(ctx, "manage_files", "delete", filename)

	return &Result{Success: true, Data: map[string]interface{}{"deleted": filename}}, nil
}

func (t *FilesTool) executeRename(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	cfg, err := getStorageConfig(args)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}
	oldName, _ := args["old_filename"].(string)
	newName, _ := args["new_filename"].(string)
	if oldName == "" || newName == "" {
		return &Result{Success: false, Error: "old_filename and new_filename are required"}, nil
	}

	oldName = sanitizeFilename(oldName, cfg.dirName)
	newName = sanitizeFilename(newName, cfg.dirName)
	if oldName == "" || newName == "" {
		return &Result{Success: false, Error: "invalid filename"}, nil
	}

	var storagePath string
	err = ctx.DB.QueryRow(
		fmt.Sprintf("SELECT storage_path FROM %s WHERE filename = ?", cfg.table),
		oldName,
	).Scan(&storagePath)
	if err == sql.ErrNoRows {
		return &Result{Success: false, Error: "not found"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying %s: %w", cfg.table, err)
	}

	dir, err := storageDir(ctx.SiteID, cfg.dirName)
	if err != nil {
		return nil, err
	}
	newStoragePath := filepath.Join(dir, filepath.FromSlash(newName))
	if subDir := filepath.Dir(newStoragePath); subDir != dir {
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating directory: %w", err)
		}
	}

	if storagePath != "" {
		if err := os.Rename(storagePath, newStoragePath); err != nil {
			return nil, fmt.Errorf("renaming file on disk: %w", err)
		}
	}

	newContentType := inferContentType(newName)
	_, err = ctx.DB.Exec(
		fmt.Sprintf("UPDATE %s SET filename = ?, storage_path = ?, content_type = ? WHERE filename = ?", cfg.table),
		newName, newStoragePath, newContentType, oldName,
	)
	if err != nil {
		return nil, fmt.Errorf("updating record: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"old_filename": oldName,
		"new_filename": newName,
		"url":          cfg.urlBase + newName,
	}}, nil
}

// ---------------------------------------------------------------------------
// history — view version history for a file
// ---------------------------------------------------------------------------

func (t *FilesTool) executeHistory(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	cfg, err := getStorageConfig(args)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}
	filename, _ := args["filename"].(string)
	if filename == "" {
		return &Result{Success: false, Error: "filename is required"}, nil
	}
	filename = sanitizeFilename(filename, cfg.dirName)
	if filename == "" {
		return &Result{Success: false, Error: "invalid filename"}, nil
	}

	limit := 20
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	rows, err := ctx.DB.Query(
		"SELECT version_number, content_type, size, changed_by, created_at FROM file_versions WHERE storage_type = ? AND filename = ? ORDER BY version_number DESC LIMIT ?",
		cfg.table, filename, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying file history: %w", err)
	}
	defer rows.Close()

	var versions []map[string]interface{}
	for rows.Next() {
		var ver, size int
		var contentType, changedBy sql.NullString
		var createdAt time.Time
		if err := rows.Scan(&ver, &contentType, &size, &changedBy, &createdAt); err != nil {
			continue
		}
		versions = append(versions, map[string]interface{}{
			"version":      ver,
			"content_type": contentType.String,
			"size":         size,
			"changed_by":   changedBy.String,
			"created_at":   createdAt,
		})
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"filename": filename,
		"versions": versions,
	}}, nil
}

// ---------------------------------------------------------------------------
// rollback — restore a file to a previous version
// ---------------------------------------------------------------------------

func (t *FilesTool) executeRollback(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	cfg, err := getStorageConfig(args)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}
	filename, _ := args["filename"].(string)
	if filename == "" {
		return &Result{Success: false, Error: "filename is required"}, nil
	}
	filename = sanitizeFilename(filename, cfg.dirName)
	if filename == "" {
		return &Result{Success: false, Error: "invalid filename"}, nil
	}

	version, ok := args["version"].(float64)
	if !ok || version < 1 {
		return &Result{Success: false, Error: "version (number) is required"}, nil
	}

	// Load the requested version content.
	var versionContent string
	var versionCT sql.NullString
	err = ctx.DB.QueryRow(
		"SELECT content, content_type FROM file_versions WHERE storage_type = ? AND filename = ? AND version_number = ?",
		cfg.table, filename, int(version),
	).Scan(&versionContent, &versionCT)
	if err == sql.ErrNoRows {
		return &Result{Success: false, Error: fmt.Sprintf("version %d not found for %s", int(version), filename)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying version: %w", err)
	}

	// Save current file as a new version first (so rollback is reversible).
	oldPath := filepath.Join("data", "sites", fmt.Sprintf("%d", ctx.SiteID), cfg.dirName, filepath.FromSlash(filename))
	if oldData, readErr := os.ReadFile(oldPath); readErr == nil {
		var maxVer int
		ctx.DB.QueryRow(
			"SELECT COALESCE(MAX(version_number), 0) FROM file_versions WHERE storage_type = ? AND filename = ?",
			cfg.table, filename,
		).Scan(&maxVer)
		ctx.DB.Exec(
			`INSERT INTO file_versions (storage_type, filename, content, content_type, size, version_number, changed_by)
			 VALUES (?, ?, ?, ?, ?, ?, 'rollback')`,
			cfg.table, filename, string(oldData), versionCT.String, len(oldData), maxVer+1,
		)
	}

	// Write restored content to disk.
	restoredData := []byte(versionContent)
	storagePath, err := writeFileToDisk(ctx.SiteID, cfg.dirName, filename, restoredData)
	if err != nil {
		return nil, fmt.Errorf("writing restored file: %w", err)
	}

	// Update the main table record.
	contentType := versionCT.String
	if contentType == "" {
		contentType = inferContentType(filename)
	}
	ctx.DB.Exec(
		fmt.Sprintf("UPDATE %s SET size = ?, storage_path = ?, content_type = ? WHERE filename = ?", cfg.table),
		len(restoredData), storagePath, contentType, filename,
	)

	return &Result{Success: true, Data: map[string]interface{}{
		"filename":         filename,
		"restored_version": int(version),
		"size":             len(restoredData),
		"url":              cfg.urlBase + filename,
	}}, nil
}

// ---------------------------------------------------------------------------
// JS syntax validation
// ---------------------------------------------------------------------------

// validateJSContent parses a JS file and returns syntax error warnings.
// Uses goja's ES5.1 parser to catch real syntax errors (unexpected tokens,
// unclosed braces, malformed expressions). Returns at most 3 warnings.
func validateJSContent(filename string, content []byte) []string {
	_, err := js_parser.ParseFile(nil, filename, string(content), 0)
	if err == nil {
		return nil
	}

	// goja returns a parser.ErrorList for multiple errors.
	if errList, ok := err.(js_parser.ErrorList); ok {
		var warnings []string
		for i, e := range errList {
			if i >= 3 {
				warnings = append(warnings, fmt.Sprintf("... and %d more syntax errors", len(errList)-3))
				break
			}
			warnings = append(warnings, fmt.Sprintf("JS syntax error at %s: %s", e.Position, e.Message))
		}
		return warnings
	}

	// Single error fallback.
	return []string{fmt.Sprintf("JS syntax error: %s", err.Error())}
}

// validateCSSContent performs basic CSS syntax validation:
// brace matching, unclosed strings, and empty file detection.
func validateCSSContent(filename string, content []byte) []string {
	css := string(content)

	if strings.TrimSpace(css) == "" {
		return []string{fmt.Sprintf("CSS file %s is empty", filename)}
	}

	var warnings []string

	// Check brace matching (skip braces inside strings and comments).
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inComment := false
	for i := 0; i < len(css); i++ {
		c := css[i]
		if inComment {
			if c == '*' && i+1 < len(css) && css[i+1] == '/' {
				inComment = false
				i++ // skip /
			}
			continue
		}
		if c == '/' && i+1 < len(css) && css[i+1] == '*' {
			inComment = true
			i++ // skip *
			continue
		}
		if inSingleQuote {
			if c == '\'' && (i == 0 || css[i-1] != '\\') {
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if c == '"' && (i == 0 || css[i-1] != '\\') {
				inDoubleQuote = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingleQuote = true
		case '"':
			inDoubleQuote = true
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				warnings = append(warnings, fmt.Sprintf("CSS syntax error in %s: unexpected closing brace '}'", filename))
				depth = 0
			}
		}
	}
	if depth > 0 {
		warnings = append(warnings, fmt.Sprintf("CSS syntax error in %s: %d unclosed brace(s) '{'", filename, depth))
	}
	if inComment {
		warnings = append(warnings, fmt.Sprintf("CSS syntax error in %s: unclosed comment /* ... */", filename))
	}

	return warnings
}

func (t *FilesTool) MaxResultSize() int { return 16000 }

func (t *FilesTool) Summarize(result string) string {
	r, data, _, ok := parseSummaryResult(result)
	if !ok {
		return summarizeTruncate(result, 200)
	}
	if !r.Success {
		return summarizeError(r.Error)
	}
	if data == nil {
		return summarizeTruncate(result, 300)
	}
	if content, ok := data["content"].(string); ok && content != "" {
		filename, _ := data["filename"].(string)
		return fmt.Sprintf(`{"success":true,"summary":"Read file %s (%d chars)"}`, filename, len(content))
	}
	if warnings, ok := data["warnings"]; ok {
		filename, _ := data["filename"].(string)
		wJSON, _ := json.Marshal(warnings)
		return fmt.Sprintf(`{"success":true,"file":"%s","warnings":%s,"ACTION_REQUIRED":"Fix JS errors"}`, filename, wJSON)
	}
	filename, _ := data["filename"].(string)
	size, _ := data["size"].(float64)
	return fmt.Sprintf(`{"success":true,"summary":"File %s (%d bytes)"}`, filename, int(size))
}
