/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/markdr-hue/IATAN/db"
	"github.com/markdr-hue/IATAN/db/models"
	"github.com/markdr-hue/IATAN/events"
	"github.com/markdr-hue/IATAN/llm"
	"github.com/markdr-hue/IATAN/tools"
)

const (
	monitoringBaseDefault = 5 * time.Minute
	monitoringMaxDefault  = 15 * time.Minute
	idleThreshold         = 3
	maxStageRetries       = 3
	llmTimeout            = 5 * time.Minute
)

// PipelineWorker is a goroutine that autonomously builds a site using a
// deterministic stage pipeline. It replaces the tick-based BrainWorker.
type PipelineWorker struct {
	siteID   int
	siteDB   *db.SiteDB
	deps     *Deps
	logger   *slog.Logger
	commands chan BrainCommand

	mu            sync.RWMutex
	state         BrainState
	idleTickCount int
	wakeContext   map[string]interface{}

	semaphore chan struct{}
}

// NewPipelineWorker creates a new pipeline worker for the given site.
func NewPipelineWorker(siteID int, deps *Deps, semaphore chan struct{}) (*PipelineWorker, error) {
	siteDB, err := deps.SiteDBManager.Open(siteID)
	if err != nil {
		return nil, err
	}
	return &PipelineWorker{
		siteID:    siteID,
		siteDB:    siteDB,
		deps:      deps,
		logger:    slog.With("component", "pipeline", "site_id", siteID),
		commands:  make(chan BrainCommand, 16),
		state:     StateIdle,
		semaphore: semaphore,
	}, nil
}

// State returns the current worker state.
func (w *PipelineWorker) State() BrainState {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.state
}

// Send enqueues a command for the worker.
func (w *PipelineWorker) Send(cmd BrainCommand) error {
	select {
	case w.commands <- cmd:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("pipeline command channel full, command %s not delivered", cmd.Type)
	}
}

// Run is the main loop. It should be called in its own goroutine.
func (w *PipelineWorker) Run(ctx context.Context) {
	w.logger.Info("pipeline worker started")
	if w.deps.Bus != nil {
		w.deps.Bus.Publish(events.NewEvent(events.EventBrainStarted, w.siteID, map[string]interface{}{
			"site_id": w.siteID,
		}))
	}

	defer func() {
		w.logger.Info("pipeline worker stopped")
		if w.deps.Bus != nil {
			w.deps.Bus.Publish(events.NewEvent(events.EventBrainStopped, w.siteID, map[string]interface{}{
				"site_id": w.siteID,
			}))
		}
	}()

	// Load site mode to determine initial behavior.
	site, err := models.GetSiteByID(w.deps.DB, w.siteID)
	if err != nil {
		w.logger.Error("failed to load site", "error", err)
		return
	}

	switch site.Mode {
	case "building":
		w.setState(StateBuilding)
		// Auto-recover from crashes: reset error count and clear non-question pauses
		// so the pipeline can resume instead of staying stuck in paused loop.
		// But if error_count was high (repeated failures), keep the pause to avoid
		// burning tokens on the same failure after restart.
		var prevErrorCount int
		w.siteDB.DB.QueryRow("SELECT error_count FROM pipeline_state WHERE id = 1").Scan(&prevErrorCount)
		if prevErrorCount >= maxStageRetries {
			w.logger.Warn("auto-recovery skipped: previous error_count was high, manual resume required", "error_count", prevErrorCount)
			w.setState(StatePaused)
			w.runPausedLoop(ctx)
			return
		}
		if prevErrorCount > 0 {
			w.logger.Info("auto-recovery: clearing error_count", "previous_count", prevErrorCount)
		}
		w.siteDB.ExecWrite(`UPDATE pipeline_state SET error_count = 0, updated_at = CURRENT_TIMESTAMP WHERE id = 1 AND error_count > 0`)
		w.siteDB.ExecWrite(`UPDATE pipeline_state SET paused = 0, pause_reason = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = 1 AND paused = 1 AND pause_reason NOT IN (?, ?)`, PauseReasonOwnerAnswers, PauseReasonApproval)
		w.runBuildPipeline(ctx)
	case "monitoring":
		w.setState(StateMonitoring)
		w.runMonitoringLoop(ctx)
	case "paused":
		w.setState(StatePaused)
		w.runPausedLoop(ctx)
	default:
		w.setState(StateIdle)
		w.runPausedLoop(ctx)
	}
}

// runBuildPipeline executes the deterministic build pipeline, resuming from
// the current stage recorded in pipeline_state.
func (w *PipelineWorker) runBuildPipeline(ctx context.Context) {
	state, err := LoadPipelineState(w.siteDB.DB)
	if err != nil {
		w.logger.Error("failed to load pipeline state", "error", err)
		w.publishBrainError(fmt.Sprintf("Failed to load pipeline state: %v", err))
		return
	}

	if state.Paused {
		w.setState(StatePaused)
		w.publishBrainMessage(fmt.Sprintf("Pipeline paused: %s", state.PauseReason))
		w.runPausedLoop(ctx)
		return
	}

	// Execute stages sequentially from current stage.
	stage := state.Stage
	stageRetries := 0
	for {
		if ctx.Err() != nil {
			return
		}

		w.logger.Info("executing pipeline stage", "stage", stage)
		w.publishBrainMessage(fmt.Sprintf("Starting stage: **%s**", stage))

		var nextStage PipelineStage
		var stageErr error

		// Terminal stages that exit the build loop.
		if stage == StageMonitoring {
			w.setState(StateMonitoring)
			w.runMonitoringLoop(ctx)
			return
		}

		// Run the stage in a closure with panic recovery so a nil-pointer
		// or index-out-of-bounds inside any stage doesn't kill the worker.
		func() {
			defer func() {
				if r := recover(); r != nil {
					stageErr = fmt.Errorf("panic in stage %s: %v", stage, r)
					w.logger.Error("stage panic", "stage", stage, "panic", r)
				}
			}()
			switch stage {
			case StagePlan:
				nextStage, stageErr = w.runPlan(ctx)
			case StageDesign:
				nextStage, stageErr = w.runDesign(ctx)
			case StageDataLayer:
				nextStage, stageErr = w.runDataLayer(ctx)
			case StageBuildPages:
				nextStage, stageErr = w.runBuildPages(ctx)
			case StageReview:
				nextStage, stageErr = w.runReview(ctx)
			case StageComplete:
				nextStage, stageErr = w.runComplete(ctx)
			case StageUpdatePlan:
				nextStage, stageErr = w.runUpdatePlan(ctx)
			default:
				stageErr = fmt.Errorf("unknown pipeline stage: %s", stage)
			}
		}()

		if stageErr != nil {
			// Check if the stage intentionally paused (e.g., awaiting owner answers).
			// Don't count intentional pauses as errors.
			ps, psErr := LoadPipelineState(w.siteDB.DB)
			if psErr != nil {
				w.logger.Warn("failed to check pause state after stage error", "error", psErr)
			}
			if ps != nil && ps.Paused {
				w.logger.Info("stage paused intentionally", "stage", stage, "reason", ps.PauseReason)
				w.setState(StatePaused)
				w.runPausedLoop(ctx)
				return
			}

			w.logger.Error("stage failed", "stage", stage, "error", stageErr)

			// Circuit breaker: permanent errors (bad config) should pause
			// immediately instead of burning retries that can never succeed.
			if isPermanentError(stageErr) {
				PausePipeline(w.siteDB, fmt.Sprintf("permanent error in %s: %v", stage, stageErr))
				w.publishBrainError(fmt.Sprintf("Pipeline paused: **%s** — %v. Fix the configuration and resume.", stage, stageErr))
				w.setState(StatePaused)
				w.runPausedLoop(ctx)
				return
			}

			IncrementErrorCount(w.siteDB, stageErr.Error())
			stageRetries++

			if stageRetries >= maxStageRetries {
				PausePipeline(w.siteDB, fmt.Sprintf("stage %s failed %d times", stage, stageRetries))
				w.publishBrainError(fmt.Sprintf("Pipeline paused: stage **%s** failed %d consecutive times. Last error: %v", stage, stageRetries, stageErr))
				w.setState(StatePaused)
				w.runPausedLoop(ctx)
				return
			}

			// Backoff before retrying to prevent tight loops.
			backoff := time.Duration(stageRetries) * 5 * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			w.logger.Info("retrying stage after backoff", "stage", stage, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			continue // retry same stage
		}

		// Stage succeeded — advance (also resets error count atomically).
		if err := AdvanceStage(w.siteDB, nextStage); err != nil {
			w.logger.Error("failed to advance stage", "error", err)
			// Don't update in-memory stage — retry so the DB stays consistent.
			// Stages are idempotent, so re-executing is safe.
			continue
		}
		stage = nextStage
		stageRetries = 0
	}
}

// runMonitoringLoop handles the monitoring phase with adaptive timing.
func (w *PipelineWorker) runMonitoringLoop(ctx context.Context) {
	interval := w.monitoringInterval()
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-w.commands:
			w.handleCommand(ctx, cmd)
			timer.Reset(w.monitoringInterval())
		case <-timer.C:
			w.monitoringTick(ctx)
			timer.Reset(w.monitoringInterval())
		}
	}
}

// runPausedLoop waits for commands while paused.
func (w *PipelineWorker) runPausedLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-w.commands:
			w.handleCommand(ctx, cmd)
			// Check if we were unpaused.
			if w.State() == StateBuilding {
				w.runBuildPipeline(ctx)
				return
			}
			if w.State() == StateMonitoring {
				w.runMonitoringLoop(ctx)
				return
			}
		}
	}
}

// handleCommand dispatches a command.
func (w *PipelineWorker) handleCommand(ctx context.Context, cmd BrainCommand) {
	w.logger.Info("handling command", "type", cmd.Type)

	switch cmd.Type {
	case CommandWake:
		w.mu.Lock()
		w.idleTickCount = 0
		if cmd.Payload != nil {
			w.wakeContext = cmd.Payload
		}
		w.mu.Unlock()
		// If paused waiting for answers, resume the pipeline.
		if w.State() == StatePaused {
			ResumePipeline(w.siteDB)
			w.setState(StateBuilding)
		}

	case CommandModeChange:
		if mode, ok := cmd.Payload["mode"].(string); ok {
			switch mode {
			case "building":
				w.setState(StateBuilding)
				// Reset pipeline for fresh build.
				ResetPipeline(w.siteDB)
			case "monitoring":
				w.setState(StateMonitoring)
			case "paused":
				w.setState(StatePaused)
			}
			if w.deps.Bus != nil {
				w.deps.Bus.Publish(events.NewEvent(events.EventBrainModeChanged, w.siteID, map[string]interface{}{
					"site_id": w.siteID,
					"mode":    mode,
				}))
			}
		}

	case CommandUpdate:
		// Trigger incremental update with change description.
		w.mu.Lock()
		w.idleTickCount = 0
		w.mu.Unlock()
		w.setState(StateBuilding)
		desc := ""
		if d, ok := cmd.Payload["description"].(string); ok {
			desc = d
		}
		w.siteDB.ExecWrite("UPDATE pipeline_state SET update_description = ? WHERE id = 1", desc)
		if err := AdvanceStage(w.siteDB, StageUpdatePlan); err != nil {
			w.logger.Error("failed to set update stage", "error", err)
		}

	case CommandScheduledTask:
		if prompt, ok := cmd.Payload["prompt"].(string); ok {
			runID := payloadInt64(cmd.Payload, "run_id")
			taskID := int(payloadInt64(cmd.Payload, "task_id"))
			w.executeScheduledTask(ctx, prompt, runID, taskID)
		}

	case CommandChat:
		if w.State() == StateMonitoring {
			w.mu.Lock()
			w.idleTickCount = 0
			w.mu.Unlock()

			// If the command carries a user message, run a chat-wake with
			// write tools so the brain can fix things the owner reports.
			if msg, ok := cmd.Payload["message"].(string); ok && msg != "" {
				go w.handleChatWake(ctx, msg)
			}
		}

	case CommandShutdown:
		w.logger.Info("shutdown command received")
	}
}

// monitoringInterval returns the current monitoring tick interval with adaptive backoff.
func (w *PipelineWorker) monitoringInterval() time.Duration {
	base := monitoringBaseDefault
	if w.deps.MonitoringBase > 0 {
		base = w.deps.MonitoringBase
	}
	max := monitoringMaxDefault
	if w.deps.MonitoringMax > 0 {
		max = w.deps.MonitoringMax
	}

	w.mu.RLock()
	idle := w.idleTickCount
	w.mu.RUnlock()

	if idle >= idleThreshold {
		return max
	}
	return base
}

// --- LLM execution helpers ---

// runToolLoop executes an LLM tool-call loop: call LLM, execute tool calls,
// repeat until no more tool calls or maxIter reached.
func (w *PipelineWorker) runToolLoop(ctx context.Context, provider llm.Provider, modelID, systemPrompt string, messages []llm.Message, toolDefs []llm.ToolDef, maxIter, maxTokens int) (lastContent, lastModel string, totalTokens, totalToolCalls int, err error) {
	iteration := 0
	llmLogger := llm.NewDBLLMLogger(w.siteDB.Writer())
	loggedProvider := llm.NewLoggedProvider(provider, llmLogger, "brain", "brain", &iteration)

	for i := 0; i < maxIter; i++ {
		iteration = i

		var resp *llm.CompletionResponse
		var callErr error
		for attempt := 0; attempt < 2; attempt++ {
			llmCtx, llmCancel := context.WithTimeout(ctx, llmTimeout)
			resp, callErr = loggedProvider.Complete(llmCtx, llm.CompletionRequest{
				Model:       modelID,
				System:      systemPrompt,
				Messages:    messages,
				Tools:       toolDefs,
				MaxTokens:   maxTokens,
				CacheSystem: true,
			})
			llmCancel()
			if callErr == nil {
				break
			}
			if attempt == 0 && ctx.Err() == nil {
				w.logger.Warn("LLM call failed, retrying", "iteration", i, "error", callErr)
				continue
			}
			break
		}
		if callErr != nil {
			return "", "", totalTokens, totalToolCalls, fmt.Errorf("LLM call failed at iteration %d: %w", i, callErr)
		}

		totalTokens += resp.Usage.InputTokens + resp.Usage.OutputTokens
		lastContent = resp.Content
		lastModel = resp.Model

		// Save and publish assistant message.
		assistantMsg := llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		if resp.Content != "" || len(resp.ToolCalls) > 0 {
			w.saveChatMessage(assistantMsg)
		}
		if resp.Content != "" && w.deps.Bus != nil {
			w.deps.Bus.Publish(events.NewEvent(events.EventBrainMessage, w.siteID, map[string]interface{}{
				"session_id": "brain",
				"role":       "assistant",
				"content":    resp.Content,
			}))
		}

		if len(resp.ToolCalls) == 0 {
			if resp.StopReason == "max_tokens" {
				// LLM was cut off mid-generation. Ask it to continue.
				w.logger.Warn("LLM hit max_tokens, requesting continuation", "iteration", i)
				messages = append(messages, assistantMsg)
				messages = append(messages, llm.Message{
					Role:    llm.RoleUser,
					Content: "Your response was cut off. Call the tool now — output ONLY the tool call, no explanation.",
				})
				continue
			}
			break
		}

		messages = append(messages, assistantMsg)
		messages = w.executeToolCalls(ctx, resp.ToolCalls, messages)
		totalToolCalls += len(resp.ToolCalls)
	}
	return
}

// callLLM makes a single LLM call without tools (used by PLAN stage).
func (w *PipelineWorker) callLLM(ctx context.Context, provider llm.Provider, modelID, systemPrompt, userMessage string, maxTokens int) (string, int, error) {
	llmLogger := llm.NewDBLLMLogger(w.siteDB.Writer())
	iteration := 0
	loggedProvider := llm.NewLoggedProvider(provider, llmLogger, "brain", "brain", &iteration)

	messages := []llm.Message{{Role: llm.RoleUser, Content: userMessage}}

	llmCtx, llmCancel := context.WithTimeout(ctx, llmTimeout)
	defer llmCancel()

	resp, err := loggedProvider.Complete(llmCtx, llm.CompletionRequest{
		Model:     modelID,
		System:    systemPrompt,
		Messages:  messages,
		MaxTokens: maxTokens,
	})
	if err != nil {
		return "", 0, err
	}

	tokens := resp.Usage.InputTokens + resp.Usage.OutputTokens

	// Save assistant response.
	if resp.Content != "" {
		w.saveChatMessage(llm.Message{Role: llm.RoleAssistant, Content: resp.Content})
		if w.deps.Bus != nil {
			w.deps.Bus.Publish(events.NewEvent(events.EventBrainMessage, w.siteID, map[string]interface{}{
				"session_id": "brain",
				"role":       "assistant",
				"content":    resp.Content,
			}))
		}
	}

	return resp.Content, tokens, nil
}

// executeToolCalls runs tool calls and appends results to messages.
func (w *PipelineWorker) executeToolCalls(ctx context.Context, toolCalls []llm.ToolCall, messages []llm.Message) []llm.Message {
	for _, tc := range toolCalls {
		w.logger.Info("tool call", "tool", tc.Name, "call_id", tc.ID)

		if !w.deps.ToolRegistry.Has(tc.Name) {
			w.logger.Warn("unknown tool called by LLM", "tool", tc.Name)
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    fmt.Sprintf(`{"error": "unknown tool '%s' — check available tool names"}`, tc.Name),
				ToolCallID: tc.ID,
			})
			continue
		}

		var args map[string]interface{}
		argBytes := []byte(tc.Arguments)
		// Some LLMs wrap arguments in an array — unwrap single-element arrays.
		if len(argBytes) > 0 && argBytes[0] == '[' {
			var arr []json.RawMessage
			if err := json.Unmarshal(argBytes, &arr); err == nil && len(arr) == 1 {
				argBytes = arr[0]
			}
		}
		if err := json.Unmarshal(argBytes, &args); err != nil {
			w.logger.Warn("bad tool arguments", "tool", tc.Name, "error", err)
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    fmt.Sprintf(`{"error": "invalid JSON arguments for tool %s: %s"}`, tc.Name, err.Error()),
				ToolCallID: tc.ID,
			})
			continue
		}

		if w.deps.Bus != nil {
			w.deps.Bus.Publish(events.NewEvent(events.EventBrainToolStart, w.siteID, map[string]interface{}{
				"tool":    tc.Name,
				"name":    tc.Name,
				"call_id": tc.ID,
				"args":    args,
			}))
		}

		toolCtx := &tools.ToolContext{
			DB:        w.siteDB.Writer(),
			GlobalDB:  w.deps.DB,
			SiteID:    w.siteID,
			Logger:    w.logger,
			Bus:       w.deps.Bus,
			Encryptor: w.deps.Encryptor,
		}

		result, toolErr := w.deps.ToolExecutor.Execute(ctx, toolCtx, tc.Name, args)
		if toolErr != nil {
			w.logger.Error("tool failed", "tool", tc.Name, "error", toolErr)
			result = fmt.Sprintf(`{"error": "tool %s failed: %s"}`, tc.Name, toolErr.Error())
		}

		// In-memory: truncated for token control.
		toolResultMsg := llm.Message{
			Role:       llm.RoleTool,
			Content:    w.truncateToolResult(tc.Name, result),
			ToolCallID: tc.ID,
		}
		messages = append(messages, toolResultMsg)

		// DB: summarized for cross-stage history.
		summaryMsg := llm.Message{
			Role:       llm.RoleTool,
			Content:    w.summarizeToolResult(tc.Name, result),
			ToolCallID: tc.ID,
		}
		w.saveChatMessage(summaryMsg)

		// Publish result event.
		resultPayload := map[string]interface{}{
			"tool":    tc.Name,
			"name":    tc.Name,
			"call_id": tc.ID,
			"args":    args,
		}
		if isInteractiveTool(tc.Name) {
			resultPayload["result"] = result
		} else {
			resultPayload["result"] = truncate(result, 500)
		}
		if w.deps.Bus != nil {
			w.deps.Bus.Publish(events.NewEvent(events.EventBrainToolResult, w.siteID, resultPayload))
		}
	}
	return messages
}

// executeScheduledTask runs a scheduled task with a custom prompt.
func (w *PipelineWorker) executeScheduledTask(ctx context.Context, prompt string, runID int64, taskID int) {
	provider, modelID, err := w.getProvider()
	if err != nil {
		w.logger.Error("scheduled task: provider error", "error", err)
		w.finalizeTaskRun(runID, taskID, false, fmt.Sprintf("provider error: %v", err))
		return
	}

	systemPrompt := buildScheduledTaskPrompt(w.deps.DB, w.siteDB.DB, w.siteID)
	messages := []llm.Message{{Role: llm.RoleUser, Content: prompt}}
	toolDefs := w.deps.ToolRegistry.ToLLMTools()
	start := time.Now()

	lastContent, lastModel, totalTokens, _, iterErr := w.runToolLoop(ctx, provider, modelID, systemPrompt, messages, toolDefs, 20, 2048)
	if iterErr != nil {
		w.finalizeTaskRun(runID, taskID, false, fmt.Sprintf("LLM error: %v", iterErr))
		return
	}

	w.logBrainEvent("scheduled_task", lastContent, prompt, totalTokens, lastModel, time.Since(start).Milliseconds())
	w.finalizeTaskRun(runID, taskID, true, "")
}

// handleChatWake runs a targeted LLM call with write tools in response to
// a user chat message during monitoring. This lets the brain fix issues
// the owner reports without restarting the full pipeline.
func (w *PipelineWorker) handleChatWake(ctx context.Context, userMessage string) {
	start := time.Now()

	// Acquire semaphore to prevent concurrent execution with monitoring ticks.
	select {
	case w.semaphore <- struct{}{}:
		defer func() { <-w.semaphore }()
	case <-ctx.Done():
		return
	}

	provider, modelID, err := w.getProvider()
	if err != nil {
		w.logger.Error("chat-wake: provider error", "error", err)
		return
	}

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	prompt := buildChatWakePrompt(site, w.siteDB.DB, userMessage)
	messages := []llm.Message{{Role: llm.RoleUser, Content: userMessage}}
	toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(toToolSet(chatWakeTools))

	_, lastModel, totalTokens, _, iterErr := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 10, 8192)
	if iterErr != nil {
		w.logger.Error("chat-wake: tool loop error", "error", iterErr)
	}

	w.logBrainEvent("chat_wake", "Responded to owner chat message", userMessage, totalTokens, lastModel, time.Since(start).Milliseconds())
}

// --- Provider resolution ---

func (w *PipelineWorker) getProvider() (llm.Provider, string, error) {
	model, providerRow, err := models.GetModelForSite(w.deps.DB, w.siteID)
	if err != nil {
		w.logger.Warn("site has no valid model, falling back to default", "error", err)
		model, providerRow, err = models.GetDefaultModel(w.deps.DB)
		if err != nil {
			return nil, "", fmt.Errorf("no model configured and no default available: %w", err)
		}
	}

	var apiKey string
	if providerRow.APIKeyEncrypted != nil && *providerRow.APIKeyEncrypted != "" {
		apiKey, err = w.deps.Encryptor.Decrypt(*providerRow.APIKeyEncrypted)
		if err != nil {
			return nil, "", fmt.Errorf("failed to decrypt API key for %q: %w", providerRow.Name, err)
		}
	} else if providerRow.RequiresAPIKey() {
		return nil, "", fmt.Errorf("provider %q has no API key", providerRow.Name)
	}

	var baseURL string
	if providerRow.BaseURL != nil {
		baseURL = *providerRow.BaseURL
	}

	if providerRow.ProviderType == "openai" && baseURL == "" {
		return nil, "", fmt.Errorf("provider %q (openai) has no base_url", providerRow.Name)
	}

	if w.deps.ProviderFactory != nil {
		p := w.deps.ProviderFactory(providerRow.Name, providerRow.ProviderType, apiKey, baseURL)
		if p != nil {
			return p, model.ModelID, nil
		}
	}

	p, err := w.deps.LLMRegistry.Get(providerRow.Name)
	if err != nil {
		return nil, "", fmt.Errorf("provider %q not available: %w", providerRow.Name, err)
	}
	return p, model.ModelID, nil
}

// --- Persistence helpers ---

func (w *PipelineWorker) saveChatMessage(msg llm.Message) {
	var toolCallsJSON *string
	if len(msg.ToolCalls) > 0 {
		data, err := json.Marshal(msg.ToolCalls)
		if err == nil {
			s := string(data)
			toolCallsJSON = &s
		}
	}

	var toolCallID *string
	if msg.ToolCallID != "" {
		toolCallID = &msg.ToolCallID
	}

	_, err := w.siteDB.ExecWrite(
		`INSERT INTO chat_messages (session_id, role, content, tool_calls, tool_call_id) VALUES ('brain', ?, ?, ?, ?)`,
		string(msg.Role), msg.Content, toolCallsJSON, toolCallID,
	)
	if err != nil {
		w.logger.Error("failed to save chat message", "error", err)
	}
}

// saveChatMessageOnce saves a user message only if an identical one hasn't been
// saved recently (within 30 minutes). Prevents duplicate messages on stage retries.
func (w *PipelineWorker) saveChatMessageOnce(msg llm.Message) {
	if msg.Role == llm.RoleUser {
		var exists int
		w.siteDB.Writer().QueryRow(
			"SELECT COUNT(*) FROM chat_messages WHERE session_id = 'brain' AND role = 'user' AND content = ? AND created_at > datetime('now', '-30 minutes')",
			msg.Content,
		).Scan(&exists)
		if exists > 0 {
			return
		}
	}
	w.saveChatMessage(msg)
}

func (w *PipelineWorker) logBrainEvent(eventType, summary, details string, tokens int, model string, durationMs int64) {
	_, err := w.siteDB.ExecWrite(
		"INSERT INTO brain_log (event_type, summary, details, tokens_used, model, duration_ms) VALUES (?, ?, ?, ?, ?, ?)",
		eventType, summary, details, tokens, model, durationMs,
	)
	if err != nil {
		w.logger.Error("failed to log brain event", "error", err)
	}
}

func (w *PipelineWorker) setState(s BrainState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.state = s
}

func (w *PipelineWorker) publishBrainMessage(content string) {
	w.saveChatMessage(llm.Message{Role: llm.RoleAssistant, Content: content})
	if w.deps.Bus != nil {
		w.deps.Bus.Publish(events.NewEvent(events.EventBrainMessage, w.siteID, map[string]interface{}{
			"session_id": "brain",
			"role":       "assistant",
			"content":    content,
		}))
	}
}

func (w *PipelineWorker) publishBrainError(errMsg string) {
	w.publishBrainMessage("**Pipeline Error:** " + errMsg)
}

func (w *PipelineWorker) finalizeTaskRun(runID int64, taskID int, success bool, errMsg string) {
	if runID <= 0 {
		return
	}
	if success {
		w.siteDB.ExecWrite("UPDATE task_runs SET status = 'completed', completed_at = CURRENT_TIMESTAMP WHERE id = ?", runID)
		if taskID > 0 {
			w.siteDB.ExecWrite("UPDATE scheduled_tasks SET run_count = run_count + 1 WHERE id = ?", taskID)
		}
	} else {
		w.siteDB.ExecWrite("UPDATE task_runs SET status = 'failed', error_message = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?", errMsg, runID)
		if taskID > 0 {
			w.siteDB.ExecWrite("UPDATE scheduled_tasks SET error_count = error_count + 1 WHERE id = ?", taskID)
		}
	}
}

// --- Utility functions ---

// isPermanentError returns true for errors that can never succeed on retry,
// such as missing API keys or invalid provider configuration.
func isPermanentError(err error) bool {
	msg := strings.ToLower(err.Error())
	permanentPatterns := []string{
		"no api key", "no model configured", "provider not available",
		"failed to decrypt", "has no api key", "has no base_url",
	}
	for _, p := range permanentPatterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

func isInteractiveTool(name string) bool {
	switch name {
	case "manage_communication", "manage_pages",
		"manage_files", "manage_layout", "manage_schema",
		"manage_data", "manage_endpoints":
		return true
	}
	return false
}

// payloadInt64 extracts an int64 from a payload map, handling int64, float64,
// and int types (JSON numbers deserialize as float64 in Go's interface{}).
func payloadInt64(payload map[string]interface{}, key string) int64 {
	if v, ok := payload[key].(int64); ok {
		return v
	}
	if v, ok := payload[key].(float64); ok {
		return int64(v)
	}
	if v, ok := payload[key].(int); ok {
		return int64(v)
	}
	return 0
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (w *PipelineWorker) truncateToolResult(toolName string, result string) string {
	maxSize := 4000
	if tool, err := w.deps.ToolRegistry.Get(toolName); err == nil {
		if sizer, ok := tool.(tools.ResultSizer); ok {
			maxSize = sizer.MaxResultSize()
		}
	}
	return truncate(result, maxSize)
}

func (w *PipelineWorker) summarizeToolResult(toolName string, result string) string {
	if tool, err := w.deps.ToolRegistry.Get(toolName); err == nil {
		if summarizer, ok := tool.(tools.Summarizer); ok {
			return summarizer.Summarize(result)
		}
	}
	return tools.GenericSummarize(result)
}
