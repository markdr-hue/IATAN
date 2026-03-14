/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/markdr-hue/IATAN/db/models"
	"github.com/markdr-hue/IATAN/events"
	"github.com/markdr-hue/IATAN/llm"
	"github.com/markdr-hue/IATAN/tools"
)

// Derived tool sets — all based on tools.ChatToolSet as the single source of truth.
var (
	buildToolSet     = toolSetExcept(tools.ChatToolSet, "manage_diagnostics", "manage_analytics", "manage_site")
	chatWakeToolSet  = toolSetExcept(tools.ChatToolSet, "manage_diagnostics", "manage_analytics")
	monitoringTools  = map[string]bool{"manage_diagnostics": true, "manage_analytics": true, "manage_communication": true}
	fullWriteToolSet = tools.ChatToolSet
)

func toolSetExcept(base map[string]bool, exclude ...string) map[string]bool {
	ex := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		ex[e] = true
	}
	m := make(map[string]bool, len(base))
	for n := range base {
		if !ex[n] {
			m[n] = true
		}
	}
	return m
}

// stageRun tracks timing and logging for a pipeline stage.
type stageRun struct {
	w     *PipelineWorker
	start time.Time
	logID int64
	stage PipelineStage
}

func (w *PipelineWorker) beginStage(stage PipelineStage) *stageRun {
	logID, _ := LogStageStart(w.siteDB, stage)
	return &stageRun{w: w, start: time.Now(), logID: logID, stage: stage}
}

func (sr *stageRun) fail(err error) (PipelineStage, error) {
	LogStageError(sr.w.siteDB, sr.logID, err.Error())
	return sr.stage, err
}

func (sr *stageRun) complete(tokens, toolCalls int) {
	LogStageComplete(sr.w.siteDB, sr.logID, tokens, 0, toolCalls, time.Since(sr.start))
}

// --- PLAN stage ---

func (w *PipelineWorker) runPlan(ctx context.Context) (PipelineStage, error) {
	sr := w.beginStage(StagePlan)

	provider, modelID, err := w.getProvider()
	if err != nil {
		return sr.fail(err)
	}

	site, err := models.GetSiteByID(w.deps.DB, w.siteID)
	if err != nil {
		return sr.fail(err)
	}

	// Check for answered questions (wake context).
	var answers string
	w.mu.RLock()
	if w.wakeContext != nil {
		if a, ok := w.wakeContext["answer"].(string); ok {
			answers = a
		}
	}
	w.mu.RUnlock()

	capRef := w.deps.ToolRegistry.BuildGuide(fullWriteToolSet)
	prompt := buildPlanPrompt(site, w.ownerName(), answers, capRef)
	userMsg := "Analyze the site requirements and produce a complete Plan JSON."
	if answers != "" {
		userMsg = fmt.Sprintf("The owner answered your questions: %q\n\nNow produce a complete Plan JSON.", answers)
	}

	w.saveChatMessageOnce(llm.Message{Role: llm.RoleUser, Content: userMsg})

	messages := []llm.Message{{Role: llm.RoleUser, Content: userMsg}}
	content, _, tokens, _, err := w.runToolLoop(ctx, provider, modelID, prompt, messages, nil, 1, 16384)
	if err != nil {
		return sr.fail(err)
	}

	plan, err := ParsePlan(content)
	if err != nil {
		w.logger.Warn("plan JSON parse failed, retrying", "error", err)
		w.publishBrainMessage("Plan response wasn't valid JSON, retrying...")
		retryUser := fmt.Sprintf("Your previous response could not be parsed. Error: %s\nRespond with ONLY a raw JSON object. No markdown code fences, no explanation text.", err.Error())
		retryMsgs := []llm.Message{{Role: llm.RoleUser, Content: retryUser}}
		retryContent, _, tokens2, _, err2 := w.runToolLoop(ctx, provider, modelID, prompt, retryMsgs, nil, 1, 16384)
		tokens += tokens2
		if err2 != nil {
			return sr.fail(err2)
		}
		plan, err = ParsePlan(retryContent)
		if err != nil {
			return sr.fail(fmt.Errorf("plan JSON still invalid after retry: %w", err))
		}
	}

	// Handle questions.
	if len(plan.Questions) > 0 {
		w.logger.Info("plan has questions, pausing for owner answers", "count", len(plan.Questions))
		// Mark stale pending questions as superseded.
		if _, err := w.siteDB.ExecWrite("UPDATE questions SET status = 'superseded' WHERE status = 'pending'"); err != nil {
			w.logger.Warn("failed to supersede old questions", "error", err)
		}
		for _, q := range plan.Questions {
			opts := "[]"
			if len(q.Options) > 0 {
				if b, err := json.Marshal(q.Options); err == nil {
					opts = string(b)
				}
			}
			qType := q.Type
			if qType == "" {
				qType = "open"
			}
			qResult, _ := w.siteDB.ExecWrite(
				"INSERT INTO questions (question, urgency, status, options, type) VALUES (?, 'normal', 'pending', ?, ?)",
				q.Question, opts, qType,
			)
			qID, _ := qResult.LastInsertId()
			if w.deps.Bus != nil {
				w.deps.Bus.Publish(events.NewEvent(events.EventQuestionAsked, w.siteID, map[string]interface{}{
					"id":       qID,
					"question": q.Question,
					"options":  q.Options,
					"type":     qType,
				}))
			}
		}
		PausePipeline(w.siteDB, PauseReasonOwnerAnswers)
		sr.complete(tokens, 0)
		return StagePlan, fmt.Errorf("paused: awaiting owner answers")
	}

	if errs := ValidatePlan(plan); len(errs) > 0 {
		return sr.fail(fmt.Errorf("plan validation errors: %s", strings.Join(errs, "; ")))
	}

	// Save plan to pipeline state.
	planJSON, _ := marshalToJSON(plan)
	w.siteDB.ExecWrite("UPDATE pipeline_state SET plan_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1", planJSON)

	// Store architecture in site config for public handler.
	configJSON := fmt.Sprintf(`{"architecture":"%s"}`, plan.Architecture)
	w.deps.DB.Exec("UPDATE sites SET config = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", configJSON, w.siteID)

	w.publishBrainMessage(fmt.Sprintf("Plan ready: %s (%s), %d pages, %d endpoints, %d tables",
		plan.AppType, plan.Architecture, len(plan.Pages), len(plan.Endpoints), len(plan.Tables)))
	sr.complete(tokens, 0)

	// Clear wake context after successful plan.
	w.mu.Lock()
	w.wakeContext = nil
	w.mu.Unlock()

	return StageBuild, nil
}

// --- BUILD stage ---
// Single continuous LLM tool-calling session that builds everything:
// schema, endpoints, CSS, layout, and all pages in one conversation.

func (w *PipelineWorker) runBuild(ctx context.Context) (PipelineStage, error) {
	sr := w.beginStage(StageBuild)

	provider, modelID, err := w.getProvider()
	if err != nil {
		return sr.fail(err)
	}

	plan, err := w.loadPlan()
	if err != nil {
		return sr.fail(err)
	}

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	db := w.siteDB.DB

	// Check for crash recovery: what already exists?
	existingManifest := buildCrashRecoveryManifest(db)

	toolGuide := w.deps.ToolRegistry.BuildGuide(buildToolSet)
	prompt := buildBuildPrompt(plan, site, w.ownerName(), existingManifest, toolGuide)

	userMsg := "Build this site from the plan. Follow the build order in the system prompt. When everything is built, stop."
	if existingManifest != "" {
		userMsg = "Resume building. Items already built are listed above under 'Already Built'. Build only the remaining items from the plan."
	}

	w.publishBrainMessage(fmt.Sprintf("Building %s: %d pages, %d endpoints, %d tables...",
		plan.AppType, len(plan.Pages), len(plan.Endpoints), len(plan.Tables)))

	messages := []llm.Message{{Role: llm.RoleUser, Content: userMsg}}
	toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(buildToolSet)
	_, _, totalTokens, totalToolCalls, loopErr := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 50, 16384)

	if loopErr != nil {
		return sr.fail(loopErr)
	}

	w.publishBrainMessage(fmt.Sprintf("Build complete: %d tool calls, %s", totalToolCalls, time.Since(sr.start).Round(time.Second)))
	sr.complete(totalTokens, totalToolCalls)

	return StageComplete, nil
}

// --- COMPLETE stage ---

func (w *PipelineWorker) runComplete(ctx context.Context) (PipelineStage, error) {
	sr := w.beginStage(StageComplete)

	w.logBrainEvent("complete", "Site build completed", "", 0, "", 0)
	w.publishBrainMessage("Site build complete! Switching to monitoring mode.")

	w.deps.DB.Exec("UPDATE sites SET mode = 'monitoring', updated_at = CURRENT_TIMESTAMP WHERE id = ?", w.siteID)
	if w.deps.Bus != nil {
		w.deps.Bus.Publish(events.NewEvent(events.EventBrainModeChanged, w.siteID, map[string]interface{}{
			"site_id": w.siteID,
			"mode":    "monitoring",
		}))
	}

	sr.complete(0, 0)
	return StageMonitoring, nil
}

// --- UPDATE_PLAN stage (incremental) ---

func (w *PipelineWorker) runUpdatePlan(ctx context.Context) (PipelineStage, error) {
	sr := w.beginStage(StageUpdatePlan)

	provider, modelID, err := w.getProvider()
	if err != nil {
		return sr.fail(err)
	}

	existingPlan, _ := w.loadPlan()

	state, _ := LoadPipelineState(w.siteDB.DB)
	changeDesc := ""
	if state != nil {
		changeDesc = state.UpdateDescription
	}

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	capRef := w.deps.ToolRegistry.BuildGuide(buildToolSet)
	prompt := buildUpdatePlanPrompt(existingPlan, site, changeDesc, w.ownerName(), capRef)
	userMsg := "Create a PlanPatch JSON describing the changes needed."
	if changeDesc != "" {
		userMsg = fmt.Sprintf("The owner requested: %s\n\nCreate a PlanPatch JSON describing the changes needed.", changeDesc)
	}
	w.saveChatMessageOnce(llm.Message{Role: llm.RoleUser, Content: userMsg})

	messages := []llm.Message{{Role: llm.RoleUser, Content: userMsg}}
	content, _, tokens, _, err := w.runToolLoop(ctx, provider, modelID, prompt, messages, nil, 1, 4096)
	if err != nil {
		return sr.fail(err)
	}

	patch, err := ParsePlanPatch(content)
	if err != nil {
		w.logger.Warn("plan patch JSON parse failed, retrying", "error", err)
		w.publishBrainMessage("Patch response wasn't valid JSON, retrying...")
		retryUser := fmt.Sprintf("Your previous response could not be parsed. Error: %s\nRespond with ONLY a raw JSON object. No markdown code fences, no explanation text.", err.Error())
		retryMsgs := []llm.Message{{Role: llm.RoleUser, Content: retryUser}}
		retryContent, _, tokens2, _, err2 := w.runToolLoop(ctx, provider, modelID, prompt, retryMsgs, nil, 1, 4096)
		tokens += tokens2
		if err2 != nil {
			return sr.fail(err2)
		}
		patch, err = ParsePlanPatch(retryContent)
		if err != nil {
			return sr.fail(fmt.Errorf("patch JSON still invalid after retry: %w", err))
		}
	}

	if existingPlan == nil {
		return sr.fail(fmt.Errorf("cannot apply patch: no existing plan found"))
	}

	w.siteDB.ExecWrite("UPDATE pipeline_state SET update_description = NULL WHERE id = 1")

	// Mark modified pages as deleted so BUILD recreates them.
	for _, mod := range patch.ModifyPages {
		w.siteDB.ExecWrite("UPDATE pages SET is_deleted = 1 WHERE path = ? AND is_deleted = 0", mod.Path)
	}

	existingPlan.ApplyPatch(patch)

	planJSON, _ := marshalToJSON(existingPlan)
	w.siteDB.ExecWrite("UPDATE pipeline_state SET plan_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1", planJSON)

	sr.complete(tokens, 0)
	return StageBuild, nil
}

// --- Monitoring tick ---

func (w *PipelineWorker) monitoringTick(ctx context.Context) {
	start := time.Now()

	select {
	case w.semaphore <- struct{}{}:
		defer func() { <-w.semaphore }()
	case <-ctx.Done():
		return
	}

	var recentErrors int
	w.siteDB.DB.QueryRow("SELECT COUNT(*) FROM brain_log WHERE event_type = 'error' AND created_at > datetime('now', '-1 hour')").Scan(&recentErrors)

	if recentErrors == 0 {
		w.mu.Lock()
		w.idleCheckCount++
		w.mu.Unlock()
		w.logBrainEvent("tick", "Monitoring: healthy", "", 0, "", time.Since(start).Milliseconds())
		return
	}

	// Issues found — reset idle counter.
	w.mu.Lock()
	w.idleCheckCount = 0
	w.mu.Unlock()

	provider, modelID, err := w.getProvider()
	if err != nil {
		w.logger.Error("monitoring: provider error", "error", err)
		return
	}

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	plan, _ := w.loadPlan()
	prompt := buildMonitoringPrompt(site, w.siteDB.DB, plan, w.ownerName())
	var contextMsg strings.Builder
	contextMsg.WriteString("Check site health. Issues detected:\n")
	contextMsg.WriteString(fmt.Sprintf("- %d recent errors in the last hour\n", recentErrors))

	messages := []llm.Message{{Role: llm.RoleUser, Content: contextMsg.String()}}
	toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(monitoringTools)

	_, lastModel, totalTokens, _, _ := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 5, 2048)

	w.logBrainEvent("tick", "Monitoring: investigated issues", "", totalTokens, lastModel, time.Since(start).Milliseconds())
}

// --- Helper methods ---

func (w *PipelineWorker) loadPlan() (*Plan, error) {
	state, err := LoadPipelineState(w.siteDB.DB)
	if err != nil {
		return nil, err
	}
	if state.PlanJSON == "" {
		return nil, fmt.Errorf("no plan found in pipeline state")
	}
	return ParsePlan(state.PlanJSON)
}

func (w *PipelineWorker) ownerName() string {
	var name string
	w.deps.DB.QueryRow("SELECT display_name FROM users WHERE role = 'admin' ORDER BY id LIMIT 1").Scan(&name)
	if name != "" {
		name = strings.ReplaceAll(name, "\n", " ")
		name = strings.ReplaceAll(name, "\r", "")
		if runes := []rune(name); len(runes) > 50 {
			name = string(runes[:50])
		}
	}
	return name
}
