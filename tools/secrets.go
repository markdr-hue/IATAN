/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"fmt"
	"time"

	"github.com/markdr-hue/IATAN/events"
)

// SecretsTool consolidates store, list, and delete into a single
// manage_secrets tool.
type SecretsTool struct{}

func (t *SecretsTool) Name() string { return "manage_secrets" }
func (t *SecretsTool) Description() string {
	return "Manage encrypted secrets. Actions: store (encrypt and save), list (names only, never values), delete."
}

func (t *SecretsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"store", "list", "delete"},
			},
			"name":  map[string]interface{}{"type": "string", "description": "Unique name for the secret (e.g. 'openai_api_key')"},
			"value": map[string]interface{}{"type": "string", "description": "The secret value to encrypt and store (for store action)"},
		},
		"required": []string{"action"},
	}
}

func (t *SecretsTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	action, errResult := RequireAction(args)
	if errResult != nil {
		return errResult, nil
	}
	switch action {
	case "store":
		return t.executeStore(ctx, args)
	case "list":
		return t.executeList(ctx, args)
	case "delete":
		return t.executeDelete(ctx, args)
	default:
		return &Result{Success: false, Error: fmt.Sprintf("unknown action: %q (use store, list, delete)", action)}, nil
	}
}

func (t *SecretsTool) executeStore(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["name"].(string)
	value, _ := args["value"].(string)
	if name == "" || value == "" {
		return &Result{Success: false, Error: "name and value are required"}, nil
	}

	if ctx.Encryptor == nil {
		return &Result{Success: false, Error: "encryption not available"}, nil
	}

	encrypted, err := ctx.Encryptor.Encrypt(value)
	if err != nil {
		return nil, fmt.Errorf("encrypting secret: %w", err)
	}

	_, err = ctx.DB.Exec(
		`INSERT INTO secrets (name, value_encrypted, updated_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(name) DO UPDATE SET value_encrypted = excluded.value_encrypted, updated_at = CURRENT_TIMESTAMP`,
		name, encrypted,
	)
	if err != nil {
		return nil, fmt.Errorf("storing secret: %w", err)
	}

	if ctx.Bus != nil {
		ctx.Bus.Publish(events.NewEvent(events.EventSecretStored, ctx.SiteID, map[string]interface{}{
			"name": name,
		}))
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"name":    name,
		"message": fmt.Sprintf("Secret '%s' stored successfully", name),
	}}, nil
}

func (t *SecretsTool) executeList(ctx *ToolContext, _ map[string]interface{}) (*Result, error) {
	rows, err := ctx.DB.Query(
		"SELECT id, name, created_at, updated_at FROM secrets ORDER BY name",
	)
	if err != nil {
		return nil, fmt.Errorf("listing secrets: %w", err)
	}
	defer rows.Close()

	var secrets []map[string]interface{}
	for rows.Next() {
		var id int
		var name string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &name, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning secret: %w", err)
		}
		secrets = append(secrets, map[string]interface{}{
			"id":         id,
			"name":       name,
			"created_at": createdAt,
			"updated_at": updatedAt,
		})
	}

	return &Result{Success: true, Data: secrets}, nil
}

func (t *SecretsTool) executeDelete(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return &Result{Success: false, Error: "name is required"}, nil
	}

	result, err := ctx.DB.Exec(
		"DELETE FROM secrets WHERE name = ?",
		name,
	)
	if err != nil {
		return nil, fmt.Errorf("deleting secret: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return &Result{Success: false, Error: fmt.Sprintf("secret '%s' not found", name)}, nil
	}

	LogDestructiveAction(ctx, "manage_secrets", "delete", name)

	return &Result{Success: true, Data: map[string]interface{}{
		"name":    name,
		"message": fmt.Sprintf("Secret '%s' deleted", name),
	}}, nil
}
