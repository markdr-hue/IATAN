/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/markdr-hue/IATAN/internal/cron"
)

// computeNextRun calculates the next run time for a task.
// For interval-based tasks, adds interval seconds to now.
// For cron-based tasks, uses the shared cron parser.
func computeNextRun(cronExpr string, intervalSec int, now time.Time) time.Time {
	if cronExpr != "" {
		return cron.NextTime(cronExpr, now)
	}
	if intervalSec > 0 {
		return now.Add(time.Duration(intervalSec) * time.Second)
	}
	return now.Add(1 * time.Hour)
}

// SchedulerTool consolidates create, list, update, and delete into a single
// manage_scheduler tool.
type SchedulerTool struct{}

func (t *SchedulerTool) Name() string { return "manage_scheduler" }
func (t *SchedulerTool) Description() string {
	return "Manage scheduled tasks. Actions: create, list, update, delete. Tasks run on a cron schedule (e.g. '0 8 * * *' = daily 8am) OR interval_seconds — provide one or the other, not both (cron takes precedence if both given). When fired, the brain executes the prompt with full tool access — it can query data, send emails, update pages, and make HTTP requests."
}

func (t *SchedulerTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"create", "list", "update", "delete"},
			},
			"id":               map[string]interface{}{"type": "number", "description": "Task ID (for update/delete)"},
			"name":             map[string]interface{}{"type": "string", "description": "Name of the scheduled task"},
			"description":      map[string]interface{}{"type": "string", "description": "Description of what the task does"},
			"cron_expression":  map[string]interface{}{"type": "string", "description": "Cron expression for scheduling (e.g. '0 */6 * * *')"},
			"interval_seconds": map[string]interface{}{"type": "number", "description": "Alternative: run every N seconds"},
			"prompt":           map[string]interface{}{"type": "string", "description": "Prompt to execute when the task runs"},
			"is_enabled":       map[string]interface{}{"type": "boolean", "description": "Enable or disable the task"},
		},
		"required": []string{"action"},
	}
}

func (t *SchedulerTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	return DispatchAction(ctx, args, map[string]ActionHandler{
		"create": t.executeCreate,
		"list":   t.executeList,
		"update": t.executeUpdate,
		"delete": t.executeDelete,
	}, nil)
}

func (t *SchedulerTool) executeCreate(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	name, _ := args["name"].(string)
	prompt, _ := args["prompt"].(string)
	if name == "" || prompt == "" {
		return &Result{Success: false, Error: "name and prompt are required"}, nil
	}
	description, _ := args["description"].(string)
	cronExpr, _ := args["cron_expression"].(string)
	intervalSec, _ := args["interval_seconds"].(float64)

	// Validate cron expression if provided.
	if cronExpr != "" {
		if err := cron.Validate(cronExpr); err != nil {
			return &Result{Success: false, Error: "invalid cron expression: " + err.Error()}, nil
		}
	}

	// Compute initial next_run so the scheduler picks it up immediately.
	now := time.Now()
	var nextRun *time.Time
	if cronExpr != "" {
		nr := computeNextRun(cronExpr, 0, now)
		nextRun = &nr
	} else if intervalSec > 0 {
		nr := computeNextRun("", int(intervalSec), now)
		nextRun = &nr
	}

	result, err := ctx.DB.Exec(
		"INSERT INTO scheduled_tasks (name, description, cron_expression, interval_seconds, prompt, next_run) VALUES (?, ?, ?, ?, ?, ?)",
		name, description, cronExpr, int(intervalSec), prompt, nextRun,
	)
	if err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}

	id, _ := result.LastInsertId()
	data := map[string]interface{}{
		"id":   id,
		"name": name,
	}
	if nextRun != nil {
		data["next_run"] = *nextRun
	}
	return &Result{Success: true, Data: data}, nil
}

func (t *SchedulerTool) executeList(ctx *ToolContext, _ map[string]interface{}) (*Result, error) {
	rows, err := ctx.DB.Query(
		"SELECT id, name, description, cron_expression, interval_seconds, prompt, is_enabled, last_run, next_run, created_at FROM scheduled_tasks ORDER BY name",
	)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	var tasks []map[string]interface{}
	for rows.Next() {
		var id int
		var name string
		var description, cronExpr, prompt sql.NullString
		var intervalSec sql.NullInt64
		var isEnabled bool
		var lastRun, nextRun sql.NullTime
		var createdAt time.Time

		if err := rows.Scan(&id, &name, &description, &cronExpr, &intervalSec, &prompt, &isEnabled, &lastRun, &nextRun, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning task: %w", err)
		}

		task := map[string]interface{}{
			"id":         id,
			"name":       name,
			"is_enabled": isEnabled,
			"created_at": createdAt,
		}
		if description.Valid {
			task["description"] = description.String
		}
		if cronExpr.Valid {
			task["cron_expression"] = cronExpr.String
		}
		if intervalSec.Valid {
			task["interval_seconds"] = intervalSec.Int64
		}
		if prompt.Valid {
			task["prompt"] = prompt.String
		}
		if lastRun.Valid {
			task["last_run"] = lastRun.Time
		}
		if nextRun.Valid {
			task["next_run"] = nextRun.Time
		}
		tasks = append(tasks, task)
	}

	return &Result{Success: true, Data: tasks}, nil
}

func (t *SchedulerTool) executeUpdate(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	idFloat, _ := args["id"].(float64)
	if idFloat == 0 {
		return &Result{Success: false, Error: "id is required"}, nil
	}
	taskID := int64(idFloat)

	setClauses := []string{"updated_at = CURRENT_TIMESTAMP"}
	var values []interface{}

	if name, ok := args["name"].(string); ok && name != "" {
		setClauses = append(setClauses, "name = ?")
		values = append(values, name)
	}
	if description, ok := args["description"].(string); ok {
		setClauses = append(setClauses, "description = ?")
		values = append(values, description)
	}
	var newCronExpr string
	var newIntervalSec int
	var scheduleChanged bool
	if cronExpr, ok := args["cron_expression"].(string); ok {
		if cronExpr != "" {
			if err := cron.Validate(cronExpr); err != nil {
				return &Result{Success: false, Error: "invalid cron expression: " + err.Error()}, nil
			}
		}
		setClauses = append(setClauses, "cron_expression = ?")
		values = append(values, cronExpr)
		newCronExpr = cronExpr
		scheduleChanged = true
	}
	if intervalSec, ok := args["interval_seconds"].(float64); ok {
		setClauses = append(setClauses, "interval_seconds = ?")
		values = append(values, int(intervalSec))
		newIntervalSec = int(intervalSec)
		scheduleChanged = true
	}
	if prompt, ok := args["prompt"].(string); ok && prompt != "" {
		setClauses = append(setClauses, "prompt = ?")
		values = append(values, prompt)
	}
	if isEnabled, ok := args["is_enabled"].(bool); ok {
		setClauses = append(setClauses, "is_enabled = ?")
		values = append(values, isEnabled)
	}

	values = append(values, taskID)

	query := fmt.Sprintf("UPDATE scheduled_tasks SET %s WHERE id = ?",
		strings.Join(setClauses, ", "))

	res, err := ctx.DB.Exec(query, values...)
	if err != nil {
		return nil, fmt.Errorf("updating task: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return &Result{Success: false, Error: "task not found"}, nil
	}

	// Recalculate next_run if the schedule changed.
	if scheduleChanged {
		now := time.Now()
		nextRun := computeNextRun(newCronExpr, newIntervalSec, now)
		ctx.DB.Exec(
			"UPDATE scheduled_tasks SET next_run = ? WHERE id = ?",
			nextRun, taskID,
		)
	}

	return &Result{Success: true, Data: map[string]interface{}{"id": taskID}}, nil
}

func (t *SchedulerTool) executeDelete(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	idFloat, _ := args["id"].(float64)
	if idFloat == 0 {
		return &Result{Success: false, Error: "id is required"}, nil
	}
	taskID := int64(idFloat)

	res, err := ctx.DB.Exec(
		"DELETE FROM scheduled_tasks WHERE id = ?",
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("deleting task: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return &Result{Success: false, Error: "task not found"}, nil
	}

	return &Result{Success: true, Data: map[string]interface{}{"deleted_id": taskID}}, nil
}
