/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/markdr-hue/IATAN/events"
)

// Executor dispatches tool calls through the registry and publishes events.
type Executor struct {
	registry *Registry
}

// NewExecutor creates an Executor backed by the given registry.
func NewExecutor(registry *Registry) *Executor {
	return &Executor{registry: registry}
}

// Execute looks up the named tool, runs it, publishes an event, and returns
// the result JSON-marshalled for LLM consumption.
func (e *Executor) Execute(ctx context.Context, toolCtx *ToolContext, name string, args map[string]interface{}) (string, error) {
	tool, err := e.registry.Get(name)
	if err != nil {
		return e.marshalError(fmt.Sprintf("unknown tool: %s", name))
	}

	start := time.Now()
	result, execErr := tool.Execute(toolCtx, args)
	duration := time.Since(start)

	// Build event payload.
	payload := map[string]interface{}{
		"tool":        name,
		"args":        args,
		"duration_ms": duration.Milliseconds(),
	}

	if execErr != nil {
		payload["error"] = execErr.Error()
		if toolCtx.Bus != nil {
			toolCtx.Bus.Publish(events.NewEvent(events.EventToolFailed, toolCtx.SiteID, payload))
		}
		return e.marshalError(execErr.Error())
	}

	if result == nil {
		result = &Result{Success: true}
	}

	payload["success"] = result.Success
	if toolCtx.Bus != nil {
		toolCtx.Bus.Publish(events.NewEvent(events.EventToolExecuted, toolCtx.SiteID, payload))
	}

	data, err := json.Marshal(result)
	if err != nil {
		return e.marshalError(fmt.Sprintf("failed to marshal result: %v", err))
	}
	return string(data), nil
}

// marshalError returns a JSON-encoded error Result.
func (e *Executor) marshalError(msg string) (string, error) {
	r := &Result{Success: false, Error: msg}
	data, _ := json.Marshal(r)
	return string(data), nil
}
