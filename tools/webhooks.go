/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// manage_webhooks — unified webhook manager
// ---------------------------------------------------------------------------

type WebhooksTool struct{}

func (t *WebhooksTool) Name() string { return "manage_webhooks" }
func (t *WebhooksTool) Description() string {
	return "Manage webhooks. Actions: create (incoming/outgoing), get (details+subscriptions), list, delete, update (url/enabled), subscribe (to event types)."
}
func (t *WebhooksTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action":       map[string]interface{}{"type": "string", "enum": []string{"create", "get", "list", "delete", "update", "subscribe"}, "description": "Action to perform"},
			"name":         map[string]interface{}{"type": "string", "description": "Webhook name (for create, get, delete, update)"},
			"url":          map[string]interface{}{"type": "string", "description": "URL for outgoing webhooks (omit for incoming)"},
			"is_enabled":   map[string]interface{}{"type": "boolean", "description": "Enable or disable the webhook"},
			"webhook_name": map[string]interface{}{"type": "string", "description": "Webhook name (for subscribe)"},
			"event_types": map[string]interface{}{
				"type":        "array",
				"description": "Event types to subscribe to (e.g. page.created, page.updated, brain.tick)",
				"items":       map[string]interface{}{"type": "string"},
			},
		},
		"required": []string{"action"},
	}
}

func (t *WebhooksTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	action, errResult := RequireAction(args)
	if errResult != nil {
		return errResult, nil
	}
	switch action {
	case "create":
		return t.create(ctx, args)
	case "get":
		return t.get(ctx, args)
	case "list":
		return t.list(ctx, args)
	case "delete":
		return t.delete(ctx, args)
	case "update":
		return t.update(ctx, args)
	case "subscribe":
		return t.subscribe(ctx, args)
	default:
		return &Result{Success: false, Error: "unknown action: " + action}, nil
	}
}

func (t *WebhooksTool) create(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return &Result{Success: false, Error: "name is required"}, nil
	}
	url, _ := args["url"].(string)

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("generating secret: %w", err)
	}
	secret := hex.EncodeToString(secretBytes)

	direction := "incoming"
	if url != "" {
		direction = "outgoing"
	}

	result, err := ctx.DB.Exec(
		"INSERT INTO webhooks (name, secret, url, direction) VALUES (?, ?, ?, ?)",
		name, secret, url, direction,
	)
	if err != nil {
		return nil, fmt.Errorf("creating webhook: %w", err)
	}

	id, _ := result.LastInsertId()
	data := map[string]interface{}{
		"id":        id,
		"name":      name,
		"secret":    secret,
		"direction": direction,
	}
	if url != "" {
		data["url"] = url
	}
	return &Result{Success: true, Data: data}, nil
}

func (t *WebhooksTool) get(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return &Result{Success: false, Error: "name is required"}, nil
	}

	var id int
	var secret, direction string
	var url sql.NullString
	var isEnabled bool
	var lastTriggered sql.NullTime
	var createdAt time.Time

	err := ctx.DB.QueryRow(
		"SELECT id, secret, url, direction, is_enabled, last_triggered, created_at FROM webhooks WHERE name = ?",
		name,
	).Scan(&id, &secret, &url, &direction, &isEnabled, &lastTriggered, &createdAt)
	if err == sql.ErrNoRows {
		return &Result{Success: false, Error: "webhook not found"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying webhook: %w", err)
	}

	wh := map[string]interface{}{
		"id":         id,
		"name":       name,
		"secret":     secret,
		"direction":  direction,
		"is_enabled": isEnabled,
		"created_at": createdAt,
	}
	if url.Valid && url.String != "" {
		wh["url"] = url.String
	}
	if lastTriggered.Valid {
		wh["last_triggered"] = lastTriggered.Time
	}

	rows, err := ctx.DB.Query(
		"SELECT event_type FROM webhook_subscriptions WHERE webhook_id = ? ORDER BY event_type",
		id,
	)
	if err == nil {
		defer rows.Close()
		var subs []string
		for rows.Next() {
			var et string
			if rows.Scan(&et) == nil {
				subs = append(subs, et)
			}
		}
		if len(subs) > 0 {
			wh["subscriptions"] = subs
		}
	}

	return &Result{Success: true, Data: wh}, nil
}

func (t *WebhooksTool) list(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	rows, err := ctx.DB.Query(
		"SELECT id, name, direction, url, is_enabled, last_triggered, created_at FROM webhooks ORDER BY name",
	)
	if err != nil {
		return nil, fmt.Errorf("listing webhooks: %w", err)
	}
	defer rows.Close()

	var webhooks []map[string]interface{}
	for rows.Next() {
		var id int
		var name, direction string
		var url sql.NullString
		var isEnabled bool
		var lastTriggered sql.NullTime
		var createdAt time.Time
		if err := rows.Scan(&id, &name, &direction, &url, &isEnabled, &lastTriggered, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning webhook: %w", err)
		}
		wh := map[string]interface{}{
			"id":         id,
			"name":       name,
			"direction":  direction,
			"is_enabled": isEnabled,
			"created_at": createdAt,
		}
		if url.Valid && url.String != "" {
			wh["url"] = url.String
		}
		if lastTriggered.Valid {
			wh["last_triggered"] = lastTriggered.Time
		}
		webhooks = append(webhooks, wh)
	}

	return &Result{Success: true, Data: webhooks}, nil
}

func (t *WebhooksTool) delete(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return &Result{Success: false, Error: "name is required"}, nil
	}

	res, err := ctx.DB.Exec(
		"DELETE FROM webhooks WHERE name = ?",
		name,
	)
	if err != nil {
		return nil, fmt.Errorf("deleting webhook: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return &Result{Success: false, Error: "webhook not found"}, nil
	}

	return &Result{Success: true, Data: map[string]interface{}{"deleted": name}}, nil
}

func (t *WebhooksTool) update(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return &Result{Success: false, Error: "name is required"}, nil
	}

	var setClauses []string
	var values []interface{}

	if url, ok := args["url"].(string); ok {
		setClauses = append(setClauses, "url = ?")
		values = append(values, url)
	}
	if isEnabled, ok := args["is_enabled"].(bool); ok {
		setClauses = append(setClauses, "is_enabled = ?")
		values = append(values, isEnabled)
	}

	if len(setClauses) == 0 {
		return &Result{Success: false, Error: "provide at least one field to update (url or is_enabled)"}, nil
	}

	values = append(values, name)
	query := fmt.Sprintf("UPDATE webhooks SET %s WHERE name = ?",
		strings.Join(setClauses, ", "))

	res, err := ctx.DB.Exec(query, values...)
	if err != nil {
		return nil, fmt.Errorf("updating webhook: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return &Result{Success: false, Error: "webhook not found"}, nil
	}

	return &Result{Success: true, Data: map[string]interface{}{"updated": name}}, nil
}

func (t *WebhooksTool) subscribe(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["webhook_name"].(string)
	if name == "" {
		return &Result{Success: false, Error: "webhook_name is required"}, nil
	}

	eventTypesRaw, _ := args["event_types"].([]interface{})
	if len(eventTypesRaw) == 0 {
		return &Result{Success: false, Error: "event_types is required"}, nil
	}

	var webhookID int
	err := ctx.DB.QueryRow(
		"SELECT id FROM webhooks WHERE name = ?",
		name,
	).Scan(&webhookID)
	if err != nil {
		return &Result{Success: false, Error: "webhook not found"}, nil
	}

	var subscribed []string
	for _, et := range eventTypesRaw {
		eventType, ok := et.(string)
		if !ok || eventType == "" {
			continue
		}
		_, err := ctx.DB.Exec(
			"INSERT INTO webhook_subscriptions (webhook_id, event_type) VALUES (?, ?) ON CONFLICT(webhook_id, event_type) DO NOTHING",
			webhookID, eventType,
		)
		if err == nil {
			subscribed = append(subscribed, eventType)
		}
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"webhook_name": name,
		"subscribed":   subscribed,
	}}, nil
}
