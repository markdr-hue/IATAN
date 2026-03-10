/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"database/sql"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// manage_memory — unified memory manager
// ---------------------------------------------------------------------------

type MemoryTool struct{}

func (t *MemoryTool) Name() string { return "manage_memory" }
func (t *MemoryTool) Description() string {
	return "Manage site memory (persistent key-value store). Actions: remember (store), recall (retrieve by key or category), list (list all), forget (delete)."
}
func (t *MemoryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action":   map[string]interface{}{"type": "string", "enum": []string{"remember", "recall", "list", "forget"}, "description": "Action to perform"},
			"key":      map[string]interface{}{"type": "string", "description": "Unique key for the memory (required for remember, forget; optional for recall)"},
			"value":    map[string]interface{}{"type": "string", "description": "Value to remember (required for remember)"},
			"category": map[string]interface{}{"type": "string", "description": "Category for grouping memories (default: general)"},
		},
		"required": []string{"action"},
	}
}

func (t *MemoryTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	return DispatchAction(ctx, args, map[string]ActionHandler{
		"remember": t.remember,
		"recall":   t.recall,
		"list":     t.list,
		"forget":   t.forget,
	}, nil)
}

func (t *MemoryTool) remember(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	key, _ := args["key"].(string)
	value, _ := args["value"].(string)
	if key == "" || value == "" {
		return &Result{Success: false, Error: "key and value are required"}, nil
	}
	category, _ := args["category"].(string)
	if category == "" {
		category = "general"
	}

	_, err := ctx.DB.Exec(
		`INSERT INTO memory (key, value, category, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET
		   value = excluded.value,
		   category = excluded.category,
		   updated_at = CURRENT_TIMESTAMP`,
		key, value, category,
	)
	if err != nil {
		return nil, fmt.Errorf("remembering: %w", err)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"key":      key,
		"category": category,
	}}, nil
}

func (t *MemoryTool) recall(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	key, _ := args["key"].(string)
	category, _ := args["category"].(string)

	// Look up specific key.
	if key != "" {
		var value, cat string
		var updatedAt time.Time
		err := ctx.DB.QueryRow(
			"SELECT value, category, updated_at FROM memory WHERE key = ?",
			key,
		).Scan(&value, &cat, &updatedAt)
		if err == sql.ErrNoRows {
			return &Result{Success: false, Error: "memory not found"}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("recalling: %w", err)
		}
		return &Result{Success: true, Data: map[string]interface{}{
			"key":        key,
			"value":      value,
			"category":   cat,
			"updated_at": updatedAt,
		}}, nil
	}

	// List by category.
	query := "SELECT key, value, category, updated_at FROM memory"
	var queryArgs []interface{}
	if category != "" {
		query += " WHERE category = ?"
		queryArgs = append(queryArgs, category)
	}
	query += " ORDER BY key"

	rows, err := ctx.DB.Query(query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("recalling by category: %w", err)
	}
	defer rows.Close()

	var memories []map[string]interface{}
	for rows.Next() {
		var k, v, c string
		var updatedAt time.Time
		if err := rows.Scan(&k, &v, &c, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning memory: %w", err)
		}
		memories = append(memories, map[string]interface{}{
			"key":        k,
			"value":      v,
			"category":   c,
			"updated_at": updatedAt,
		})
	}

	return &Result{Success: true, Data: memories}, nil
}

func (t *MemoryTool) list(ctx *ToolContext, _ map[string]interface{}) (*Result, error) {
	rows, err := ctx.DB.Query(
		"SELECT key, value, category, updated_at FROM memory ORDER BY category, key",
	)
	if err != nil {
		return nil, fmt.Errorf("listing memories: %w", err)
	}
	defer rows.Close()

	var memories []map[string]interface{}
	for rows.Next() {
		var k, v, c string
		var updatedAt time.Time
		if err := rows.Scan(&k, &v, &c, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning memory: %w", err)
		}
		memories = append(memories, map[string]interface{}{
			"key":        k,
			"value":      v,
			"category":   c,
			"updated_at": updatedAt,
		})
	}

	return &Result{Success: true, Data: memories}, nil
}

func (t *MemoryTool) forget(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	key, _ := args["key"].(string)
	if key == "" {
		return &Result{Success: false, Error: "key is required"}, nil
	}

	res, err := ctx.DB.Exec(
		"DELETE FROM memory WHERE key = ?",
		key,
	)
	if err != nil {
		return nil, fmt.Errorf("forgetting: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return &Result{Success: false, Error: "memory not found"}, nil
	}

	return &Result{Success: true, Data: map[string]interface{}{"deleted": key}}, nil
}
