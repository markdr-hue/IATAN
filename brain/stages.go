/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package brain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/markdr-hue/IATAN/db/models"
	"github.com/markdr-hue/IATAN/events"
	"github.com/markdr-hue/IATAN/llm"
)

// Tool sets per stage.
var (
	// BUILD: all construction tools in one session.
	buildTools = []string{
		"manage_schema",
		"manage_endpoints",
		"manage_data",
		"manage_files",
		"manage_layout",
		"manage_pages",

		"manage_communication",
		"manage_secrets",
		"manage_providers",
		"manage_email",
		"manage_payments",
		"manage_webhooks",
		"manage_scheduler",
		"make_http_request",
	}

	// Monitoring is read-only.
	monitoringToolSet = []string{
		"manage_diagnostics",
		"manage_analytics",
		"manage_communication",

	}

	// chatWakeTools gives write access when owner sends a message during monitoring.
	chatWakeTools = []string{
		"manage_pages",
		"manage_files",
		"manage_layout",
		"manage_data",
		"manage_endpoints",
		"manage_schema",
		"manage_diagnostics",
		"manage_analytics",

		"manage_communication",
		"manage_scheduler",
		"manage_site",
		"manage_secrets",
		"manage_providers",
		"manage_email",
		"manage_payments",
		"manage_webhooks",
		"make_http_request",
	}

	// scheduledTaskTools: same as chatWakeTools — tasks need write access but not all tools.
	scheduledTaskTools = chatWakeTools
)

func toToolSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// --- ANALYZE stage ---
// Takes the user's description and maps it to platform capabilities.

func (w *PipelineWorker) runAnalyze(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageAnalyze)

	provider, modelID, err := w.getProvider()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageAnalyze, err
	}

	site, err := models.GetSiteByID(w.deps.DB, w.siteID)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageAnalyze, err
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

	prompt := buildAnalyzePrompt(site, w.ownerName(), answers)
	userMsg := "Analyze the site requirements and produce an Analysis JSON."
	if answers != "" {
		userMsg = fmt.Sprintf("The owner answered your questions: %q\n\nNow analyze the site requirements and produce an Analysis JSON.", answers)
	}

	w.saveChatMessageOnce(llm.Message{Role: llm.RoleUser, Content: userMsg})

	content, tokens, err := w.callLLM(ctx, provider, modelID, prompt, userMsg, 4096)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageAnalyze, err
	}

	analysis, err := ParseAnalysis(content)
	if err != nil {
		// Retry with stricter prompt.
		w.logger.Warn("analysis JSON parse failed, retrying", "error", err)
		w.publishBrainMessage("Analysis response wasn't valid JSON, retrying...")
		retryUser := fmt.Sprintf("Your previous response could not be parsed. Error: %s\nRespond with ONLY a raw JSON object. No markdown code fences, no explanation text.", err.Error())
		content, tokens2, err2 := w.callLLM(ctx, provider, modelID, prompt, retryUser, 4096)
		tokens += tokens2
		if err2 != nil {
			LogStageError(w.siteDB, logID, err2.Error())
			return StageAnalyze, err2
		}
		analysis, err = ParseAnalysis(content)
		if err != nil {
			LogStageError(w.siteDB, logID, err.Error())
			return StageAnalyze, fmt.Errorf("analysis JSON still invalid after retry: %w", err)
		}
	}

	// Handle questions.
	if len(analysis.Questions) > 0 {
		w.logger.Info("analysis has questions, pausing for owner answers", "count", len(analysis.Questions))
		// Mark any stale pending questions from previous attempts as superseded.
		if _, err := w.siteDB.ExecWrite("UPDATE questions SET status = 'superseded' WHERE status = 'pending'"); err != nil {
			w.logger.Warn("failed to supersede old questions", "error", err)
		}
		for _, q := range analysis.Questions {
			opts := ""
			if len(q.Options) > 0 {
				opts = strings.Join(q.Options, ", ")
			}
			qResult, _ := w.siteDB.ExecWrite(
				"INSERT INTO questions (question, urgency, status, options) VALUES (?, 'normal', 'pending', ?)",
				q.Question, opts,
			)
			qID, _ := qResult.LastInsertId()
			if w.deps.Bus != nil {
				w.deps.Bus.Publish(events.NewEvent(events.EventQuestionAsked, w.siteID, map[string]interface{}{
					"id":       qID,
					"question": q.Question,
					"options":  q.Options,
				}))
			}
		}
		PausePipeline(w.siteDB, PauseReasonOwnerAnswers)
		LogStageComplete(w.siteDB, logID, tokens, 0, 0, time.Since(start))
		return StageAnalyze, fmt.Errorf("paused: awaiting owner answers")
	}

	// Validate analysis.
	if errs := ValidateAnalysis(analysis); len(errs) > 0 {
		errMsg := "Analysis validation errors: " + strings.Join(errs, "; ")
		w.logger.Warn("analysis validation failed", "errors", errs)
		LogStageError(w.siteDB, logID, errMsg)
		return StageAnalyze, fmt.Errorf("%s", errMsg)
	}

	// Save analysis to pipeline state (plan_json column).
	analysisJSON, _ := marshalToJSON(analysis)
	w.siteDB.ExecWrite("UPDATE pipeline_state SET plan_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1", analysisJSON)

	w.publishBrainMessage(fmt.Sprintf("Analysis complete: %s (%s), auth: %s", analysis.AppType, analysis.DesignMood, analysis.AuthStrategy))
	LogStageComplete(w.siteDB, logID, tokens, 0, 0, time.Since(start))

	// Clear wake context after successful analysis.
	w.mu.Lock()
	w.wakeContext = nil
	w.mu.Unlock()

	return StageBlueprint, nil
}

// --- BLUEPRINT stage ---
// Transforms Analysis into a full build specification.

func (w *PipelineWorker) runBlueprint(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageBlueprint)

	provider, modelID, err := w.getProvider()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageBlueprint, err
	}

	// Load analysis from pipeline state.
	state, err := LoadPipelineState(w.siteDB.DB)
	if err != nil || state.AnalysisJSON == "" {
		msg := "no analysis found in pipeline state"
		if err != nil {
			msg = err.Error()
		}
		LogStageError(w.siteDB, logID, msg)
		return StageBlueprint, fmt.Errorf("%s", msg)
	}

	analysis, err := ParseAnalysis(state.AnalysisJSON)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageBlueprint, err
	}

	// Check for answered questions (wake context), same as ANALYZE.
	var answers string
	w.mu.RLock()
	if w.wakeContext != nil {
		if a, ok := w.wakeContext["answer"].(string); ok {
			answers = a
		}
	}
	w.mu.RUnlock()

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	prompt := buildBlueprintPrompt(analysis, site)
	userMsg := "Create a complete Blueprint JSON from the analysis."
	if answers != "" {
		userMsg = fmt.Sprintf("The owner answered your questions: %q\n\nNow create a complete Blueprint JSON from the analysis.", answers)
	}

	w.saveChatMessageOnce(llm.Message{Role: llm.RoleUser, Content: userMsg})

	content, tokens, err := w.callLLM(ctx, provider, modelID, prompt, userMsg, 8192)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageBlueprint, err
	}

	bp, err := ParseBlueprint(content)
	if err != nil {
		// Retry.
		w.logger.Warn("blueprint JSON parse failed, retrying", "error", err)
		w.publishBrainMessage("Blueprint response wasn't valid JSON, retrying...")
		retryUser := fmt.Sprintf("Your previous response could not be parsed. Error: %s\nRespond with ONLY a raw JSON object. No markdown code fences, no explanation text.", err.Error())
		content, tokens2, err2 := w.callLLM(ctx, provider, modelID, prompt, retryUser, 8192)
		tokens += tokens2
		if err2 != nil {
			LogStageError(w.siteDB, logID, err2.Error())
			return StageBlueprint, err2
		}
		bp, err = ParseBlueprint(content)
		if err != nil {
			LogStageError(w.siteDB, logID, err.Error())
			return StageBlueprint, fmt.Errorf("blueprint JSON still invalid after retry: %w", err)
		}
	}

	// Handle questions (unlikely at this stage but supported).
	if len(bp.Questions) > 0 {
		for _, q := range bp.Questions {
			opts := ""
			if len(q.Options) > 0 {
				opts = strings.Join(q.Options, ", ")
			}
			qResult, _ := w.siteDB.ExecWrite(
				"INSERT INTO questions (question, urgency, status, options) VALUES (?, 'normal', 'pending', ?)",
				q.Question, opts,
			)
			qID, _ := qResult.LastInsertId()
			if w.deps.Bus != nil {
				w.deps.Bus.Publish(events.NewEvent(events.EventQuestionAsked, w.siteID, map[string]interface{}{
					"id":       qID,
					"question": q.Question,
					"options":  q.Options,
				}))
			}
		}
		PausePipeline(w.siteDB, PauseReasonOwnerAnswers)
		LogStageComplete(w.siteDB, logID, tokens, 0, 0, time.Since(start))
		return StageBlueprint, fmt.Errorf("paused: awaiting owner answers")
	}

	// Validate blueprint.
	if errs := ValidateBlueprint(bp); len(errs) > 0 {
		errMsg := "Blueprint validation errors: " + strings.Join(errs, "; ")
		w.logger.Warn("blueprint validation failed", "errors", errs)
		LogStageError(w.siteDB, logID, errMsg)
		return StageBlueprint, fmt.Errorf("%s", errMsg)
	}

	// Save blueprint to pipeline state (blueprint_json column).
	bpJSON, _ := marshalToJSON(bp)
	w.siteDB.ExecWrite("UPDATE pipeline_state SET blueprint_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1", bpJSON)

	// Store architecture in site config for public handler.
	configJSON := fmt.Sprintf(`{"architecture":"%s"}`, bp.Architecture)
	w.deps.DB.Exec("UPDATE sites SET config = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", configJSON, w.siteID)

	w.publishBrainMessage(fmt.Sprintf("Blueprint ready: %d pages, %d endpoints, %d tables, %s",
		len(bp.Pages), len(bp.Endpoints), len(bp.DataTables), bp.Architecture))
	LogStageComplete(w.siteDB, logID, tokens, 0, 0, time.Since(start))

	// Clear wake context after successful blueprint.
	w.mu.Lock()
	w.wakeContext = nil
	w.mu.Unlock()

	return StageBuild, nil
}

// --- BUILD stage ---
// One long tool loop session that creates everything: data layer, CSS, layouts, pages.

func (w *PipelineWorker) runBuild(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageBuild)

	provider, modelID, err := w.getProvider()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageBuild, err
	}

	bp, err := w.loadBlueprint()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageBuild, err
	}

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)

	totalTokens := 0
	totalToolCalls := 0

	// Single-phase build: all tools available, LLM decides the order.
	prompt := buildBuildPrompt(bp, site)
	userMsg := "Build the complete site following the Blueprint. Execute all steps in order."
	w.saveChatMessageOnce(llm.Message{Role: llm.RoleUser, Content: userMsg})
	messages := []llm.Message{{Role: llm.RoleUser, Content: userMsg}}
	toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(toToolSet(buildTools))
	_, _, t, c, loopErr := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 50, 32768)
	totalTokens += t
	totalToolCalls += c
	if loopErr != nil {
		LogStageError(w.siteDB, logID, loopErr.Error())
		return StageBuild, loopErr
	}

	// Post-build: check all blueprint items were created.
	allToolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(toToolSet(buildTools))
	missing := validateBlueprintConformance(w.siteDB.Writer(), bp)
	if len(missing) > 0 {
		w.publishBrainMessage(fmt.Sprintf("Post-build check: %d missing items to fix...", len(missing)))
		w.logger.Warn("build missing blueprint items, running fixup", "issues", missing)
		for attempt := 0; attempt < 3 && len(missing) > 0; attempt++ {
			fixPrompt := buildPostBuildFixupPrompt(missing, bp, "blueprint")
			fixMessages := []llm.Message{{Role: llm.RoleUser, Content: "The build phase completed but these items are missing. Create ONLY the missing items:\n- " + strings.Join(missing, "\n- ")}}
			_, _, fixTokens, fixCalls, _ := w.runToolLoop(ctx, provider, modelID, fixPrompt, fixMessages, allToolDefs, 10, 8192)
			totalTokens += fixTokens
			totalToolCalls += fixCalls
			missing = validateBlueprintConformance(w.siteDB.Writer(), bp)
		}
	}

	w.publishBrainMessage(fmt.Sprintf("Build complete: %d tool calls", totalToolCalls))
	LogStageComplete(w.siteDB, logID, totalTokens, 0, totalToolCalls, time.Since(start))

	return StageValidate, nil
}

// --- VALIDATE stage ---
// Blueprint conformance check: are all planned items built?

func (w *PipelineWorker) runValidate(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageValidate)

	bp, _ := w.loadBlueprint()
	if bp == nil {
		w.publishBrainMessage("Validation skipped — no blueprint found.")
		LogStageComplete(w.siteDB, logID, 0, 0, 0, time.Since(start))
		return StageComplete, nil
	}

	totalTokens := 0
	totalToolCalls := 0

	// Phase 1: Structural conformance — are all blueprint items created?
	missing := validateBlueprintConformance(w.siteDB.Writer(), bp)
	if len(missing) > 0 {
		w.publishBrainMessage(fmt.Sprintf("Validation: %d missing items, attempting to create...", len(missing)))

		provider, modelID, err := w.getProvider()
		if err != nil {
			LogStageError(w.siteDB, logID, err.Error())
			return StageValidate, err
		}

		toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(toToolSet(buildTools))
		site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
		prompt := buildValidateFixupPrompt(missing, bp, site)
		messages := []llm.Message{{Role: llm.RoleUser, Content: "These blueprint items were not created during the build. Create them now:\n- " + strings.Join(missing, "\n- ")}}
		_, _, tokens, calls, _ := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 15, 16384)
		totalTokens += tokens
		totalToolCalls += calls

		remaining := validateBlueprintConformance(w.siteDB.Writer(), bp)
		if len(remaining) > 0 {
			w.publishBrainMessage(fmt.Sprintf("Validation: %d items still missing after fix attempt.", len(remaining)))
		} else {
			w.publishBrainMessage("Validation passed — all missing items created.")
		}
	} else {
		w.publishBrainMessage("Validation passed — all blueprint items created.")
	}

	// Phase 2: Quality checks — empty pages, missing assets, broken links, no CSS.
	qualityIssues := validatePageQuality(w.siteDB.Writer(), bp)
	if len(qualityIssues) > 0 {
		w.publishBrainMessage(fmt.Sprintf("Quality check: %d issues found, fixing...", len(qualityIssues)))

		provider, modelID, err := w.getProvider()
		if err != nil {
			LogStageError(w.siteDB, logID, err.Error())
			return StageValidate, err
		}

		toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(toToolSet(buildTools))
		site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
		prompt := buildQualityFixupPrompt(qualityIssues, bp, site)
		messages := []llm.Message{{Role: llm.RoleUser, Content: "Fix these quality issues:\n- " + strings.Join(qualityIssues, "\n- ")}}
		_, _, tokens, calls, _ := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 15, 16384)
		totalTokens += tokens
		totalToolCalls += calls

		remaining := validatePageQuality(w.siteDB.Writer(), bp)
		if len(remaining) > 0 {
			w.publishBrainMessage(fmt.Sprintf("Quality check: %d issues remain after fix attempt.", len(remaining)))
		} else {
			w.publishBrainMessage("Quality check passed.")
		}
	}

	LogStageComplete(w.siteDB, logID, totalTokens, 0, totalToolCalls, time.Since(start))
	return StageComplete, nil
}

// --- COMPLETE stage (unchanged) ---

func (w *PipelineWorker) runComplete(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageComplete)

	w.logBrainEvent("complete", "Site build completed", "", 0, "", 0)
	w.publishBrainMessage("Site build complete! Switching to monitoring mode.")

	if _, err := w.deps.DB.Exec("UPDATE sites SET mode = 'monitoring', updated_at = CURRENT_TIMESTAMP WHERE id = ?", w.siteID); err != nil {
		w.logger.Error("failed to update site mode to monitoring", "error", err)
	}
	if w.deps.Bus != nil {
		w.deps.Bus.Publish(events.NewEvent(events.EventBrainModeChanged, w.siteID, map[string]interface{}{
			"site_id": w.siteID,
			"mode":    "monitoring",
		}))
	}

	LogStageComplete(w.siteDB, logID, 0, 0, 0, time.Since(start))
	return StageMonitoring, nil
}

// --- UPDATE_BLUEPRINT stage (incremental) ---

func (w *PipelineWorker) runUpdateBlueprint(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageUpdateBlueprint)

	provider, modelID, err := w.getProvider()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageUpdateBlueprint, err
	}

	existingBP, _ := w.loadBlueprint()

	state, _ := LoadPipelineState(w.siteDB.DB)
	changeDesc := ""
	if state != nil {
		changeDesc = state.UpdateDescription
	}

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	prompt := buildUpdateBlueprintPrompt(existingBP, site, changeDesc)
	userMsg := "Create a BlueprintPatch JSON describing the changes needed."
	if changeDesc != "" {
		userMsg = fmt.Sprintf("The owner requested: %s\n\nCreate a BlueprintPatch JSON describing the changes needed.", changeDesc)
	}
	w.saveChatMessageOnce(llm.Message{Role: llm.RoleUser, Content: userMsg})

	content, tokens, err := w.callLLM(ctx, provider, modelID, prompt, userMsg, 4096)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageUpdateBlueprint, err
	}

	patch, err := ParseBlueprintPatch(content)
	if err != nil {
		w.logger.Warn("blueprint patch JSON parse failed, retrying", "error", err)
		w.publishBrainMessage("Patch response wasn't valid JSON, retrying...")
		retryUser := fmt.Sprintf("Your previous response could not be parsed. Error: %s\nRespond with ONLY a raw JSON object. No markdown code fences, no explanation text.", err.Error())
		content, tokens2, err2 := w.callLLM(ctx, provider, modelID, prompt, retryUser, 4096)
		tokens += tokens2
		if err2 != nil {
			LogStageError(w.siteDB, logID, err2.Error())
			return StageUpdateBlueprint, err2
		}
		patch, err = ParseBlueprintPatch(content)
		if err != nil {
			LogStageError(w.siteDB, logID, err.Error())
			return StageUpdateBlueprint, fmt.Errorf("patch JSON still invalid after retry: %w", err)
		}
	}

	if existingBP == nil {
		LogStageError(w.siteDB, logID, "cannot apply patch: no existing blueprint found")
		return StageUpdateBlueprint, fmt.Errorf("cannot apply patch: no existing blueprint found")
	}

	// Clear update_description early so it doesn't persist if the patch fails.
	w.siteDB.ExecWrite("UPDATE pipeline_state SET update_description = NULL WHERE id = 1")

	// Validate patch: check for duplicate paths and missing references.
	existingPaths := make(map[string]bool, len(existingBP.Pages))
	for _, p := range existingBP.Pages {
		existingPaths[p.Path] = true
	}
	for _, ap := range patch.AddPages {
		if existingPaths[ap.Path] {
			w.logger.Warn("blueprint patch: add_pages path already exists, will overwrite", "path", ap.Path)
		}
	}
	for _, rm := range patch.RemovePages {
		if !existingPaths[rm] {
			w.logger.Warn("blueprint patch: remove_pages references non-existent path", "path", rm)
		}
	}
	for _, mod := range patch.ModifyPages {
		if !existingPaths[mod.Path] {
			w.logger.Warn("blueprint patch: modify_pages references non-existent path", "path", mod.Path)
		}
	}

	// Validate patch against exclusions.
	if len(existingBP.Exclusions) > 0 {
		exclusionSet := strings.Join(existingBP.Exclusions, " ")
		var filtered []EndpointSpec
		for _, ep := range patch.AddEndpoints {
			if ep.Action == "create_auth" && strings.Contains(exclusionSet, "no auth") {
				w.logger.Warn("blueprint patch: skipping create_auth endpoint — conflicts with exclusion", "path", ep.Path)
				continue
			}
			filtered = append(filtered, ep)
		}
		patch.AddEndpoints = filtered
	}

	// Apply patch.
	existingBP.Pages = append(existingBP.Pages, patch.AddPages...)

	for _, mod := range patch.ModifyPages {
		for i, p := range existingBP.Pages {
			if p.Path == mod.Path {
				existingBP.Pages[i] = mod
				w.siteDB.ExecWrite("UPDATE pages SET is_deleted = 1 WHERE path = ? AND is_deleted = 0", mod.Path)
				break
			}
		}
	}

	for _, rm := range patch.RemovePages {
		for i, p := range existingBP.Pages {
			if p.Path == rm {
				existingBP.Pages = append(existingBP.Pages[:i], existingBP.Pages[i+1:]...)
				break
			}
		}
	}

	existingBP.Endpoints = append(existingBP.Endpoints, patch.AddEndpoints...)
	existingBP.DataTables = append(existingBP.DataTables, patch.AddTables...)
	existingBP.Webhooks = append(existingBP.Webhooks, patch.AddWebhooks...)
	existingBP.ScheduledTasks = append(existingBP.ScheduledTasks, patch.AddTasks...)

	// Save updated blueprint.
	bpJSON, _ := marshalToJSON(existingBP)
	w.siteDB.ExecWrite("UPDATE pipeline_state SET blueprint_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1", bpJSON)

	LogStageComplete(w.siteDB, logID, tokens, 0, 0, time.Since(start))

	// Always go to BUILD for incremental updates.
	return StageBuild, nil
}

// --- Monitoring tick (unchanged) ---

func (w *PipelineWorker) monitoringTick(ctx context.Context) {
	start := time.Now()

	select {
	case w.semaphore <- struct{}{}:
		defer func() { <-w.semaphore }()
	case <-ctx.Done():
		return
	}

	var recentErrors int
	w.siteDB.Writer().QueryRow("SELECT COUNT(*) FROM brain_log WHERE event_type = 'error' AND created_at > datetime('now', '-1 hour')").Scan(&recentErrors)

	if recentErrors == 0 {
		w.mu.Lock()
		w.idleCheckCount++
		w.mu.Unlock()
		w.logBrainEvent("tick", "Monitoring: healthy", "", 0, "", time.Since(start).Milliseconds())
		return
	}

	// Issues found — reset idle counter so monitoring stays at base interval.
	w.mu.Lock()
	w.idleCheckCount = 0
	w.mu.Unlock()

	provider, modelID, err := w.getProvider()
	if err != nil {
		w.logger.Error("monitoring: provider error", "error", err)
		return
	}

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	bp, _ := w.loadBlueprint()
	prompt := buildMonitoringPrompt(site, w.siteDB.DB, bp)
	var contextMsg strings.Builder
	contextMsg.WriteString("Check site health. Issues detected:\n")
	contextMsg.WriteString(fmt.Sprintf("- %d recent errors in the last hour\n", recentErrors))

	messages := []llm.Message{{Role: llm.RoleUser, Content: contextMsg.String()}}
	toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(toToolSet(monitoringToolSet))

	_, lastModel, totalTokens, _, _ := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 5, 2048)

	w.logBrainEvent("tick", "Monitoring: investigated issues", "", totalTokens, lastModel, time.Since(start).Milliseconds())
}

// --- Helper methods ---

func (w *PipelineWorker) loadBlueprint() (*Blueprint, error) {
	state, err := LoadPipelineState(w.siteDB.DB)
	if err != nil {
		return nil, err
	}
	if state.BlueprintJSON == "" {
		return nil, fmt.Errorf("no blueprint found in pipeline state")
	}
	return ParseBlueprint(state.BlueprintJSON)
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

