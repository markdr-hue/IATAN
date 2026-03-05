/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/markdr-hue/IATAN/security"
)

// ---------------------------------------------------------------------------
// EndpointsTool — unified manage_endpoints tool
// ---------------------------------------------------------------------------

// EndpointsTool consolidates API endpoint and auth endpoint management into a
// single tool with seven actions: create_api, list_api, delete_api,
// create_auth, list_auth, delete_auth, verify_password.
type EndpointsTool struct{}

func (t *EndpointsTool) Name() string { return "manage_endpoints" }
func (t *EndpointsTool) Description() string {
	return "Manage API and auth endpoints. Actions: create_api, list_api, delete_api, create_auth, list_auth, delete_auth, verify_password.\n" +
		"Response formats for API endpoints: GET /api/{path} → {\"data\":[...],\"count\":N,\"limit\":N,\"offset\":N}. " +
		"GET /api/{path}/{id} → bare object. POST → {\"success\":true,\"id\":N}. PUT/DELETE → {\"success\":true}.\n" +
		"Filtering: GET /api/{path}?column=value&sort=column&order=asc|desc. Multiple filters use AND.\n" +
		"JS example: fetch('/api/items?category=shoes&sort=price&order=asc').then(r=>r.json()).then(res => res.data.forEach(...))\n" +
		"Auth endpoints create /api/{path}/register, /api/{path}/login, /api/{path}/me routes using JWT."
}

func (t *EndpointsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"create_api", "list_api", "delete_api", "create_auth", "list_auth", "delete_auth", "verify_password"},
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
			"id": map[string]interface{}{
				"type":        "number",
				"description": "Row ID to check. Required for verify_password.",
			},
			"password": map[string]interface{}{
				"type":        "string",
				"description": "Plaintext password to verify. Required for verify_password.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *EndpointsTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	action, errResult := RequireAction(args)
	if errResult != nil {
		// Infer action from provided args — LLMs sometimes omit the action field.
		if _, hasPassword := args["password_column"]; hasPassword {
			action = "create_auth"
		} else if _, hasTable := args["table_name"]; hasTable {
			action = "create_api"
		} else {
			action = "list_api"
		}
		args["action"] = action
	}
	switch action {
	case "create_api":
		return t.createAPI(ctx, args)
	case "list_api":
		return t.listAPI(ctx, args)
	case "delete_api":
		return t.deleteAPI(ctx, args)
	case "create_auth":
		return t.createAuth(ctx, args)
	case "list_auth":
		return t.listAuth(ctx, args)
	case "delete_auth":
		return t.deleteAuth(ctx, args)
	case "verify_password":
		return t.verifyPassword(ctx, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown action: %q (use create_api, list_api, delete_api, create_auth, list_auth, delete_auth, verify_password)", action)}, nil
	}
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

	rateLimit := 60
	if rl, ok := args["rate_limit"].(float64); ok && rl > 0 {
		rateLimit = int(rl)
	}

	_, err = ctx.DB.Exec(
		`INSERT INTO api_endpoints (path, table_name, methods, public_columns, requires_auth, rate_limit)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		   table_name = excluded.table_name,
		   methods = excluded.methods,
		   public_columns = excluded.public_columns,
		   requires_auth = excluded.requires_auth,
		   rate_limit = excluded.rate_limit`,
		path, tableName, string(methodsJSON), publicColsJSON, requiresAuth, rateLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("creating API endpoint: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"path":          path,
		"table_name":    tableName,
		"methods":       methods,
		"requires_auth": requiresAuth,
		"rate_limit":    rateLimit,
	}}, nil
}

func (t *EndpointsTool) listAPI(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
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

	// Insert into auth_endpoints table.
	result, err := ctx.DB.Exec(
		`INSERT INTO auth_endpoints (table_name, path, username_column, password_column, public_columns)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		   table_name = excluded.table_name,
		   username_column = excluded.username_column,
		   password_column = excluded.password_column,
		   public_columns = excluded.public_columns`,
		tableName, path, usernameCol, passwordCol, publicColumnsJSON,
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
	}}, nil
}

func (t *EndpointsTool) listAuth(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
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
