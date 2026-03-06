/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/markdr-hue/IATAN/security"
)

// validTableName matches only alphanumeric and underscore characters.
var validTableName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

// validColumnName matches only alphanumeric and underscore characters.
var validColumnName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

// allowedColumnTypes are the SQLite types we permit in dynamic tables.
// PASSWORD and ENCRYPTED are stored as TEXT but handled specially on insert/query.
var allowedColumnTypes = map[string]string{
	"TEXT":      "TEXT",
	"INTEGER":   "INTEGER",
	"REAL":      "REAL",
	"BLOB":      "BLOB",
	"BOOLEAN":   "BOOLEAN",
	"PASSWORD":  "TEXT", // stored as bcrypt hash
	"ENCRYPTED": "TEXT", // stored as AES-encrypted base64
}

// secureColumnKinds maps logical types to their security handling.
var secureColumnKinds = map[string]string{
	"PASSWORD":  "hash",
	"ENCRYPTED": "encrypt",
}

// sanitizedTableName validates and returns the physical table name.
func sanitizedTableName(name string) (string, error) {
	if !validTableName.MatchString(name) {
		return "", fmt.Errorf("invalid table name: %s (must be alphanumeric/underscore, start with letter)", name)
	}
	return name, nil
}

// loadSecureColumns loads the secure_columns JSON map for a dynamic table.
func loadSecureColumns(ctx *ToolContext, tableName string) (map[string]string, error) {
	return LoadSecureColumns(ctx.DB, tableName)
}

// LoadSecureColumns reads the secure column types (hash/encrypt) for a dynamic table.
// Exported so admin handlers can share this logic without duplication.
func LoadSecureColumns(db *sql.DB, tableName string) (map[string]string, error) {
	var raw string
	err := db.QueryRow(
		"SELECT secure_columns FROM dynamic_tables WHERE table_name = ?",
		tableName,
	).Scan(&raw)
	if err != nil {
		return nil, nil // table not in registry or no secure columns
	}
	var cols map[string]string
	if err := json.Unmarshal([]byte(raw), &cols); err != nil {
		return nil, nil
	}
	return cols, nil
}

// processSecureValue delegates to the shared security.ProcessSecureValue.
func processSecureValue(kind string, value interface{}, enc *security.Encryptor) (interface{}, error) {
	return security.ProcessSecureValue(kind, value, enc)
}

// allowedOps are the SQL comparison operators permitted in structured filters.
var allowedOps = map[string]bool{
	"=": true, "!=": true, "<": true, ">": true, "<=": true, ">=": true,
	"LIKE": true, "NOT LIKE": true, "IS NULL": true, "IS NOT NULL": true,
}

// buildWhereClause builds a parameterized WHERE clause from structured filters.
// Each filter must have "column" and "op"; "value" is required for most ops.
func buildWhereClause(filters []interface{}) (string, []interface{}, error) {
	if len(filters) == 0 {
		return "", nil, nil
	}

	var clauses []string
	var params []interface{}

	for _, f := range filters {
		fm, ok := f.(map[string]interface{})
		if !ok {
			return "", nil, fmt.Errorf("each filter must be an object with column, op, and value")
		}
		col, _ := fm["column"].(string)
		op, _ := fm["op"].(string)

		if !validColumnName.MatchString(col) {
			return "", nil, fmt.Errorf("invalid column name in filter: %s", col)
		}
		op = strings.ToUpper(strings.TrimSpace(op))
		if !allowedOps[op] {
			return "", nil, fmt.Errorf("unsupported operator: %s", op)
		}

		if op == "IS NULL" || op == "IS NOT NULL" {
			clauses = append(clauses, fmt.Sprintf("%s %s", col, op))
		} else {
			val, hasVal := fm["value"]
			if !hasVal {
				return "", nil, fmt.Errorf("filter on %s requires a value", col)
			}
			clauses = append(clauses, fmt.Sprintf("%s %s ?", col, op))
			params = append(params, val)
		}
	}

	return " WHERE " + strings.Join(clauses, " AND "), params, nil
}

// ---------------------------------------------------------------------------
// SchemaTool — manage_schema
// ---------------------------------------------------------------------------

// SchemaTool consolidates create, alter, describe, list, and drop table operations.
type SchemaTool struct{}

func (t *SchemaTool) Name() string { return "manage_schema" }
func (t *SchemaTool) Description() string {
	return "Manage dynamic table schemas. Actions: create (create table), alter (add/drop columns), describe (show columns), list (list tables), drop (delete table and related endpoints). Note: 'id' and 'created_at' columns are auto-added — do NOT include them in your column definitions."
}
func (t *SchemaTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"create", "alter", "describe", "list", "drop"},
				"description": "Schema operation to perform",
			},
			"table_name": map[string]interface{}{
				"type":        "string",
				"description": "Logical table name (alphanumeric and underscores only)",
			},
			"columns": map[string]interface{}{
				"type":        "object",
				"description": "Column definitions as {name: type} for create. Types: TEXT, INTEGER, REAL, BOOLEAN, PASSWORD (auto-hashed), ENCRYPTED (auto-encrypted)",
			},
			"add_columns": map[string]interface{}{
				"type":        "object",
				"description": "Columns to add for alter: {name: type}",
			},
			"drop_columns": map[string]interface{}{
				"type":        "array",
				"description": "Column names to drop for alter",
				"items":       map[string]interface{}{"type": "string"},
			},
		},
		"required": []string{"action"},
	}
}

func (t *SchemaTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	action, errResult := RequireAction(args)
	if errResult != nil {
		// Infer action from provided args — LLMs sometimes omit the action field.
		if _, hasCols := args["columns"]; hasCols {
			action = "create"
		} else if _, hasTable := args["table_name"]; hasTable {
			action = "describe"
		} else {
			action = "list"
		}
		args["action"] = action
	}
	switch action {
	case "create":
		return t.create(ctx, args)
	case "alter":
		return t.alter(ctx, args)
	case "describe":
		return t.describe(ctx, args)
	case "list":
		return t.list(ctx, args)
	case "drop":
		return t.drop(ctx, args)
	default:
		return &Result{Success: false, Error: "invalid action: must be create, alter, describe, list, or drop"}, nil
	}
}

func (t *SchemaTool) create(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	tableName, _ := args["table_name"].(string)
	columnsRaw, _ := args["columns"].(map[string]interface{})
	if tableName == "" || len(columnsRaw) == 0 {
		return &Result{Success: false, Error: "table_name and columns are required"}, nil
	}

	physicalName, err := sanitizedTableName(tableName)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	// Validate and build column definitions, track secure columns.
	var colDefs []string
	secureCols := map[string]string{}

	for colName, colTypeRaw := range columnsRaw {
		if !validColumnName.MatchString(colName) {
			return &Result{Success: false, Error: fmt.Sprintf("invalid column name: %s", colName)}, nil
		}
		colType, ok := colTypeRaw.(string)
		if !ok {
			return &Result{Success: false, Error: fmt.Sprintf("column type must be a string for %s", colName)}, nil
		}
		colType = strings.ToUpper(colType)
		sqlType, allowed := allowedColumnTypes[colType]
		if !allowed {
			return &Result{Success: false, Error: fmt.Sprintf("unsupported column type: %s (allowed: TEXT, INTEGER, REAL, BOOLEAN, PASSWORD, ENCRYPTED)", colType)}, nil
		}
		colDefs = append(colDefs, fmt.Sprintf("%s %s", colName, sqlType))

		// Track secure columns.
		if kind, isSecure := secureColumnKinds[colType]; isSecure {
			secureCols[colName] = kind
		}
	}

	createSQL := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, %s, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)",
		physicalName, strings.Join(colDefs, ", "),
	)

	if _, err := ctx.DB.Exec(createSQL); err != nil {
		return nil, fmt.Errorf("creating table: %w", err)
	}

	// Record the table in dynamic_tables registry with secure column info.
	schemaDef, err := json.Marshal(columnsRaw)
	if err != nil {
		return nil, fmt.Errorf("marshaling schema: %w", err)
	}
	secureColsJSON, err := json.Marshal(secureCols)
	if err != nil {
		return nil, fmt.Errorf("marshaling secure columns: %w", err)
	}
	_, err = ctx.DB.Exec(
		`INSERT INTO dynamic_tables (table_name, schema_def, secure_columns)
		 VALUES (?, ?, ?)
		 ON CONFLICT(table_name) DO UPDATE SET schema_def = excluded.schema_def, secure_columns = excluded.secure_columns`,
		tableName, string(schemaDef), string(secureColsJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("recording dynamic table: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"table":          tableName,
		"columns":        columnsRaw,
		"secure_columns": secureCols,
	}}, nil
}

func (t *SchemaTool) alter(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	tableName, _ := args["table_name"].(string)
	if tableName == "" {
		return &Result{Success: false, Error: "table_name is required"}, nil
	}

	physicalName, err := sanitizedTableName(tableName)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	// Load existing secure columns.
	secureCols, _ := loadSecureColumns(ctx, tableName)
	if secureCols == nil {
		secureCols = map[string]string{}
	}

	var addedCols []string
	var droppedCols []string

	// Add columns.
	if addColumnsRaw, ok := args["add_columns"].(map[string]interface{}); ok {
		for colName, colTypeRaw := range addColumnsRaw {
			if !validColumnName.MatchString(colName) {
				return &Result{Success: false, Error: fmt.Sprintf("invalid column name: %s", colName)}, nil
			}
			colType, ok := colTypeRaw.(string)
			if !ok {
				return &Result{Success: false, Error: fmt.Sprintf("column type must be a string for %s", colName)}, nil
			}
			colType = strings.ToUpper(colType)
			sqlType, allowed := allowedColumnTypes[colType]
			if !allowed {
				return &Result{Success: false, Error: fmt.Sprintf("unsupported column type: %s", colType)}, nil
			}

			alterSQL := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", physicalName, colName, sqlType)
			if _, err := ctx.DB.Exec(alterSQL); err != nil {
				return nil, fmt.Errorf("adding column %s: %w", colName, err)
			}

			// Track secure columns.
			if kind, isSecure := secureColumnKinds[colType]; isSecure {
				secureCols[colName] = kind
			}

			addedCols = append(addedCols, colName)
		}
	}

	// Drop columns (SQLite supports ALTER TABLE DROP COLUMN since 3.35.0).
	if dropColumnsRaw, ok := args["drop_columns"].([]interface{}); ok {
		protectedCols := map[string]bool{"id": true, "created_at": true}

		// Collect valid column names first for the approval check.
		var colsToDrop []string
		for _, colRaw := range dropColumnsRaw {
			colName, ok := colRaw.(string)
			if !ok || colName == "" {
				continue
			}
			if protectedCols[colName] {
				return &Result{Success: false, Error: fmt.Sprintf("cannot drop protected column: %s", colName)}, nil
			}
			colsToDrop = append(colsToDrop, colName)
		}

		// Approval gate for dropping columns (data loss).
		if len(colsToDrop) > 0 {
			approvalKey := fmt.Sprintf("drop_columns:%s:%s", tableName, strings.Join(colsToDrop, ","))
			if !CheckApproval(ctx.DB, approvalKey) {
				question := fmt.Sprintf(
					"The AI wants to drop column(s) %s from table '%s'. Data in these columns will be permanently lost. Approve?",
					strings.Join(colsToDrop, ", "), tableName,
				)
				return RequestApproval(ctx, approvalKey, question,
					fmt.Sprintf("Dropping columns [%s] from table '%s'.", strings.Join(colsToDrop, ", "), tableName),
				), nil
			}
		}

		for _, colName := range colsToDrop {
			alterSQL := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", physicalName, colName)
			if _, err := ctx.DB.Exec(alterSQL); err != nil {
				return nil, fmt.Errorf("dropping column %s: %w", colName, err)
			}

			// Remove from secure columns if present.
			delete(secureCols, colName)
			droppedCols = append(droppedCols, colName)
		}

		if len(droppedCols) > 0 {
			LogDestructiveAction(ctx, "manage_schema", "alter_drop_columns",
				fmt.Sprintf("%s:[%s]", tableName, strings.Join(droppedCols, ",")))
		}
	}

	if len(addedCols) == 0 && len(droppedCols) == 0 {
		return &Result{Success: false, Error: "no columns to add or drop"}, nil
	}

	// Update secure_columns in the registry.
	secureColsJSON, err := json.Marshal(secureCols)
	if err != nil {
		ctx.Logger.Error("failed to marshal secure columns", "error", err)
	} else if _, err := ctx.DB.Exec(
		"UPDATE dynamic_tables SET secure_columns = ? WHERE table_name = ?",
		string(secureColsJSON), tableName,
	); err != nil {
		ctx.Logger.Error("failed to update secure columns in registry", "error", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"table":   tableName,
		"added":   addedCols,
		"dropped": droppedCols,
	}}, nil
}

func (t *SchemaTool) describe(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	tableName, _ := args["table_name"].(string)
	if tableName == "" {
		return &Result{Success: false, Error: "table_name is required"}, nil
	}

	physicalName, err := sanitizedTableName(tableName)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	// Get column info via PRAGMA.
	rows, err := ctx.DB.Query(fmt.Sprintf("PRAGMA table_info(%s)", physicalName))
	if err != nil {
		return nil, fmt.Errorf("describing table: %w", err)
	}
	defer rows.Close()

	secureCols, _ := loadSecureColumns(ctx, tableName)

	var columns []map[string]interface{}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue interface{}
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			continue
		}

		col := map[string]interface{}{
			"name": name,
			"type": colType,
		}
		if pk == 1 {
			col["primary_key"] = true
		}
		if secureCols != nil {
			if kind, ok := secureCols[name]; ok {
				if kind == "hash" {
					col["secure"] = "PASSWORD"
				} else if kind == "encrypt" {
					col["secure"] = "ENCRYPTED"
				}
			}
		}
		columns = append(columns, col)
	}

	if len(columns) == 0 {
		return &Result{Success: false, Error: "table not found"}, nil
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"table":   tableName,
		"columns": columns,
	}}, nil
}

func (t *SchemaTool) list(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	rows, err := ctx.DB.Query(
		"SELECT table_name, schema_def, created_at FROM dynamic_tables ORDER BY table_name",
	)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	defer rows.Close()

	var tables []map[string]interface{}
	for rows.Next() {
		var name, schemaDef string
		var createdAt interface{}
		if err := rows.Scan(&name, &schemaDef, &createdAt); err != nil {
			continue
		}
		tables = append(tables, map[string]interface{}{
			"table_name": name,
			"schema":     schemaDef,
			"created_at": createdAt,
		})
	}

	return &Result{Success: true, Data: tables}, nil
}

func (t *SchemaTool) drop(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	tableName, _ := args["table_name"].(string)
	if tableName == "" {
		return &Result{Success: false, Error: "table_name is required"}, nil
	}

	physicalName, err := sanitizedTableName(tableName)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	// Verify the table exists in the registry.
	var exists int
	err = ctx.DB.QueryRow(
		"SELECT COUNT(*) FROM dynamic_tables WHERE table_name = ?",
		tableName,
	).Scan(&exists)
	if err != nil || exists == 0 {
		return &Result{Success: false, Error: "table not found"}, nil
	}

	// Count rows so the owner knows how much data will be lost.
	var rowCount int
	ctx.DB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", physicalName)).Scan(&rowCount)

	// --- Approval gate: dropping a table is irreversible ---
	approvalKey := "drop_table:" + tableName
	if !CheckApproval(ctx.DB, approvalKey) {
		question := fmt.Sprintf(
			"The AI wants to DROP the table '%s' (%d rows). This will permanently delete all data in the table and remove related API endpoints. This cannot be undone. Approve?",
			tableName, rowCount,
		)
		return RequestApproval(ctx, approvalKey, question,
			fmt.Sprintf("Dropping table '%s' (%d rows) and its related endpoints.", tableName, rowCount),
		), nil
	}

	// Drop the physical table.
	if _, err := ctx.DB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", physicalName)); err != nil {
		return nil, fmt.Errorf("dropping table: %w", err)
	}

	// Remove from dynamic_tables registry.
	if _, err := ctx.DB.Exec("DELETE FROM dynamic_tables WHERE table_name = ?", tableName); err != nil {
		return nil, fmt.Errorf("removing table registry entry: %w", err)
	}

	// Remove related API endpoints.
	if _, err := ctx.DB.Exec("DELETE FROM api_endpoints WHERE table_name = ?", tableName); err != nil {
		return nil, fmt.Errorf("removing api endpoints: %w", err)
	}

	// Remove related auth endpoints.
	if _, err := ctx.DB.Exec("DELETE FROM auth_endpoints WHERE table_name = ?", tableName); err != nil {
		return nil, fmt.Errorf("removing auth endpoints: %w", err)
	}

	LogDestructiveAction(ctx, "manage_schema", "drop", tableName)

	return &Result{Success: true, Data: map[string]interface{}{
		"dropped": tableName,
	}}, nil
}

// ---------------------------------------------------------------------------
// DataTool — manage_data
// ---------------------------------------------------------------------------

// DataTool consolidates query, insert, update, delete, and count operations.
type DataTool struct{}

func (t *DataTool) Name() string { return "manage_data" }
func (t *DataTool) Description() string {
	return "Manage dynamic table data. Actions: query (select rows), insert (single row via 'data' or bulk via 'rows' array), update (by id), delete (by id), count. PASSWORD columns are auto-hashed, ENCRYPTED columns are auto-encrypted."
}
func (t *DataTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"query", "insert", "update", "delete", "count"},
				"description": "Data operation to perform",
			},
			"table_name": map[string]interface{}{
				"type":        "string",
				"description": "Logical table name",
			},
			"filters": map[string]interface{}{
				"type":        "array",
				"description": "Array of filter objects for query/count: [{\"column\": \"name\", \"op\": \"=\", \"value\": \"foo\"}]. Supported ops: =, !=, <, >, <=, >=, LIKE, NOT LIKE, IS NULL, IS NOT NULL",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"column": map[string]interface{}{"type": "string"},
						"op":     map[string]interface{}{"type": "string"},
						"value":  map[string]interface{}{},
					},
				},
			},
			"order_by": map[string]interface{}{
				"type":        "object",
				"description": "Order by specification for query: {\"column\": \"name\", \"direction\": \"ASC\"}",
				"properties": map[string]interface{}{
					"column":    map[string]interface{}{"type": "string"},
					"direction": map[string]interface{}{"type": "string", "enum": []string{"ASC", "DESC"}},
				},
			},
			"limit": map[string]interface{}{"type": "number", "description": "Maximum number of rows for query"},
			"data": map[string]interface{}{
				"type":        "object",
				"description": "Key-value pairs for single-row insert/update",
			},
			"rows": map[string]interface{}{
				"type":        "array",
				"description": "Array of row objects for bulk insert [{col: val}, ...]. Use instead of 'data' to insert multiple rows in one call.",
				"items":       map[string]interface{}{"type": "object"},
			},
			"id": map[string]interface{}{"type": "number", "description": "Row ID for update/delete"},
		},
		"required": []string{"action", "table_name"},
	}
}

func (t *DataTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	action, errResult := RequireAction(args)
	if errResult != nil {
		// Infer action from provided args — LLMs sometimes omit the action field.
		if _, hasRows := args["rows"]; hasRows {
			action = "insert"
		} else if _, hasValues := args["values"]; hasValues {
			action = "update"
		} else if _, hasWhere := args["where"]; hasWhere {
			action = "query"
		} else {
			action = "query"
		}
		args["action"] = action
	}
	switch action {
	case "query":
		return t.query(ctx, args)
	case "insert":
		return t.insert(ctx, args)
	case "update":
		return t.update(ctx, args)
	case "delete":
		return t.del(ctx, args)
	case "count":
		return t.count(ctx, args)
	default:
		return &Result{Success: false, Error: "invalid action: must be query, insert, update, delete, or count"}, nil
	}
}

func (t *DataTool) query(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	tableName, _ := args["table_name"].(string)
	physicalName, err := sanitizedTableName(tableName)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	// Load secure columns to know which to strip from results.
	secureCols, _ := loadSecureColumns(ctx, tableName)

	query := fmt.Sprintf("SELECT * FROM %s", physicalName)
	var queryParams []interface{}

	// Structured filters (replaces raw WHERE string)
	if filtersRaw, ok := args["filters"].([]interface{}); ok && len(filtersRaw) > 0 {
		whereClause, params, err := buildWhereClause(filtersRaw)
		if err != nil {
			return &Result{Success: false, Error: err.Error()}, nil
		}
		query += whereClause
		queryParams = params
	}

	// Structured order_by (replaces raw ORDER BY string)
	if orderByRaw, ok := args["order_by"].(map[string]interface{}); ok {
		col, _ := orderByRaw["column"].(string)
		dir, _ := orderByRaw["direction"].(string)
		if col != "" {
			if !validColumnName.MatchString(col) {
				return &Result{Success: false, Error: fmt.Sprintf("invalid order_by column: %s", col)}, nil
			}
			dir = strings.ToUpper(strings.TrimSpace(dir))
			if dir != "ASC" && dir != "DESC" {
				dir = "ASC"
			}
			query += fmt.Sprintf(" ORDER BY %s %s", col, dir)
		}
	}

	if limit, ok := args["limit"].(float64); ok && limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", int(limit))
	} else {
		query += " LIMIT 50" // default cap to prevent unbounded results
	}

	rows, err := ctx.DB.Query(query, queryParams...)
	if err != nil {
		return nil, fmt.Errorf("querying table: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("getting columns: %w", err)
	}

	var results []map[string]interface{}
	for rows.Next() {
		values := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		row := make(map[string]interface{})
		for i, col := range cols {
			// Strip PASSWORD columns entirely — never expose hashes.
			if secureCols != nil && secureCols[col] == "hash" {
				continue
			}
			row[col] = values[i]
		}
		results = append(results, row)
	}

	return &Result{Success: true, Data: results}, nil
}

func (t *DataTool) insert(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	tableName, _ := args["table_name"].(string)
	if tableName == "" {
		return &Result{Success: false, Error: "table_name is required"}, nil
	}

	physicalName, err := sanitizedTableName(tableName)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	// Load secure columns once for all rows.
	secureCols, _ := loadSecureColumns(ctx, tableName)

	// Bulk insert via "rows" parameter.
	if rowsRaw, ok := args["rows"].([]interface{}); ok && len(rowsRaw) > 0 {
		return t.insertBulk(ctx, physicalName, tableName, rowsRaw, secureCols)
	}

	// Single row via "data" parameter.
	data, _ := args["data"].(map[string]interface{})
	if len(data) == 0 {
		return &Result{Success: false, Error: "data or rows parameter is required for insert"}, nil
	}

	return t.insertSingle(ctx, physicalName, tableName, data, secureCols)
}

func (t *DataTool) insertSingle(ctx *ToolContext, physicalName, tableName string, data map[string]interface{}, secureCols map[string]string) (*Result, error) {
	var colNames []string
	var placeholders []string
	var values []interface{}

	for col, val := range data {
		if !validColumnName.MatchString(col) {
			return &Result{Success: false, Error: fmt.Sprintf("invalid column name: %s", col)}, nil
		}
		colNames = append(colNames, col)
		placeholders = append(placeholders, "?")

		if secureCols != nil {
			if kind, isSecure := secureCols[col]; isSecure {
				processed, err := processSecureValue(kind, val, ctx.Encryptor)
				if err != nil {
					return nil, fmt.Errorf("processing secure column %s: %w", col, err)
				}
				val = processed
			}
		}
		values = append(values, val)
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		physicalName, strings.Join(colNames, ", "), strings.Join(placeholders, ", "))

	res, err := ctx.DB.Exec(query, values...)
	if err != nil {
		return nil, fmt.Errorf("inserting row: %w", err)
	}

	id, _ := res.LastInsertId()
	return &Result{Success: true, Data: map[string]interface{}{
		"id":    id,
		"table": tableName,
	}}, nil
}

func (t *DataTool) insertBulk(ctx *ToolContext, physicalName, tableName string, rowsRaw []interface{}, secureCols map[string]string) (*Result, error) {
	if len(rowsRaw) > 100 {
		return &Result{Success: false, Error: "maximum 100 rows per bulk insert"}, nil
	}

	tx, err := ctx.DB.Begin()
	if err != nil {
		return nil, fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	var insertedIDs []int64

	for i, rowRaw := range rowsRaw {
		row, ok := rowRaw.(map[string]interface{})
		if !ok {
			return &Result{Success: false, Error: fmt.Sprintf("row %d is not a valid object", i)}, nil
		}

		var colNames []string
		var placeholders []string
		var values []interface{}

		for col, val := range row {
			if !validColumnName.MatchString(col) {
				return &Result{Success: false, Error: fmt.Sprintf("invalid column name in row %d: %s", i, col)}, nil
			}
			colNames = append(colNames, col)
			placeholders = append(placeholders, "?")

			if secureCols != nil {
				if kind, isSecure := secureCols[col]; isSecure {
					processed, err := processSecureValue(kind, val, ctx.Encryptor)
					if err != nil {
						return nil, fmt.Errorf("processing secure column %s in row %d: %w", col, i, err)
					}
					val = processed
				}
			}
			values = append(values, val)
		}

		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			physicalName, strings.Join(colNames, ", "), strings.Join(placeholders, ", "))

		res, err := tx.Exec(query, values...)
		if err != nil {
			return nil, fmt.Errorf("inserting row %d: %w", i, err)
		}

		id, _ := res.LastInsertId()
		insertedIDs = append(insertedIDs, id)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing bulk insert: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"table":    tableName,
		"inserted": len(insertedIDs),
		"first_id": insertedIDs[0],
		"last_id":  insertedIDs[len(insertedIDs)-1],
	}}, nil
}

func (t *DataTool) update(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	tableName, _ := args["table_name"].(string)
	idFloat, _ := args["id"].(float64)
	data, _ := args["data"].(map[string]interface{})
	if tableName == "" || len(data) == 0 {
		return &Result{Success: false, Error: "table_name, id, and data are required"}, nil
	}

	physicalName, err := sanitizedTableName(tableName)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	// Load secure columns to handle hashing/encryption.
	secureCols, _ := loadSecureColumns(ctx, tableName)

	var setClauses []string
	var values []interface{}

	for col, val := range data {
		if !validColumnName.MatchString(col) {
			return &Result{Success: false, Error: fmt.Sprintf("invalid column name: %s", col)}, nil
		}

		// Process secure columns.
		if secureCols != nil {
			if kind, isSecure := secureCols[col]; isSecure {
				processed, err := processSecureValue(kind, val, ctx.Encryptor)
				if err != nil {
					return nil, fmt.Errorf("processing secure column %s: %w", col, err)
				}
				val = processed
			}
		}

		setClauses = append(setClauses, fmt.Sprintf("%s = ?", col))
		values = append(values, val)
	}
	values = append(values, int64(idFloat))

	query := fmt.Sprintf("UPDATE %s SET %s WHERE id = ?",
		physicalName, strings.Join(setClauses, ", "))

	res, err := ctx.DB.Exec(query, values...)
	if err != nil {
		return nil, fmt.Errorf("updating row: %w", err)
	}

	n, _ := res.RowsAffected()
	return &Result{Success: true, Data: map[string]interface{}{
		"rows_affected": n,
	}}, nil
}

func (t *DataTool) del(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	tableName, _ := args["table_name"].(string)
	idFloat, _ := args["id"].(float64)
	if tableName == "" {
		return &Result{Success: false, Error: "table_name and id are required"}, nil
	}

	physicalName, err := sanitizedTableName(tableName)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	res, err := ctx.DB.Exec(fmt.Sprintf("DELETE FROM %s WHERE id = ?", physicalName), int64(idFloat))
	if err != nil {
		return nil, fmt.Errorf("deleting row: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return &Result{Success: false, Error: "row not found"}, nil
	}

	LogDestructiveAction(ctx, "manage_data", "delete",
		fmt.Sprintf("%s id=%d", tableName, int64(idFloat)))

	return &Result{Success: true, Data: map[string]interface{}{
		"deleted_id": int64(idFloat),
	}}, nil
}

func (t *DataTool) count(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	tableName, _ := args["table_name"].(string)
	if tableName == "" {
		return &Result{Success: false, Error: "table_name is required"}, nil
	}

	physicalName, err := sanitizedTableName(tableName)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", physicalName)
	var queryParams []interface{}

	if filtersRaw, ok := args["filters"].([]interface{}); ok && len(filtersRaw) > 0 {
		whereClause, params, err := buildWhereClause(filtersRaw)
		if err != nil {
			return &Result{Success: false, Error: err.Error()}, nil
		}
		query += whereClause
		queryParams = params
	}

	var count int
	err = ctx.DB.QueryRow(query, queryParams...).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("counting rows: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"table": tableName,
		"count": count,
	}}, nil
}
