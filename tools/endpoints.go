/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/markdr-hue/IATAN/security"
)

// ---------------------------------------------------------------------------
// EndpointsTool — unified manage_endpoints tool
// ---------------------------------------------------------------------------

// EndpointsTool consolidates API endpoint and auth endpoint management into a
// single tool with actions: create_api, list_api, delete_api,
// create_auth, list_auth, delete_auth, create_upload, create_stream,
// create_websocket, verify_password, test, create_oauth, list_oauth, delete_oauth.
type EndpointsTool struct{}

func (t *EndpointsTool) Name() string { return "manage_endpoints" }
func (t *EndpointsTool) Description() string {
	return "Manage API and auth endpoints. Actions: create_api, list_api, delete_api, create_auth, list_auth, delete_auth, create_upload, create_stream, create_websocket, verify_password, test, create_oauth, list_oauth, delete_oauth. " +
		"API endpoints serve CRUD at /api/{path}. Auth endpoints create /register, /login, /me routes using JWT. " +
		"OAuth endpoints add social login via /api/{auth_path}/oauth/{provider} with automatic callback handling. " +
		"Upload endpoints accept multipart file uploads at /api/{path}/upload. " +
		"Stream endpoints provide real-time SSE at /api/{path}/stream for live data updates (server→client). " +
		"WebSocket endpoints provide bidirectional real-time communication at /api/{path}/ws (chat, collaboration). " +
		"Use 'test' with a path to verify an endpoint works and see sample data. " +
		"API endpoints support required_role for RBAC (e.g. required_role='admin'). Auth endpoints support default_role for new registrations."
}

func (t *EndpointsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"create_api", "list_api", "delete_api", "create_auth", "list_auth", "delete_auth", "create_upload", "create_stream", "create_websocket", "verify_password", "test", "create_oauth", "list_oauth", "delete_oauth"},
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "API path (e.g. 'contacts' becomes /api/contacts, 'auth' becomes /api/auth/register etc.)",
			},
			"table_name": map[string]interface{}{
				"type":        "string",
				"description": "Dynamic table to map to (must already exist)",
			},
			"methods": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string", "enum": []string{"GET", "POST", "PUT", "DELETE"}},
				"description": "Allowed HTTP methods (default: GET, POST). Only for create_api.",
			},
			"public_columns": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Columns visible in responses. PASSWORD columns are always hidden. For create_auth: columns for JWT claims and /me.",
			},
			"requires_auth": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, API requests must include a valid bearer token (default: false). Only for create_api.",
			},
			"public_read": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, GET requests are allowed without auth even when requires_auth=true. Use for public-readable data (forums, products, articles). Default: false. Only for create_api.",
			},
			"rate_limit": map[string]interface{}{
				"type":        "number",
				"description": "Max requests per minute per IP (default: 60). Only for create_api.",
			},
			"username_column": map[string]interface{}{
				"type":        "string",
				"description": "Column used as the unique username/email for login (e.g. 'email'). Required for create_auth.",
			},
			"password_column": map[string]interface{}{
				"type":        "string",
				"description": "Column with PASSWORD type (default: 'password'). For create_auth and verify_password.",
			},
			"allowed_types": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Allowed MIME types for uploads (e.g. [\"image/*\", \"application/pdf\"]). Glob patterns supported. Only for create_upload.",
			},
			"event_types": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Event types to stream (e.g. [\"data.insert\", \"data.update\", \"data.delete\"]). Only for create_stream.",
			},
			"max_size_mb": map[string]interface{}{
				"type":        "number",
				"description": "Max file size in MB (default: 5). Only for create_upload.",
			},
			"id": map[string]interface{}{
				"type":        "number",
				"description": "Row ID to check. Required for verify_password.",
			},
			"password": map[string]interface{}{
				"type":        "string",
				"description": "Plaintext password to verify. Required for verify_password.",
			},
			"required_role": map[string]interface{}{
				"type":        "string",
				"description": "Role required to access API endpoint (e.g. 'admin'). Implies requires_auth=true. Only for create_api.",
			},
			"default_role": map[string]interface{}{
				"type":        "string",
				"description": "Default role assigned to new users on registration (default: 'user'). Only for create_auth.",
			},
			"role_column": map[string]interface{}{
				"type":        "string",
				"description": "Column in user table storing the role (default: 'role'). Only for create_auth.",
			},
			"provider_name": map[string]interface{}{
				"type":        "string",
				"description": "OAuth provider identifier (e.g. 'google', 'github'). For create_oauth, delete_oauth.",
			},
			"display_name": map[string]interface{}{
				"type":        "string",
				"description": "Display name for OAuth button (e.g. 'Google'). For create_oauth.",
			},
			"client_id": map[string]interface{}{
				"type":        "string",
				"description": "OAuth client ID. For create_oauth.",
			},
			"client_secret": map[string]interface{}{
				"type":        "string",
				"description": "OAuth client secret (will be encrypted and stored). For create_oauth.",
			},
			"authorize_url": map[string]interface{}{
				"type":        "string",
				"description": "OAuth authorization endpoint URL. For create_oauth.",
			},
			"token_url": map[string]interface{}{
				"type":        "string",
				"description": "OAuth token exchange endpoint URL. For create_oauth.",
			},
			"userinfo_url": map[string]interface{}{
				"type":        "string",
				"description": "OAuth user info endpoint URL. For create_oauth.",
			},
			"scopes": map[string]interface{}{
				"type":        "string",
				"description": "Space-separated OAuth scopes (default: 'openid email profile'). For create_oauth.",
			},
			"username_field": map[string]interface{}{
				"type":        "string",
				"description": "Field from OAuth user info to use as username (default: 'email'). For create_oauth.",
			},
			"auth_path": map[string]interface{}{
				"type":        "string",
				"description": "Auth endpoint path this OAuth links to (must already exist). For create_oauth.",
			},
			"receive_event_type": map[string]interface{}{
				"type":        "string",
				"description": "Event type published when a client sends a WebSocket message (default: 'ws.message'). Only for create_websocket.",
			},
			"write_to_table": map[string]interface{}{
				"type":        "string",
				"description": "Table to auto-insert incoming WebSocket messages into (optional). Only for create_websocket.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *EndpointsTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	return DispatchAction(ctx, args, map[string]ActionHandler{
		"create_api":       t.createAPI,
		"list_api":         t.listAPI,
		"delete_api":       t.deleteAPI,
		"create_auth":      t.createAuth,
		"list_auth":        t.listAuth,
		"delete_auth":      t.deleteAuth,
		"create_upload":    t.createUpload,
		"create_stream":    t.createStream,
		"create_websocket": t.createWebSocket,
		"verify_password":  t.verifyPassword,
		"test":             t.testEndpoint,
		"create_oauth":     t.createOAuth,
		"list_oauth":       t.listOAuth,
		"delete_oauth":     t.deleteOAuth,
	}, nil)
}

// ---------------------------------------------------------------------------
// API endpoint actions
// ---------------------------------------------------------------------------

func (t *EndpointsTool) createAPI(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	tableName, _ := args["table_name"].(string)
	if path == "" || tableName == "" {
		return &Result{Success: false, Error: "path and table_name are required"}, nil
	}

	// Verify the dynamic table exists.
	var exists int
	err := ctx.DB.QueryRow(
		"SELECT COUNT(*) FROM dynamic_tables WHERE table_name = ?",
		tableName,
	).Scan(&exists)
	if err != nil || exists == 0 {
		return &Result{Success: false, Error: fmt.Sprintf("dynamic table '%s' does not exist — create it first with manage_schema", tableName)}, nil
	}

	// Parse methods.
	methods := []string{"GET", "POST"}
	if methodsRaw, ok := args["methods"].([]interface{}); ok && len(methodsRaw) > 0 {
		methods = nil
		for _, m := range methodsRaw {
			if ms, ok := m.(string); ok {
				methods = append(methods, ms)
			}
		}
	}
	methodsJSON, _ := json.Marshal(methods)

	// Parse public_columns.
	var publicColsJSON *string
	if colsRaw, ok := args["public_columns"].([]interface{}); ok && len(colsRaw) > 0 {
		var cols []string
		for _, c := range colsRaw {
			if cs, ok := c.(string); ok {
				cols = append(cols, cs)
			}
		}
		j, _ := json.Marshal(cols)
		s := string(j)
		publicColsJSON = &s
	}

	requiresAuth := false
	if ra, ok := args["requires_auth"].(bool); ok {
		requiresAuth = ra
	}

	publicRead := false
	if pr, ok := args["public_read"].(bool); ok {
		publicRead = pr
	}

	// Role-based access control: if required_role is set, implies requires_auth.
	var requiredRole *string
	if rr, ok := args["required_role"].(string); ok && rr != "" {
		requiredRole = &rr
		requiresAuth = true
	}

	rateLimit := 60
	if rl, ok := args["rate_limit"].(float64); ok && rl > 0 {
		rateLimit = int(rl)
	}

	_, err = ctx.DB.Exec(
		`INSERT INTO api_endpoints (path, table_name, methods, public_columns, requires_auth, public_read, required_role, rate_limit)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		   table_name = excluded.table_name,
		   methods = excluded.methods,
		   public_columns = excluded.public_columns,
		   requires_auth = excluded.requires_auth,
		   public_read = excluded.public_read,
		   required_role = excluded.required_role,
		   rate_limit = excluded.rate_limit`,
		path, tableName, string(methodsJSON), publicColsJSON, requiresAuth, publicRead, requiredRole, rateLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("creating API endpoint: %w", err)
	}

	data := map[string]interface{}{
		"path":          path,
		"table_name":    tableName,
		"methods":       methods,
		"requires_auth": requiresAuth,
		"public_read":   publicRead,
		"rate_limit":    rateLimit,
	}
	if requiredRole != nil {
		data["required_role"] = *requiredRole
	}
	return &Result{Success: true, Data: data}, nil
}

func (t *EndpointsTool) listAPI(ctx *ToolContext, _ map[string]interface{}) (*Result, error) {
	rows, err := ctx.DB.Query(
		"SELECT id, path, table_name, methods, public_columns, requires_auth, rate_limit, created_at FROM api_endpoints ORDER BY path",
	)
	if err != nil {
		return nil, fmt.Errorf("listing API endpoints: %w", err)
	}
	defer rows.Close()

	var endpoints []map[string]interface{}
	for rows.Next() {
		var id, rateLimit int
		var path, tableName, methods string
		var publicCols sql.NullString
		var requiresAuth bool
		var createdAt time.Time
		if err := rows.Scan(&id, &path, &tableName, &methods, &publicCols, &requiresAuth, &rateLimit, &createdAt); err != nil {
			continue
		}
		endpoints = append(endpoints, map[string]interface{}{
			"id":             id,
			"path":           path,
			"table_name":     tableName,
			"methods":        methods,
			"public_columns": publicCols.String,
			"requires_auth":  requiresAuth,
			"rate_limit":     rateLimit,
			"created_at":     createdAt,
		})
	}

	return &Result{Success: true, Data: endpoints}, nil
}

func (t *EndpointsTool) deleteAPI(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return &Result{Success: false, Error: "path is required"}, nil
	}

	res, err := ctx.DB.Exec(
		"DELETE FROM api_endpoints WHERE path = ?",
		path,
	)
	if err != nil {
		return nil, fmt.Errorf("deleting API endpoint: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return &Result{Success: false, Error: "endpoint not found"}, nil
	}

	LogDestructiveAction(ctx, "manage_endpoints", "delete_api", path)

	return &Result{Success: true, Data: map[string]interface{}{"deleted": path}}, nil
}

// ---------------------------------------------------------------------------
// Auth endpoint actions
// ---------------------------------------------------------------------------

func (t *EndpointsTool) createAuth(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	tableName, _ := args["table_name"].(string)
	path, _ := args["path"].(string)
	usernameCol, _ := args["username_column"].(string)
	passwordCol, _ := args["password_column"].(string)
	if passwordCol == "" {
		passwordCol = "password"
	}

	if tableName == "" || path == "" || usernameCol == "" {
		return &Result{Success: false, Error: "table_name, path, and username_column are required"}, nil
	}

	// Validate column names to prevent SQL injection.
	if !validColumnName.MatchString(usernameCol) {
		return &Result{Success: false, Error: fmt.Sprintf("invalid username_column name: %s", usernameCol)}, nil
	}
	if !validColumnName.MatchString(passwordCol) {
		return &Result{Success: false, Error: fmt.Sprintf("invalid password_column name: %s", passwordCol)}, nil
	}

	// Verify the table exists and has a PASSWORD column.
	secureCols, err := loadSecureColumns(ctx, tableName)
	if err != nil || secureCols == nil {
		return &Result{Success: false, Error: fmt.Sprintf("table %q not found in dynamic tables registry", tableName)}, nil
	}
	if kind, ok := secureCols[passwordCol]; !ok || kind != "hash" {
		return &Result{Success: false, Error: fmt.Sprintf("column %q in table %q is not a PASSWORD column", passwordCol, tableName)}, nil
	}

	// Build public columns JSON.
	publicColumnsJSON := "[]"
	if pubColsRaw, ok := args["public_columns"].([]interface{}); ok && len(pubColsRaw) > 0 {
		var pubCols []string
		for _, c := range pubColsRaw {
			if s, ok := c.(string); ok {
				if !validColumnName.MatchString(s) {
					return &Result{Success: false, Error: fmt.Sprintf("invalid public_column name: %s", s)}, nil
				}
				pubCols = append(pubCols, s)
			}
		}
		data, _ := json.Marshal(pubCols)
		publicColumnsJSON = string(data)
	}

	// RBAC: default role and role column.
	defaultRole := "user"
	if dr, ok := args["default_role"].(string); ok && dr != "" {
		defaultRole = dr
	}
	roleColumn := "role"
	if rc, ok := args["role_column"].(string); ok && rc != "" {
		if !validColumnName.MatchString(rc) {
			return &Result{Success: false, Error: fmt.Sprintf("invalid role_column name: %s", rc)}, nil
		}
		roleColumn = rc
	}

	// Insert into auth_endpoints table.
	result, err := ctx.DB.Exec(
		`INSERT INTO auth_endpoints (table_name, path, username_column, password_column, public_columns, default_role, role_column)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		   table_name = excluded.table_name,
		   username_column = excluded.username_column,
		   password_column = excluded.password_column,
		   public_columns = excluded.public_columns,
		   default_role = excluded.default_role,
		   role_column = excluded.role_column`,
		tableName, path, usernameCol, passwordCol, publicColumnsJSON, defaultRole, roleColumn,
	)
	if err != nil {
		return nil, fmt.Errorf("creating auth endpoint: %w", err)
	}

	id, _ := result.LastInsertId()
	return &Result{Success: true, Data: map[string]interface{}{
		"id":   id,
		"path": path,
		"routes": []string{
			fmt.Sprintf("POST /api/%s/register", path),
			fmt.Sprintf("POST /api/%s/login", path),
			fmt.Sprintf("GET /api/%s/me", path),
		},
		"table":           tableName,
		"username_column": usernameCol,
		"password_column": passwordCol,
		"default_role":    defaultRole,
		"role_column":     roleColumn,
	}}, nil
}

func (t *EndpointsTool) listAuth(ctx *ToolContext, _ map[string]interface{}) (*Result, error) {
	rows, err := ctx.DB.Query(
		"SELECT id, path, table_name, username_column, password_column, public_columns, created_at FROM auth_endpoints ORDER BY path",
	)
	if err != nil {
		return nil, fmt.Errorf("listing auth endpoints: %w", err)
	}
	defer rows.Close()

	var endpoints []map[string]interface{}
	for rows.Next() {
		var id int
		var path, tableName, usernameCol, passwordCol, publicCols string
		var createdAt interface{}
		if err := rows.Scan(&id, &path, &tableName, &usernameCol, &passwordCol, &publicCols, &createdAt); err != nil {
			continue
		}
		endpoints = append(endpoints, map[string]interface{}{
			"id":              id,
			"path":            path,
			"table_name":      tableName,
			"username_column": usernameCol,
			"password_column": passwordCol,
			"public_columns":  publicCols,
			"created_at":      createdAt,
			"routes": []string{
				fmt.Sprintf("POST /api/%s/register", path),
				fmt.Sprintf("POST /api/%s/login", path),
				fmt.Sprintf("GET /api/%s/me", path),
			},
		})
	}

	return &Result{Success: true, Data: endpoints}, nil
}

func (t *EndpointsTool) deleteAuth(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return &Result{Success: false, Error: "path is required"}, nil
	}

	res, err := ctx.DB.Exec(
		"DELETE FROM auth_endpoints WHERE path = ?",
		path,
	)
	if err != nil {
		return nil, fmt.Errorf("deleting auth endpoint: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return &Result{Success: false, Error: "auth endpoint not found"}, nil
	}

	LogDestructiveAction(ctx, "manage_endpoints", "delete_auth", path)

	return &Result{Success: true, Data: map[string]interface{}{"deleted": path}}, nil
}

func (t *EndpointsTool) verifyPassword(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	tableName, _ := args["table_name"].(string)
	idFloat, _ := args["id"].(float64)
	password, _ := args["password"].(string)
	passwordCol, _ := args["password_column"].(string)
	if passwordCol == "" {
		passwordCol = "password"
	}

	if tableName == "" || password == "" {
		return &Result{Success: false, Error: "table_name, id, and password are required"}, nil
	}

	physicalName, err := sanitizedTableName(tableName)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	// Validate column name to prevent SQL injection.
	if !validColumnName.MatchString(passwordCol) {
		return &Result{Success: false, Error: fmt.Sprintf("invalid column name: %s", passwordCol)}, nil
	}

	// Fetch the hashed password from the row.
	var hashedPassword string
	err = ctx.DB.QueryRow(
		fmt.Sprintf("SELECT %s FROM %s WHERE id = ?", passwordCol, physicalName),
		int64(idFloat),
	).Scan(&hashedPassword)
	if err != nil {
		return &Result{Success: false, Error: "row not found"}, nil
	}

	// Use the security package to check the password.
	match := checkPasswordHash(password, hashedPassword)

	return &Result{Success: true, Data: map[string]interface{}{
		"match": match,
	}}, nil
}

// checkPasswordHash compares a plaintext password against a bcrypt hash.
func checkPasswordHash(password, hash string) bool {
	return security.CheckPassword(password, hash)
}

// ---------------------------------------------------------------------------
// Upload endpoint action
// ---------------------------------------------------------------------------

func (t *EndpointsTool) createUpload(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return &Result{Success: false, Error: "path is required"}, nil
	}

	// Parse allowed_types (default: image/*, application/pdf).
	allowedTypes := []string{"image/*", "application/pdf"}
	if typesRaw, ok := args["allowed_types"].([]interface{}); ok && len(typesRaw) > 0 {
		allowedTypes = nil
		for _, t := range typesRaw {
			if ts, ok := t.(string); ok {
				allowedTypes = append(allowedTypes, ts)
			}
		}
	}
	allowedTypesJSON, _ := json.Marshal(allowedTypes)

	maxSizeMB := 5
	if ms, ok := args["max_size_mb"].(float64); ok && ms > 0 {
		maxSizeMB = int(ms)
	}

	requiresAuth := false
	if ra, ok := args["requires_auth"].(bool); ok {
		requiresAuth = ra
	}

	// Optional: link to a table to store file metadata.
	tableName, _ := args["table_name"].(string)

	_, err := ctx.DB.Exec(
		`INSERT INTO upload_endpoints (path, allowed_types, max_size_mb, requires_auth, table_name)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		   allowed_types = excluded.allowed_types,
		   max_size_mb = excluded.max_size_mb,
		   requires_auth = excluded.requires_auth,
		   table_name = excluded.table_name`,
		path, string(allowedTypesJSON), maxSizeMB, requiresAuth, tableName,
	)
	if err != nil {
		return nil, fmt.Errorf("creating upload endpoint: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"path":          path,
		"route":         fmt.Sprintf("POST /api/%s/upload", path),
		"allowed_types": allowedTypes,
		"max_size_mb":   maxSizeMB,
		"requires_auth": requiresAuth,
		"table_name":    tableName,
	}}, nil
}

// ---------------------------------------------------------------------------
// Stream endpoint action
// ---------------------------------------------------------------------------

func (t *EndpointsTool) createStream(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return &Result{Success: false, Error: "path is required"}, nil
	}

	// Parse event_types (default: all data events).
	eventTypes := []string{"data.insert", "data.update", "data.delete"}
	if typesRaw, ok := args["event_types"].([]interface{}); ok && len(typesRaw) > 0 {
		eventTypes = nil
		for _, et := range typesRaw {
			if ets, ok := et.(string); ok {
				eventTypes = append(eventTypes, ets)
			}
		}
	}
	eventTypesJSON, _ := json.Marshal(eventTypes)

	requiresAuth := false
	if ra, ok := args["requires_auth"].(bool); ok {
		requiresAuth = ra
	}

	_, err := ctx.DB.Exec(
		`INSERT INTO stream_endpoints (path, event_types, requires_auth)
		 VALUES (?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		   event_types = excluded.event_types,
		   requires_auth = excluded.requires_auth`,
		path, string(eventTypesJSON), requiresAuth,
	)
	if err != nil {
		return nil, fmt.Errorf("creating stream endpoint: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"path":          path,
		"route":         fmt.Sprintf("GET /api/%s/stream", path),
		"event_types":   eventTypes,
		"requires_auth": requiresAuth,
		"usage":         "const source = new EventSource('/api/" + path + "/stream'); source.addEventListener('data.insert', (e) => { const data = JSON.parse(e.data); ... });",
	}}, nil
}

// ---------------------------------------------------------------------------
// WebSocket endpoint action
// ---------------------------------------------------------------------------

func (t *EndpointsTool) createWebSocket(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return &Result{Success: false, Error: "path is required"}, nil
	}

	// Parse event_types (default: all data events).
	eventTypes := []string{"data.insert", "data.update", "data.delete"}
	if typesRaw, ok := args["event_types"].([]interface{}); ok && len(typesRaw) > 0 {
		eventTypes = nil
		for _, et := range typesRaw {
			if ets, ok := et.(string); ok {
				eventTypes = append(eventTypes, ets)
			}
		}
	}
	eventTypesJSON, _ := json.Marshal(eventTypes)

	receiveEventType := "ws.message"
	if ret, ok := args["receive_event_type"].(string); ok && ret != "" {
		receiveEventType = ret
	}

	writeToTable := ""
	if wtt, ok := args["write_to_table"].(string); ok && wtt != "" {
		if !validColumnName.MatchString(wtt) {
			return &Result{Success: false, Error: fmt.Sprintf("invalid write_to_table name: %s", wtt)}, nil
		}
		writeToTable = wtt
	}

	requiresAuth := false
	if ra, ok := args["requires_auth"].(bool); ok {
		requiresAuth = ra
	}

	_, err := ctx.DB.Exec(
		`INSERT INTO ws_endpoints (path, event_types, receive_event_type, write_to_table, requires_auth)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		   event_types = excluded.event_types,
		   receive_event_type = excluded.receive_event_type,
		   write_to_table = excluded.write_to_table,
		   requires_auth = excluded.requires_auth`,
		path, string(eventTypesJSON), receiveEventType, writeToTable, requiresAuth,
	)
	if err != nil {
		return nil, fmt.Errorf("creating websocket endpoint: %w", err)
	}

	result := map[string]interface{}{
		"path":               path,
		"route":              fmt.Sprintf("GET /api/%s/ws", path),
		"event_types":        eventTypes,
		"receive_event_type": receiveEventType,
		"requires_auth":      requiresAuth,
		"usage":              "const ws = new WebSocket((location.protocol==='https:'?'wss:':'ws:') + '//' + location.host + '/api/" + path + "/ws'); ws.onmessage = (e) => { const data = JSON.parse(e.data); ... }; ws.send(JSON.stringify({content: 'hello'}));",
	}
	if writeToTable != "" {
		result["write_to_table"] = writeToTable
	}
	return &Result{Success: true, Data: result}, nil
}

// ---------------------------------------------------------------------------
// Test action
// ---------------------------------------------------------------------------

func (t *EndpointsTool) testEndpoint(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return &Result{Success: false, Error: "path is required"}, nil
	}

	url := resolveLocalURL(ctx, "/api/"+path+"?limit=1")

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("endpoint unreachable: %v", err)}, nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode != http.StatusOK {
		return &Result{Success: false, Data: map[string]interface{}{
			"status_code": resp.StatusCode,
			"body":        string(body),
		}, Error: fmt.Sprintf("endpoint returned status %d", resp.StatusCode)}, nil
	}

	// Try to parse as JSON to extract column names.
	var parsed interface{}
	var columns []string
	if json.Unmarshal(body, &parsed) == nil {
		// Extract column names from first record if it's an array of objects.
		if arr, ok := parsed.([]interface{}); ok && len(arr) > 0 {
			if obj, ok := arr[0].(map[string]interface{}); ok {
				for k := range obj {
					columns = append(columns, k)
				}
			}
		}
	}

	result := map[string]interface{}{
		"status_code": resp.StatusCode,
		"data_sample": string(body),
	}
	if len(columns) > 0 {
		result["columns"] = columns
	}

	return &Result{Success: true, Data: result}, nil
}

// ---------------------------------------------------------------------------
// OAuth endpoint actions
// ---------------------------------------------------------------------------

func (t *EndpointsTool) createOAuth(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	providerName, _ := args["provider_name"].(string)
	displayName, _ := args["display_name"].(string)
	clientID, _ := args["client_id"].(string)
	clientSecret, _ := args["client_secret"].(string)
	authorizeURL, _ := args["authorize_url"].(string)
	tokenURL, _ := args["token_url"].(string)
	userinfoURL, _ := args["userinfo_url"].(string)
	authPath, _ := args["auth_path"].(string)

	if providerName == "" || clientID == "" || clientSecret == "" || authorizeURL == "" || tokenURL == "" || userinfoURL == "" || authPath == "" {
		return &Result{Success: false, Error: "provider_name, client_id, client_secret, authorize_url, token_url, userinfo_url, and auth_path are required"}, nil
	}
	if displayName == "" {
		displayName = providerName
	}

	scopes := "openid email profile"
	if s, ok := args["scopes"].(string); ok && s != "" {
		scopes = s
	}
	usernameField := "email"
	if uf, ok := args["username_field"].(string); ok && uf != "" {
		usernameField = uf
	}

	// Verify the auth endpoint exists.
	var authExists int
	ctx.DB.QueryRow("SELECT COUNT(*) FROM auth_endpoints WHERE path = ?", authPath).Scan(&authExists)
	if authExists == 0 {
		return &Result{Success: false, Error: fmt.Sprintf("auth endpoint '%s' does not exist — create it first with create_auth", authPath)}, nil
	}

	// Encrypt and store the client secret.
	secretName := "oauth_" + providerName + "_secret"
	if ctx.Encryptor == nil {
		return &Result{Success: false, Error: "encryption not available"}, nil
	}
	encrypted, err := ctx.Encryptor.Encrypt(clientSecret)
	if err != nil {
		return &Result{Success: false, Error: "failed to encrypt client secret"}, nil
	}
	ctx.DB.Exec(
		`INSERT INTO secrets (name, value_encrypted) VALUES (?, ?) ON CONFLICT(name) DO UPDATE SET value_encrypted = excluded.value_encrypted, updated_at = CURRENT_TIMESTAMP`,
		secretName, encrypted,
	)

	// Insert OAuth provider.
	_, err = ctx.DB.Exec(
		`INSERT INTO oauth_providers (name, display_name, client_id, client_secret_name, authorize_url, token_url, userinfo_url, scopes, username_field, auth_endpoint_path)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   display_name = excluded.display_name,
		   client_id = excluded.client_id,
		   client_secret_name = excluded.client_secret_name,
		   authorize_url = excluded.authorize_url,
		   token_url = excluded.token_url,
		   userinfo_url = excluded.userinfo_url,
		   scopes = excluded.scopes,
		   username_field = excluded.username_field,
		   auth_endpoint_path = excluded.auth_endpoint_path`,
		providerName, displayName, clientID, secretName, authorizeURL, tokenURL, userinfoURL, scopes, usernameField, authPath,
	)
	if err != nil {
		return nil, fmt.Errorf("creating OAuth provider: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"provider": providerName,
		"routes": []string{
			fmt.Sprintf("GET /api/%s/oauth/%s (redirect to provider)", authPath, providerName),
			fmt.Sprintf("GET /api/%s/oauth/%s/callback (automatic)", authPath, providerName),
		},
		"display_name": displayName,
		"auth_path":    authPath,
	}}, nil
}

func (t *EndpointsTool) listOAuth(ctx *ToolContext, _ map[string]interface{}) (*Result, error) {
	rows, err := ctx.DB.Query(
		"SELECT id, name, display_name, client_id, authorize_url, token_url, userinfo_url, scopes, username_field, auth_endpoint_path, is_enabled, created_at FROM oauth_providers ORDER BY name",
	)
	if err != nil {
		return nil, fmt.Errorf("listing OAuth providers: %w", err)
	}
	defer rows.Close()

	var providers []map[string]interface{}
	for rows.Next() {
		var id int
		var name, displayName, clientID, authorizeURL, tokenURL, userinfoURL, scopes, usernameField, authPath string
		var isEnabled bool
		var createdAt string
		if err := rows.Scan(&id, &name, &displayName, &clientID, &authorizeURL, &tokenURL, &userinfoURL, &scopes, &usernameField, &authPath, &isEnabled, &createdAt); err != nil {
			continue
		}
		providers = append(providers, map[string]interface{}{
			"id":            id,
			"name":          name,
			"display_name":  displayName,
			"client_id":     clientID,
			"authorize_url": authorizeURL,
			"scopes":        scopes,
			"auth_path":     authPath,
			"is_enabled":    isEnabled,
			"created_at":    createdAt,
		})
	}
	return &Result{Success: true, Data: providers}, nil
}

func (t *EndpointsTool) deleteOAuth(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	providerName, _ := args["provider_name"].(string)
	if providerName == "" {
		return &Result{Success: false, Error: "provider_name is required"}, nil
	}

	// Clean up the associated secret.
	secretName := "oauth_" + providerName + "_secret"
	ctx.DB.Exec("DELETE FROM secrets WHERE name = ?", secretName)

	result, err := ctx.DB.Exec("DELETE FROM oauth_providers WHERE name = ?", providerName)
	if err != nil {
		return nil, fmt.Errorf("deleting OAuth provider: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return &Result{Success: false, Error: fmt.Sprintf("OAuth provider '%s' not found", providerName)}, nil
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"deleted": providerName,
	}}, nil
}

func (t *EndpointsTool) MaxResultSize() int { return 8000 }

func (t *EndpointsTool) Summarize(result string) string {
	r, dataMap, dataArr, ok := parseSummaryResult(result)
	if !ok {
		return summarizeTruncate(result, 200)
	}
	if !r.Success {
		return summarizeError(r.Error)
	}
	if dataArr != nil {
		return fmt.Sprintf(`{"success":true,"summary":"Listed %d endpoints"}`, len(dataArr))
	}
	// For create/test operations, include key fields.
	if path, _ := dataMap["path"].(string); path != "" {
		return fmt.Sprintf(`{"success":true,"summary":"Endpoint at /api/%s"}`, path)
	}
	return summarizeTruncate(result, 300)
}
