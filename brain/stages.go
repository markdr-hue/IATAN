/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package brain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/markdr-hue/IATAN/db/models"
	"github.com/markdr-hue/IATAN/events"
	"github.com/markdr-hue/IATAN/llm"
)

// Tool sets per stage — only the tools needed for each stage.
var (
	designTools = []string{
		"manage_files",
		"manage_layout",
		"manage_memory",
		"manage_communication",
		"make_http_request",
	}

	dataLayerTools = []string{
		"manage_schema",
		"manage_endpoints",
		"manage_data",
		"manage_secrets",
		"manage_providers",
		"manage_email",
		"manage_payments",
		"manage_webhooks",
		"manage_memory",
		"manage_communication",
	}

	buildPageTools = []string{
		"manage_pages",
		"manage_files",
		"manage_memory",
		"manage_data",
	}

	reviewTools = []string{
		"manage_pages",
		"manage_layout",
		"manage_files",
		"manage_communication",
		"manage_data",
	}

	// Monitoring is read-only — diagnose issues, don't fix them.
	// Write-capable tools (manage_pages, manage_layout, etc.) are
	// intentionally excluded so the LLM can't enter a rebuild loop.
	// Real fixes should go through the UPDATE_PLAN pipeline.
	monitoringToolSet = []string{
		"manage_diagnostics",
		"manage_analytics",
		"manage_communication",
		"manage_memory",
	}

	// chatWakeTools gives the brain write access when the owner sends a
	// chat message during monitoring. Includes page/file/layout/data tools
	// for targeted fixes, but excludes schema/endpoint tools to prevent
	// accidental rebuild loops.
	chatWakeTools = []string{
		"manage_pages",
		"manage_files",
		"manage_layout",
		"manage_data",
		"manage_diagnostics",
		"manage_memory",
		"manage_communication",
		"manage_scheduler",
		"manage_site",
		"make_http_request",
	}
)

func toToolSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// --- PLAN stage ---

func (w *PipelineWorker) runPlan(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StagePlan)

	provider, modelID, err := w.getProvider()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StagePlan, err
	}

	site, err := models.GetSiteByID(w.deps.DB, w.siteID)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StagePlan, err
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

	prompt := buildPlanPrompt(site, w.ownerName(), answers)
	userMsg := "Create a complete site plan as JSON."
	if answers != "" {
		userMsg = fmt.Sprintf("The owner answered your questions: %q\n\nNow create a complete site plan as JSON.", answers)
	}

	// Save user message to chat (dedup on stage retry).
	w.saveChatMessageOnce(llm.Message{Role: llm.RoleUser, Content: userMsg})

	content, tokens, err := w.callLLM(ctx, provider, modelID, prompt, userMsg, 4096)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StagePlan, err
	}

	plan, err := ParseSitePlan(content)
	if err != nil {
		// Retry with stricter prompt including the actual parse error.
		w.logger.Warn("plan JSON parse failed, retrying", "error", err)
		retryPrompt := prompt + fmt.Sprintf("\n\nCRITICAL: Your previous response was not valid JSON. Parse error: %s\nRespond with ONLY a JSON object, no markdown, no explanation.", err.Error())
		content, tokens2, err2 := w.callLLM(ctx, provider, modelID, retryPrompt, "Respond with ONLY the JSON site plan.", 4096)
		tokens += tokens2
		if err2 != nil {
			LogStageError(w.siteDB, logID, err2.Error())
			return StagePlan, err2
		}
		plan, err = ParseSitePlan(content)
		if err != nil {
			LogStageError(w.siteDB, logID, err.Error())
			return StagePlan, fmt.Errorf("plan JSON still invalid after retry: %w", err)
		}
	}

	// Handle questions.
	if len(plan.Questions) > 0 {
		w.logger.Info("plan has questions, pausing for owner answers", "count", len(plan.Questions))
		for _, q := range plan.Questions {
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
		return StagePlan, fmt.Errorf("paused: awaiting owner answers")
	}

	// Validate plan.
	if errs := ValidatePlan(plan); len(errs) > 0 {
		errMsg := "Plan validation errors: " + strings.Join(errs, "; ")
		w.logger.Warn("plan validation failed", "errors", errs)
		LogStageError(w.siteDB, logID, errMsg)
		return StagePlan, fmt.Errorf("%s", errMsg)
	}

	// Save plan to pipeline state.
	planJSON, _ := MarshalPlan(plan)
	w.siteDB.ExecWrite("UPDATE pipeline_state SET plan_json = ?, current_page_index = 0, updated_at = CURRENT_TIMESTAMP WHERE id = 1", planJSON)

	// Store architecture in site config so the public handler knows whether to inject SPA runtime.
	configJSON := fmt.Sprintf(`{"architecture":"%s"}`, plan.Architecture)
	w.deps.DB.Exec("UPDATE sites SET config = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", configJSON, w.siteID)

	w.publishBrainMessage(fmt.Sprintf("Plan created: %d pages, architecture: %s", len(plan.Pages), plan.Architecture))
	LogStageComplete(w.siteDB, logID, tokens, 0, 0, time.Since(start))

	// Clear wake context after successful plan.
	w.mu.Lock()
	w.wakeContext = nil
	w.mu.Unlock()

	return StageDesign, nil
}

// --- DESIGN stage ---

func (w *PipelineWorker) runDesign(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageDesign)

	plan, err := w.loadPlan()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageDesign, err
	}

	provider, modelID, err := w.getProvider()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageDesign, err
	}

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	prompt := buildDesignPrompt(plan, site)
	messages := []llm.Message{{Role: llm.RoleUser, Content: "Create the design system, layout, and shared assets based on the plan."}}
	w.saveChatMessageOnce(messages[0])

	toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(toToolSet(designTools))

	_, lastModel, totalTokens, toolCalls, err := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 12, 16384)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageDesign, err
	}

	// Validate design output — run one targeted fix attempt before failing.
	issues := validateDesign(w.siteDB.Writer(), plan)
	if len(issues) > 0 {
		w.logger.Warn("design validation issues, attempting fix", "issues", issues)
		fixPrompt := "Fix these design issues:\n"
		for _, iss := range issues {
			fixPrompt += "- " + iss + "\n"
		}
		fixMsgs := []llm.Message{{Role: llm.RoleUser, Content: fixPrompt}}
		_, _, fixTokens, fixCalls, fixErr := w.runToolLoop(ctx, provider, modelID, prompt, fixMsgs, toolDefs, 6, 4096)
		totalTokens += fixTokens
		toolCalls += fixCalls
		if fixErr == nil {
			issues = validateDesign(w.siteDB.Writer(), plan)
		}
		if len(issues) > 0 {
			LogStageError(w.siteDB, logID, strings.Join(issues, "; "))
			return StageDesign, fmt.Errorf("design validation: %s", strings.Join(issues, "; "))
		}
	}

	w.publishBrainMessage("Design system created successfully.")
	LogStageComplete(w.siteDB, logID, totalTokens, 0, toolCalls, time.Since(start))
	_ = lastModel

	// Route: if site needs data, create tables/endpoints before pages.
	if plan.NeedsDataLayer {
		return StageDataLayer, nil
	}
	return StageBuildPages, nil
}

// --- DATA_LAYER stage ---

func (w *PipelineWorker) runDataLayer(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageDataLayer)

	plan, err := w.loadPlan()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageDataLayer, err
	}

	if !plan.NeedsDataLayer || len(plan.DataTables) == 0 {
		LogStageComplete(w.siteDB, logID, 0, 0, 0, time.Since(start))
		return StageBuildPages, nil
	}

	provider, modelID, err := w.getProvider()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageDataLayer, err
	}

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	prompt := buildDataLayerPrompt(plan, site)
	messages := []llm.Message{{Role: llm.RoleUser, Content: "Create the data tables, API endpoints, and seed data based on the plan."}}
	w.saveChatMessageOnce(messages[0])

	toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(toToolSet(dataLayerTools))

	_, _, totalTokens, toolCalls, err := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 10, 4096)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageDataLayer, err
	}

	// Validate and fix-up loop: if items are missing, give the LLM a
	// targeted message to fix just those items instead of retrying the
	// entire stage from scratch.
	for fixAttempt := 0; fixAttempt < 3; fixAttempt++ {
		issues := validateDataLayer(w.siteDB.Writer(), plan)
		if len(issues) == 0 {
			break
		}
		if fixAttempt == 2 {
			LogStageError(w.siteDB, logID, strings.Join(issues, "; "))
			return StageDataLayer, fmt.Errorf("data layer validation: %s", strings.Join(issues, "; "))
		}
		w.logger.Warn("data layer fix-up needed", "attempt", fixAttempt+1, "issues", issues)

		fixPrompt := buildDataLayerFixupPrompt(issues, plan)
		fixMessages := []llm.Message{{Role: llm.RoleUser, Content: "Fix the missing data layer items."}}

		_, _, fixTokens, fixCalls, fixErr := w.runToolLoop(ctx, provider, modelID, fixPrompt, fixMessages, toolDefs, 5, 4096)
		totalTokens += fixTokens
		toolCalls += fixCalls
		if fixErr != nil {
			w.logger.Warn("fixup attempt failed, will retry", "attempt", fixAttempt+1, "error", fixErr)
			continue // let the next attempt try rather than failing the stage
		}
	}

	w.publishBrainMessage("Data layer created successfully.")
	LogStageComplete(w.siteDB, logID, totalTokens, 0, toolCalls, time.Since(start))
	return StageBuildPages, nil
}

// --- BUILD_PAGES stage ---

func (w *PipelineWorker) runBuildPages(ctx context.Context) (PipelineStage, error) {
	plan, err := w.loadPlan()
	if err != nil {
		return StageBuildPages, err
	}

	// Check current_page_index for incremental builds (set by UPDATE_PLAN).
	// Pages before this index are already built; only new/modified pages
	// need building. Modified pages were soft-deleted by UPDATE_PLAN so
	// the "already built" check below will correctly rebuild them.
	state, _ := LoadPipelineState(w.siteDB.DB)
	startIdx := 0
	if state != nil && state.CurrentPageIndex > 0 && state.CurrentPageIndex < len(plan.Pages) {
		startIdx = state.CurrentPageIndex
		w.logger.Info("incremental build", "start_index", startIdx, "total_pages", len(plan.Pages))
	}

	// Collect all page paths for link context.
	allPaths := make([]string, len(plan.Pages))
	for i, p := range plan.Pages {
		allPaths[i] = p.Path
	}

	// Pre-load shared read-only context ONCE (avoids redundant disk I/O).
	layoutSummary := w.getLayoutSummary()
	cssContent := w.getGlobalCSS()
	cssClassMap := extractCSSClassMap(cssContent)

	// Load structured API contract (generated from DATA_LAYER output).
	apiContract := w.getAPIContract()
	authContract := w.getAuthEndpointContract()

	// Load site description for content context.
	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	var siteDescription string
	if site != nil && site.Description != nil {
		siteDescription = *site.Description
	}

	// Build homepage first to establish tone, then build remaining pages.
	// This lets us collect warnings and content terms from the homepage.
	var previousWarnings []string
	var contentTerms []string
	var remainingPages []PagePlan

	// Determine which pages to build. For incremental builds, only process
	// new pages (at index >= startIdx) plus any earlier pages that were
	// soft-deleted (modified pages needing rebuild).
	pagesToBuild := plan.Pages
	if startIdx > 0 {
		pagesToBuild = plan.Pages[startIdx:]
		// Also check if any pages before startIdx need rebuilding
		// (soft-deleted by UPDATE_PLAN for modifications).
		for _, page := range plan.Pages[:startIdx] {
			var exists int
			w.siteDB.Writer().QueryRow("SELECT COUNT(*) FROM pages WHERE path = ? AND is_deleted = 0", page.Path).Scan(&exists)
			if exists == 0 {
				pagesToBuild = append(pagesToBuild, page)
			}
		}
	}

	for _, page := range pagesToBuild {
		if page.Path == "/" {
			// Skip if already built (crash recovery).
			var exists int
			w.siteDB.Writer().QueryRow("SELECT COUNT(*) FROM pages WHERE path = '/' AND is_deleted = 0").Scan(&exists)
			if exists > 0 {
				w.logger.Info("homepage already exists, skipping", "path", "/")
			} else {
				if err := w.buildSinglePage(ctx, plan, page, allPaths, layoutSummary, cssClassMap, siteDescription, apiContract, authContract, nil, nil); err != nil {
					return StageBuildPages, err
				}
				w.publishBrainMessage(fmt.Sprintf("Built page: **%s** (%s)", page.Title, page.Path))
			}
			// Collect warnings and content terms from homepage for other pages.
			previousWarnings = w.collectPageWarnings("/")
			contentTerms = w.extractContentTerms("/", siteDescription)
		} else {
			remainingPages = append(remainingPages, page)
		}
	}

	// For incremental builds, always collect homepage context if the homepage
	// wasn't in pagesToBuild (it already exists and wasn't modified).
	if startIdx > 0 && len(previousWarnings) == 0 {
		previousWarnings = w.collectPageWarnings("/")
		contentTerms = w.extractContentTerms("/", siteDescription)
	}

	// Build remaining pages sequentially. Each page benefits from accumulated
	// content terms and warnings from all prior pages, ensuring coherence.
	builtPaths := []string{"/"}
	for _, page := range remainingPages {
		if ctx.Err() != nil {
			break
		}

		// Skip already-built pages (crash recovery).
		var exists int
		w.siteDB.Writer().QueryRow("SELECT COUNT(*) FROM pages WHERE path = ? AND is_deleted = 0", page.Path).Scan(&exists)
		if exists > 0 {
			w.logger.Info("page already exists, skipping", "path", page.Path)
			builtPaths = append(builtPaths, page.Path)
			continue
		}

		// Accumulate context from all previously built pages.
		contentTerms = w.extractContentTermsFromAll(builtPaths, siteDescription)
		previousWarnings = w.collectWarningsFromAll(builtPaths)

		err := w.buildSinglePage(ctx, plan, page, allPaths, layoutSummary, cssClassMap, siteDescription, apiContract, authContract, contentTerms, previousWarnings)
		if err != nil {
			w.logger.Error("page build failed", "path", page.Path, "error", err)
			var created int
			w.siteDB.Writer().QueryRow("SELECT COUNT(*) FROM pages WHERE path = ? AND is_deleted = 0", page.Path).Scan(&created)
			if created == 0 {
				return StageBuildPages, err
			}
			// Page exists but had errors — REVIEW validators will catch quality issues.
			w.logger.Warn("page created with errors, REVIEW will validate", "path", page.Path, "error", err)
		}
		w.publishBrainMessage(fmt.Sprintf("Built page: **%s** (%s)", page.Title, page.Path))
		builtPaths = append(builtPaths, page.Path)
	}

	// Missing pages (if any) are caught by the REVIEW stage.
	return StageReview, nil
}

func (w *PipelineWorker) buildSinglePage(ctx context.Context, plan *SitePlan, page PagePlan, allPaths []string, layoutSummary, cssClassMap, siteDescription, apiContract, authContract string, contentTerms, previousWarnings []string) error {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageBuildPages)

	provider, modelID, err := w.getProvider()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return err
	}

	// List available SVG assets for page content.
	svgAssets := w.getSVGAssetList()

	prompt := buildPagePrompt(page, plan, allPaths, layoutSummary, cssClassMap, siteDescription, apiContract, authContract, svgAssets, contentTerms, previousWarnings)
	messages := []llm.Message{{Role: llm.RoleUser, Content: fmt.Sprintf("Build the page: %s (%s)", page.Title, page.Path)}}
	w.saveChatMessageOnce(messages[0])

	toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(toToolSet(buildPageTools))

	_, _, totalTokens, toolCalls, err := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 8, 16384)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return err
	}

	// Verify the page was actually saved by the LLM.
	var saved int
	w.siteDB.Writer().QueryRow("SELECT COUNT(*) FROM pages WHERE path = ? AND is_deleted = 0", page.Path).Scan(&saved)
	if saved == 0 {
		errMsg := fmt.Sprintf("page %s was not saved by LLM after tool loop", page.Path)
		LogStageError(w.siteDB, logID, errMsg)
		return fmt.Errorf("%s", errMsg)
	}

	LogStageComplete(w.siteDB, logID, totalTokens, 0, toolCalls, time.Since(start))
	return nil
}

// --- REVIEW stage ---

func (w *PipelineWorker) runReview(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageReview)

	// Pre-compute CSS class map for both rebuild and review prompt.
	cssClassMap := extractCSSClassMap(w.getGlobalCSS())

	// First: rebuild any missing planned pages (Go-driven, no LLM guessing).
	plan, _ := w.loadPlan()
	if plan != nil {
		allPaths := make([]string, len(plan.Pages))
		for i, p := range plan.Pages {
			allPaths[i] = p.Path
		}
		layoutSummary := w.getLayoutSummary()

		site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
		var siteDescription string
		if site != nil && site.Description != nil {
			siteDescription = *site.Description
		}

		for _, page := range plan.Pages {
			var exists int
			// Use writer pool so we see pages created during BUILD_PAGES (WAL visibility).
			w.siteDB.Writer().QueryRow("SELECT COUNT(*) FROM pages WHERE path = ? AND is_deleted = 0", page.Path).Scan(&exists)
			if exists == 0 {
				w.logger.Warn("review: rebuilding missing planned page", "path", page.Path)
				w.publishBrainMessage(fmt.Sprintf("Rebuilding missing page: %s", page.Path))
				apiContract := w.getAPIContract()
				authContract := w.getAuthEndpointContract()
				if err := w.buildSinglePage(ctx, plan, page, allPaths, layoutSummary, cssClassMap, siteDescription, apiContract, authContract, nil, nil); err != nil {
					w.logger.Error("review: failed to rebuild page", "path", page.Path, "error", err)
				}
			}
		}
	}

	// Run Go-based validation (zero tokens) with a multi-pass fix loop.
	// Track previous issues to skip recurring ones that the LLM can't fix.
	issues := validateSite(w.siteDB.DB)
	issues = append(issues, validateColorContrast(plan)...)
	totalTokens := 0
	var previousIssues map[string]bool

	for fixAttempt := 0; fixAttempt < 3; fixAttempt++ {
		if len(issues) == 0 {
			break
		}

		// Filter out issues that were already seen in the previous attempt
		// (the LLM tried and failed to fix them — don't retry).
		if previousIssues != nil {
			var newIssues []string
			for _, iss := range issues {
				if !previousIssues[iss] {
					newIssues = append(newIssues, iss)
				}
			}
			issues = newIssues
			if len(issues) == 0 {
				break
			}
		}

		if fixAttempt == 2 {
			// Final attempt still has issues — log and proceed (don't block deployment).
			w.publishBrainMessage(fmt.Sprintf("Review: %d issues remain after %d fix attempts: %s", len(issues), fixAttempt+1, strings.Join(issues, "; ")))
			break
		}

		// Track current issues for deduplication on next pass.
		previousIssues = make(map[string]bool, len(issues))
		for _, iss := range issues {
			previousIssues[iss] = true
		}

		w.publishBrainMessage(fmt.Sprintf("Review found %d issues (attempt %d/3), fixing...", len(issues), fixAttempt+1))

		provider, modelID, err := w.getProvider()
		if err != nil {
			break
		}

		prompt := buildReviewPrompt(issues, w.siteDB.DB, plan, cssClassMap)
		messages := []llm.Message{{Role: llm.RoleUser, Content: "Fix the following issues:\n" + strings.Join(issues, "\n")}}
		if fixAttempt == 0 {
			w.saveChatMessageOnce(messages[0])
		}

		toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(toToolSet(reviewTools))
		_, _, tokens, _, _ := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 8, 8192)
		totalTokens += tokens

		// Re-validate after fixes (include color contrast check).
		issues = validateSite(w.siteDB.DB)
		issues = append(issues, validateColorContrast(plan)...)
	}

	if len(issues) == 0 {
		w.publishBrainMessage("Review passed: all validation checks OK.")
	}

	LogStageComplete(w.siteDB, logID, totalTokens, 0, 0, time.Since(start))
	return StageComplete, nil
}

// --- COMPLETE stage ---

func (w *PipelineWorker) runComplete(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageComplete)

	// Log completion.
	w.logBrainEvent("complete", "Site build completed", "", 0, "", 0)
	w.publishBrainMessage("Site build complete! Switching to monitoring mode.")

	// Update site mode to monitoring.
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

// --- UPDATE_PLAN stage (incremental) ---

func (w *PipelineWorker) runUpdatePlan(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageUpdatePlan)

	provider, modelID, err := w.getProvider()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageUpdatePlan, err
	}

	// Load existing plan and update description for context.
	existingPlan, _ := w.loadPlan()

	// Load the change description stored by CommandUpdate.
	state, _ := LoadPipelineState(w.siteDB.DB)
	changeDesc := ""
	if state != nil {
		changeDesc = state.UpdateDescription
	}

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	prompt := buildUpdatePlanPrompt(existingPlan, site, changeDesc)
	userMsg := "Create a PlanPatch JSON describing the changes needed."
	if changeDesc != "" {
		userMsg = fmt.Sprintf("The owner requested: %s\n\nCreate a PlanPatch JSON describing the changes needed.", changeDesc)
	}
	w.saveChatMessageOnce(llm.Message{Role: llm.RoleUser, Content: userMsg})

	content, tokens, err := w.callLLM(ctx, provider, modelID, prompt, userMsg, 4096)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageUpdatePlan, err
	}

	patch, err := ParsePlanPatch(content)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageUpdatePlan, fmt.Errorf("patch JSON invalid: %w", err)
	}

	// Apply patch to existing plan.
	if existingPlan == nil {
		LogStageError(w.siteDB, logID, "cannot apply patch: no existing plan found")
		return StageUpdatePlan, fmt.Errorf("cannot apply patch: no existing plan found")
	}

	// Add new pages.
	existingPlan.Pages = append(existingPlan.Pages, patch.AddPages...)

	// Modify existing pages: update the plan and soft-delete the old
	// page so BUILD_PAGES will rebuild it with the new spec.
	for _, mod := range patch.ModifyPages {
		for i, p := range existingPlan.Pages {
			if p.Path == mod.Path {
				existingPlan.Pages[i] = mod
				w.siteDB.ExecWrite("UPDATE pages SET is_deleted = 1 WHERE path = ? AND is_deleted = 0", mod.Path)
				break
			}
		}
	}

	// Remove pages.
	for _, rm := range patch.RemovePages {
		for i, p := range existingPlan.Pages {
			if p.Path == rm {
				existingPlan.Pages = append(existingPlan.Pages[:i], existingPlan.Pages[i+1:]...)
				break
			}
		}
	}

	// Add new tables.
	existingPlan.DataTables = append(existingPlan.DataTables, patch.AddTables...)
	if len(patch.AddTables) > 0 {
		existingPlan.NeedsDataLayer = true
	}

	// Save updated plan.
	planJSON, _ := MarshalPlan(existingPlan)
	w.siteDB.ExecWrite("UPDATE pipeline_state SET plan_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1", planJSON)

	// Set page index to start building only new/modified pages.
	// Find index of first new page.
	newStartIdx := len(existingPlan.Pages) - len(patch.AddPages)
	w.siteDB.ExecWrite("UPDATE pipeline_state SET current_page_index = ? WHERE id = 1", newStartIdx)

	// Clear the update description now that it's been consumed.
	w.siteDB.ExecWrite("UPDATE pipeline_state SET update_description = NULL WHERE id = 1")

	LogStageComplete(w.siteDB, logID, tokens, 0, 0, time.Since(start))

	// Route to appropriate next stage.
	if patch.UpdateCSS {
		return StageDesign, nil
	}
	if len(patch.AddTables) > 0 {
		return StageDataLayer, nil
	}
	return StageBuildPages, nil
}

// --- Monitoring tick ---

func (w *PipelineWorker) monitoringTick(ctx context.Context) {
	start := time.Now()

	// Acquire semaphore.
	select {
	case w.semaphore <- struct{}{}:
		defer func() { <-w.semaphore }()
	case <-ctx.Done():
		return
	}

	// Run Go-based health check first.
	issues := validateSite(w.siteDB.Writer())

	// Check for recent errors.
	var recentErrors int
	w.siteDB.Writer().QueryRow("SELECT COUNT(*) FROM brain_log WHERE event_type = 'error' AND created_at > datetime('now', '-1 hour')").Scan(&recentErrors)

	// If no issues and no errors, this is an idle tick.
	if len(issues) == 0 && recentErrors == 0 {
		w.mu.Lock()
		w.idleTickCount++
		w.mu.Unlock()
		w.logBrainEvent("tick", "Monitoring: healthy", "", 0, "", time.Since(start).Milliseconds())
		return
	}

	// Issues detected — run LLM monitoring tick.
	provider, modelID, err := w.getProvider()
	if err != nil {
		w.logger.Error("monitoring: provider error", "error", err)
		return
	}

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	prompt := buildMonitoringPrompt(site, w.siteDB.DB)
	var contextMsg strings.Builder
	contextMsg.WriteString("Check site health. Issues detected:\n")
	for _, issue := range issues {
		contextMsg.WriteString("- " + issue + "\n")
	}
	if recentErrors > 0 {
		contextMsg.WriteString(fmt.Sprintf("- %d recent errors in the last hour\n", recentErrors))
	}

	messages := []llm.Message{{Role: llm.RoleUser, Content: contextMsg.String()}}
	toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(toToolSet(monitoringToolSet))

	_, lastModel, totalTokens, _, _ := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 5, 2048)

	// Don't reset idleTickCount here — only external input (chat, commands)
	// should reset it. This lets adaptive backoff work: after 3 idle ticks
	// the interval increases from 5min to 15min.

	w.logBrainEvent("tick", "Monitoring: investigated issues", "", totalTokens, lastModel, time.Since(start).Milliseconds())
}

// --- Helper methods ---

func (w *PipelineWorker) loadPlan() (*SitePlan, error) {
	state, err := LoadPipelineState(w.siteDB.DB)
	if err != nil {
		return nil, err
	}
	if state.PlanJSON == "" {
		return nil, fmt.Errorf("no plan found in pipeline state")
	}
	return ParseSitePlan(state.PlanJSON)
}

func (w *PipelineWorker) ownerName() string {
	var name string
	w.deps.DB.QueryRow("SELECT display_name FROM users WHERE role = 'admin' ORDER BY id LIMIT 1").Scan(&name)
	if name != "" {
		name = strings.ReplaceAll(name, "\n", " ")
		name = strings.ReplaceAll(name, "\r", "")
		if len(name) > 50 {
			name = name[:50]
		}
	}
	return name
}

// getSVGAssetList returns a formatted list of available SVG assets for page prompts.
func (w *PipelineWorker) getSVGAssetList() string {
	rows, err := w.siteDB.Writer().Query("SELECT filename FROM assets WHERE filename LIKE '%.svg'")
	if err != nil {
		return ""
	}
	defer rows.Close()

	var svgs []string
	for rows.Next() {
		var filename string
		if rows.Scan(&filename) == nil {
			svgs = append(svgs, "/assets/"+filename)
		}
	}
	if len(svgs) == 0 {
		return ""
	}

	var b strings.Builder
	for _, svg := range svgs {
		b.WriteString("- " + svg + "\n")
	}
	return b.String()
}

// headingContentRe extracts text content from h1 and h2 tags.
var headingContentRe = regexp.MustCompile(`(?i)<h[12][^>]*>(.*?)</h[12]>`)
// extractContentTerms reads a built page and extracts key terms (headings,
// taglines) for content consistency across pages.
func (w *PipelineWorker) extractContentTerms(pagePath, siteDescription string) []string {
	var content sql.NullString
	w.siteDB.Writer().QueryRow("SELECT content FROM pages WHERE path = ? AND is_deleted = 0", pagePath).Scan(&content)
	if !content.Valid || content.String == "" {
		return nil
	}

	seen := make(map[string]bool)
	var terms []string

	// Extract headings (h1, h2) as key content terms.
	for _, m := range headingContentRe.FindAllStringSubmatch(content.String, 5) {
		if len(m) > 1 {
			text := strings.TrimSpace(htmlTagStripRe.ReplaceAllString(m[1], ""))
			if text != "" && len(text) > 2 && len(text) < 80 && !seen[text] {
				seen[text] = true
				terms = append(terms, text)
			}
		}
	}

	// Cap at 5 terms to avoid prompt bloat.
	if len(terms) > 5 {
		terms = terms[:5]
	}

	return terms
}

// collectPageWarnings reads a built page from DB and runs basic validation
// to collect warnings that can be passed to subsequent page builds.
func (w *PipelineWorker) collectPageWarnings(pagePath string) []string {
	var content, layout sql.NullString
	w.siteDB.Writer().QueryRow("SELECT content, layout FROM pages WHERE path = ? AND is_deleted = 0", pagePath).Scan(&content, &layout)
	if !content.Valid || content.String == "" {
		return nil
	}

	var warnings []string
	lower := strings.ToLower(content.String)

	// Skip nav/footer warnings for layout="none" pages (standalone pages with own nav).
	isNoneLayout := layout.Valid && layout.String == "none"
	if !isNoneLayout {
		if strings.Contains(lower, "<nav") {
			warnings = append(warnings, "Do NOT include <nav> in pages — navigation belongs in the layout")
		}
		if strings.Contains(lower, "<footer") {
			warnings = append(warnings, "Do NOT include <footer> in pages — footer belongs in the layout")
		}
	}
	if strings.Contains(lower, "style=\"") {
		warnings = append(warnings, "Do NOT use inline styles — use CSS classes from the global stylesheet")
	}
	if strings.Contains(lower, "<style>") || strings.Contains(lower, "<style ") {
		warnings = append(warnings, "Do NOT include <style> blocks — add styles to the shared CSS file instead")
	}
	if strings.Contains(lower, `rel="stylesheet"`) && strings.Contains(lower, "/assets/") {
		warnings = append(warnings, "Do NOT add <link> tags for shared CSS — they are auto-injected by the server")
	}

	return warnings
}

// extractContentTermsFromAll collects content terms from all built pages for context propagation.
func (w *PipelineWorker) extractContentTermsFromAll(builtPaths []string, siteDescription string) []string {
	seen := make(map[string]bool)
	var terms []string
	for _, path := range builtPaths {
		for _, term := range w.extractContentTerms(path, siteDescription) {
			if !seen[term] {
				seen[term] = true
				terms = append(terms, term)
			}
		}
	}
	// Cap at 10 terms from all pages to keep prompt compact.
	if len(terms) > 10 {
		terms = terms[:10]
	}
	return terms
}

// collectWarningsFromAll aggregates warnings from all built pages, deduplicating.
func (w *PipelineWorker) collectWarningsFromAll(builtPaths []string) []string {
	seen := make(map[string]bool)
	var warnings []string
	for _, path := range builtPaths {
		for _, warn := range w.collectPageWarnings(path) {
			if !seen[warn] {
				seen[warn] = true
				warnings = append(warnings, warn)
			}
		}
	}
	return warnings
}

func (w *PipelineWorker) getLayoutSummary() string {
	var before, after sql.NullString
	w.siteDB.Writer().QueryRow("SELECT body_before_main, body_after_main FROM layouts WHERE name = 'default'").Scan(&before, &after)
	if !before.Valid && !after.Valid {
		return "No layout created yet."
	}
	var b strings.Builder
	b.WriteString("The server wraps page content with this layout. Pages only need <main> content.\n\n")
	if before.Valid && before.String != "" {
		b.WriteString("### Before <main> (nav):\n```html\n")
		b.WriteString(before.String)
		b.WriteString("\n```\n")
	}
	if after.Valid && after.String != "" {
		b.WriteString("### After <main> (footer):\n```html\n")
		b.WriteString(after.String)
		b.WriteString("\n```\n")
	}
	return b.String()
}

// getGlobalCSS reads the global CSS file content from disk.
func (w *PipelineWorker) getGlobalCSS() string {
	var storagePath sql.NullString
	w.siteDB.Writer().QueryRow(
		"SELECT storage_path FROM assets WHERE scope = 'global' AND filename LIKE '%.css' ORDER BY filename LIMIT 1",
	).Scan(&storagePath)
	if !storagePath.Valid || storagePath.String == "" {
		return ""
	}
	data, err := os.ReadFile(storagePath.String)
	if err != nil {
		w.logger.Warn("failed to read global CSS", "path", storagePath.String, "error", err)
		return ""
	}
	return string(data)
}

// getAPIContract builds a structured API reference from all endpoint tables.
// This replaces the raw schema dump with an accurate, compact contract that
// BUILD_PAGES uses for JS-API coherence.
func (w *PipelineWorker) getAPIContract() string {
	// Use the read pool (4 connections) — DATA_LAYER committed all writes
	// before this runs, so WAL reads see the latest data.
	db := w.siteDB.DB
	var b strings.Builder

	// --- CRUD API endpoints ---
	apiRows, err := db.Query(`SELECT e.path, e.table_name, e.methods, e.public_columns, e.requires_auth, e.public_read, e.required_role, t.schema_def
		FROM api_endpoints e
		LEFT JOIN dynamic_tables t ON e.table_name = t.table_name
		ORDER BY e.path`)
	if err == nil {
		for apiRows.Next() {
			var path, tableName string
			var methods, publicCols, requiredRole, schemaDef sql.NullString
			var requiresAuth, publicRead bool
			apiRows.Scan(&path, &tableName, &methods, &publicCols, &requiresAuth, &publicRead, &requiredRole, &schemaDef)

			methodList := "GET, POST"
			if methods.Valid && methods.String != "" {
				methodList = strings.Trim(methods.String, "[]\"")
				methodList = strings.ReplaceAll(methodList, "\"", "")
			}
			b.WriteString(fmt.Sprintf("%s /api/%s", methodList, path))
			if requiresAuth {
				b.WriteString(" [AUTH]")
			}
			if publicRead {
				b.WriteString(" [PUBLIC_READ]")
			}
			if requiredRole.Valid && requiredRole.String != "" {
				b.WriteString(fmt.Sprintf(" [ROLE: %s]", requiredRole.String))
			}
			b.WriteString("\n")

			// Show POST body fields from schema (excluding id, created_at, PASSWORD cols).
			if schemaDef.Valid && schemaDef.String != "" {
				var cols map[string]string
				if json.Unmarshal([]byte(schemaDef.String), &cols) == nil {
					var postFields []string
					for col, typ := range cols {
						if col == "id" || col == "created_at" || strings.EqualFold(typ, "PASSWORD") {
							continue
						}
						postFields = append(postFields, fmt.Sprintf("%s: %s", col, typ))
					}
					sort.Strings(postFields)
					if len(postFields) > 0 {
						b.WriteString(fmt.Sprintf("  POST body: {%s}\n", strings.Join(postFields, ", ")))
					}
				}
			}

			// Show which columns GET returns.
			if publicCols.Valid && publicCols.String != "" && publicCols.String != "[]" {
				b.WriteString(fmt.Sprintf("  GET returns: %s\n", publicCols.String))
			}
			b.WriteString("\n")
		}
		apiRows.Close()
	}

	// --- Auth endpoints ---
	authRows, err := db.Query(`SELECT ae.path, ae.table_name, ae.username_column, ae.password_column,
		ae.default_role, ae.role_column, dt.schema_def
		FROM auth_endpoints ae
		LEFT JOIN dynamic_tables dt ON ae.table_name = dt.table_name`)
	if err == nil {
		for authRows.Next() {
			var path, tableName, usernameCol, passwordCol, defaultRole, roleCol string
			var schemaDef sql.NullString
			authRows.Scan(&path, &tableName, &usernameCol, &passwordCol, &defaultRole, &roleCol, &schemaDef)

			// Build list of optional registration fields from schema
			// (exclude id, created_at, username, password, role columns).
			var optionalFields []string
			if schemaDef.Valid && schemaDef.String != "" {
				var cols map[string]string
				if json.Unmarshal([]byte(schemaDef.String), &cols) == nil {
					for col, typ := range cols {
						if col == "id" || col == "created_at" || col == usernameCol ||
							col == passwordCol || col == roleCol ||
							strings.EqualFold(typ, "PASSWORD") {
							continue
						}
						optionalFields = append(optionalFields, fmt.Sprintf("\"%s\": \"...\"", col))
					}
					sort.Strings(optionalFields)
				}
			}

			// Show base path prominently so LLM passes it (not the full sub-path) to App.auth.
			b.WriteString(fmt.Sprintf("Auth base path: /api/%s\n", path))
			b.WriteString(fmt.Sprintf("  App.auth.login('/api/%s', {...})      ← pass this base path\n", path))
			b.WriteString(fmt.Sprintf("  App.auth.register('/api/%s', {...})   ← NOT /api/%s/register\n\n", path, path))

			b.WriteString(fmt.Sprintf("  POST /api/%s/register — create account\n", path))
			b.WriteString(fmt.Sprintf("    Body: {\"%s\": \"...\", \"%s\": \"...\"", usernameCol, passwordCol))
			if len(optionalFields) > 0 {
				b.WriteString(", " + strings.Join(optionalFields, ", "))
			}
			b.WriteString("}\n")
			b.WriteString("    Response: {success: true, user_id: <number>, token: \"jwt...\"}\n\n")

			b.WriteString(fmt.Sprintf("  POST /api/%s/login — authenticate\n", path))
			b.WriteString(fmt.Sprintf("    Body: {\"%s\": \"...\", \"%s\": \"...\"}\n", usernameCol, passwordCol))
			b.WriteString("    Response: {success: true, user_id: <number>, token: \"jwt...\"}\n\n")

			b.WriteString(fmt.Sprintf("  GET /api/%s/me [AUTH] — current user profile\n\n", path))

			if defaultRole != "" {
				b.WriteString(fmt.Sprintf("  Default role: \"%s\", stored in column: \"%s\"\n\n", defaultRole, roleCol))
			}

			// OAuth providers for this auth endpoint.
			oauthRows, _ := db.Query("SELECT name, display_name FROM oauth_providers WHERE auth_endpoint_path = ? AND is_enabled = 1", path)
			if oauthRows != nil {
				for oauthRows.Next() {
					var name, displayName string
					oauthRows.Scan(&name, &displayName)
					b.WriteString(fmt.Sprintf("OAuth: <a href=\"/api/%s/oauth/%s\">%s</a>\n", path, name, displayName))
				}
				oauthRows.Close()
			}
		}
		authRows.Close()
	}

	// --- SSE stream endpoints ---
	streamRows, err := db.Query("SELECT path, event_types, requires_auth FROM stream_endpoints")
	if err == nil {
		for streamRows.Next() {
			var path, eventTypes string
			var requiresAuth bool
			streamRows.Scan(&path, &eventTypes, &requiresAuth)
			b.WriteString(fmt.Sprintf("SSE /api/%s/stream", path))
			if requiresAuth {
				b.WriteString(" [AUTH]")
			}
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("  Events: %s\n", eventTypes))
			b.WriteString(fmt.Sprintf("  Usage: new EventSource('/api/%s/stream')\n\n", path))
		}
		streamRows.Close()
	}

	// --- WebSocket endpoints ---
	wsRows, err := db.Query("SELECT path, event_types, receive_event_type, write_to_table, requires_auth FROM ws_endpoints")
	if err == nil {
		for wsRows.Next() {
			var path, eventTypes, receiveType string
			var writeToTable sql.NullString
			var requiresAuth bool
			wsRows.Scan(&path, &eventTypes, &receiveType, &writeToTable, &requiresAuth)
			b.WriteString(fmt.Sprintf("WebSocket /api/%s/ws", path))
			if requiresAuth {
				b.WriteString(" [AUTH]")
			}
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("  Receive events: %s\n", eventTypes))
			b.WriteString(fmt.Sprintf("  Send: ws.send(JSON.stringify({...})) → %s\n", receiveType))
			if writeToTable.Valid && writeToTable.String != "" {
				b.WriteString(fmt.Sprintf("  Auto-writes to table: %s\n", writeToTable.String))
			}
			b.WriteString(fmt.Sprintf("  Usage: new WebSocket((location.protocol==='https:'?'wss:':'ws:')+'//'+location.host+'/api/%s/ws')\n\n", path))
		}
		wsRows.Close()
	}

	// --- Upload endpoints ---
	uploadRows, err := db.Query("SELECT path, allowed_types, max_size_mb, requires_auth FROM upload_endpoints")
	if err == nil {
		for uploadRows.Next() {
			var path, allowedTypes string
			var maxSizeMB int
			var requiresAuth bool
			uploadRows.Scan(&path, &allowedTypes, &maxSizeMB, &requiresAuth)
			b.WriteString(fmt.Sprintf("POST /api/%s/upload (multipart, field: \"file\")", path))
			if requiresAuth {
				b.WriteString(" [AUTH]")
			}
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("  Allowed: %s, max: %dMB\n", allowedTypes, maxSizeMB))
			b.WriteString("  Response: {url, filename, size, type}\n\n")
		}
		uploadRows.Close()
	}

	result := b.String()
	if result == "" {
		return ""
	}

	// Append standard response format docs.
	result += `Standard response formats:
  List: GET /api/{path} → {data: [...], count: N, limit: N, offset: N}
  Single: GET /api/{path}/{id} → bare object
  Filter: /api/{path}?column=value&sort=col&order=asc|desc
  Stats: GET /api/{path}/_stats?fn=count|sum|avg|min|max&column=col (inherits [AUTH] from endpoint)
  Create: POST /api/{path} → created object
  Error: {error: "message"} with 4xx/5xx status
  Auth header: Authorization: Bearer {token}
`
	return result
}

// getAuthEndpointContract returns just the auth endpoint reference (register/login/me).
// Used for pages that don't need full CRUD data but still need auth form endpoints.
func (w *PipelineWorker) getAuthEndpointContract() string {
	db := w.siteDB.DB
	var b strings.Builder

	authRows, err := db.Query(`SELECT ae.path, ae.username_column, ae.password_column,
		ae.default_role, dt.schema_def
		FROM auth_endpoints ae
		LEFT JOIN dynamic_tables dt ON ae.table_name = dt.table_name`)
	if err != nil {
		return ""
	}
	defer authRows.Close()

	for authRows.Next() {
		var path, usernameCol, passwordCol string
		var defaultRole sql.NullString
		var schemaDef sql.NullString
		authRows.Scan(&path, &usernameCol, &passwordCol, &defaultRole, &schemaDef)

		// Build optional registration fields from schema.
		var optionalFields []string
		if schemaDef.Valid && schemaDef.String != "" {
			var cols map[string]string
			if json.Unmarshal([]byte(schemaDef.String), &cols) == nil {
				for col, typ := range cols {
					if col == "id" || col == "created_at" || col == usernameCol ||
						col == passwordCol || strings.EqualFold(typ, "PASSWORD") {
						continue
					}
					if defaultRole.Valid && col == "role" {
						continue
					}
					optionalFields = append(optionalFields, fmt.Sprintf("\"%s\": \"...\"", col))
				}
				sort.Strings(optionalFields)
			}
		}

		b.WriteString(fmt.Sprintf("POST /api/%s/register — create account\n", path))
		b.WriteString(fmt.Sprintf("  Body: {\"%s\": \"...\", \"%s\": \"...\"", usernameCol, passwordCol))
		if len(optionalFields) > 0 {
			b.WriteString(", " + strings.Join(optionalFields, ", "))
		}
		b.WriteString("}\n")
		b.WriteString("  Response: {success: true, user_id: <number>, token: \"jwt...\"}\n\n")

		b.WriteString(fmt.Sprintf("POST /api/%s/login — authenticate\n", path))
		b.WriteString(fmt.Sprintf("  Body: {\"%s\": \"...\", \"%s\": \"...\"}\n", usernameCol, passwordCol))
		b.WriteString("  Response: {success: true, user_id: <number>, token: \"jwt...\"}\n\n")

		b.WriteString(fmt.Sprintf("GET /api/%s/me [AUTH] — current user profile\n\n", path))

		// OAuth providers.
		oauthRows, _ := db.Query("SELECT name, display_name FROM oauth_providers WHERE auth_endpoint_path = ? AND is_enabled = 1", path)
		if oauthRows != nil {
			for oauthRows.Next() {
				var name, displayName string
				oauthRows.Scan(&name, &displayName)
				b.WriteString(fmt.Sprintf("OAuth: <a href=\"/api/%s/oauth/%s\">%s</a>\n", path, name, displayName))
			}
			oauthRows.Close()
		}
	}

	return b.String()
}

// cssCustomPropRe matches CSS custom property declarations like --primary: #hex;
var cssCustomPropRe = regexp.MustCompile(`(--[\w-]+)\s*:`)

// cssRuleBlockRe matches a CSS selector and its declaration block.
var cssRuleBlockRe = regexp.MustCompile(`\.([a-zA-Z_][\w-]*)\s*(?:,\s*\.[a-zA-Z_][\w-]*\s*)*\{([^}]*)\}`)

// signatureProps are CSS properties worth surfacing in the class signature.
var signatureProps = map[string]bool{
	"display": true, "flex-direction": true, "grid-template-columns": true,
	"min-height": true, "max-width": true, "position": true,
	"background": true, "background-color": true, "gap": true,
	"padding": true, "text-align": true, "font-size": true,
	"color": true, "border-radius": true, "margin": true,
}

// extractClassSignature returns a short parenthetical describing the class,
// e.g. "(grid, 3col)" or "(flex, column)".
func extractClassSignature(declarations string) string {
	var parts []string
	for _, line := range strings.Split(declarations, ";") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		prop := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])

		if !signatureProps[prop] {
			continue
		}

		switch prop {
		case "display":
			parts = append(parts, val)
		case "grid-template-columns":
			cols := strings.Count(val, "fr") + strings.Count(val, "auto") + strings.Count(val, "px")
			if strings.Contains(val, "repeat") {
				// Extract repeat count: repeat(3, 1fr) → 3col
				if ri := strings.Index(val, "("); ri >= 0 {
					if ci := strings.Index(val[ri+1:], ","); ci >= 0 {
						parts = append(parts, strings.TrimSpace(val[ri+1:ri+1+ci])+"col")
					}
				}
			} else if cols > 0 {
				parts = append(parts, fmt.Sprintf("%dcol", cols))
			}
		case "flex-direction":
			parts = append(parts, val)
		case "min-height":
			parts = append(parts, "min-h:"+val)
		case "max-width":
			parts = append(parts, "max-w:"+val)
		case "position":
			if val != "relative" { // only surface non-default
				parts = append(parts, val)
			}
		case "background", "background-color":
			if strings.Contains(val, "var(") {
				// Extract custom property name.
				if si := strings.Index(val, "--"); si >= 0 {
					end := strings.IndexAny(val[si:], " ),")
					if end < 0 {
						end = len(val[si:])
					}
					parts = append(parts, "bg:"+val[si:si+end])
				}
			}
		case "text-align":
			parts = append(parts, "text:"+val)
		case "padding":
			parts = append(parts, "pad:"+val)
		case "gap":
			parts = append(parts, "gap:"+val)
		case "font-size":
			parts = append(parts, "font:"+val)
		case "color":
			if strings.Contains(val, "var(") {
				if si := strings.Index(val, "--"); si >= 0 {
					end := strings.IndexAny(val[si:], " ),")
					if end < 0 {
						end = len(val[si:])
					}
					parts = append(parts, "color:"+val[si:si+end])
				}
			}
		case "border-radius":
			parts = append(parts, "radius:"+val)
		case "margin":
			parts = append(parts, "margin:"+val)
		}

		if len(parts) >= 3 {
			break // keep signatures informative but compact
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// cssNestedSelectorRe matches ".parent .child" or ".parent > .child" selectors.
var cssNestedSelectorRe = regexp.MustCompile(`\.([a-zA-Z_][\w-]*)\s*[> ]+\s*\.([a-zA-Z_][\w-]*)`)

// extractCSSClassMap extracts class names with property signatures and custom
// properties from CSS content. Groups classes into component patterns (parent-child)
// and utility classes for a structured reference.
func extractCSSClassMap(cssContent string) string {
	if cssContent == "" {
		return ""
	}

	// Extract class names with their declaration blocks for signatures.
	classSignatures := make(map[string]string) // class → signature
	for _, m := range cssRuleBlockRe.FindAllStringSubmatch(cssContent, -1) {
		if len(m) > 2 {
			cls := m[1]
			if _, exists := classSignatures[cls]; !exists {
				classSignatures[cls] = extractClassSignature(m[2])
			}
		}
	}

	// Also pick up classes from the existing selector regex that the rule block regex might miss.
	for _, m := range cssSelectorRe.FindAllStringSubmatch(cssContent, -1) {
		if len(m) > 1 {
			if _, exists := classSignatures[m[1]]; !exists {
				classSignatures[m[1]] = ""
			}
		}
	}

	// --- Build component patterns from CSS nesting + prefix analysis ---
	children := make(map[string][]string) // parent → child classes

	// 1. Detect parent-child from CSS nested selectors (.card .card-title).
	for _, m := range cssNestedSelectorRe.FindAllStringSubmatch(cssContent, -1) {
		parent, child := m[1], m[2]
		if parent != child {
			children[parent] = appendUniqueStr(children[parent], child)
		}
	}

	// 2. Group by shared prefix: card-title, card-body → children of "card".
	allClasses := make([]string, 0, len(classSignatures))
	for cls := range classSignatures {
		allClasses = append(allClasses, cls)
	}
	for _, cls := range allClasses {
		if idx := strings.Index(cls, "-"); idx > 0 {
			prefix := cls[:idx]
			// Only group if the prefix itself is a defined class.
			if _, isClass := classSignatures[prefix]; isClass && prefix != cls {
				children[prefix] = appendUniqueStr(children[prefix], cls)
			}
		}
	}

	// Identify which classes are components (have children).
	componentClasses := make(map[string]bool)
	childClasses := make(map[string]bool)
	for parent, kids := range children {
		if len(kids) >= 1 {
			componentClasses[parent] = true
			for _, kid := range kids {
				childClasses[kid] = true
			}
		}
	}

	// Extract custom properties.
	propSet := make(map[string]bool)
	for _, m := range cssCustomPropRe.FindAllStringSubmatch(cssContent, -1) {
		if len(m) > 1 {
			propSet[m[1]] = true
		}
	}

	var b strings.Builder

	// Output component patterns first.
	if len(componentClasses) > 0 {
		parents := make([]string, 0, len(componentClasses))
		for p := range componentClasses {
			parents = append(parents, p)
		}
		sort.Strings(parents)
		b.WriteString("Component patterns:\n")
		for _, parent := range parents {
			kids := children[parent]
			sort.Strings(kids)
			sig := classSignatures[parent]
			b.WriteString(fmt.Sprintf("  .%s%s > %s\n", parent, sig, "."+strings.Join(kids, ", .")))
		}
		b.WriteString("\n")
	}

	// Output remaining utility/standalone classes.
	remaining := make([]string, 0)
	for cls := range classSignatures {
		if !componentClasses[cls] && !childClasses[cls] {
			remaining = append(remaining, cls)
		}
	}
	if len(remaining) > 0 {
		sort.Strings(remaining)
		b.WriteString("Utility classes:\n")
		for _, cls := range remaining {
			sig := classSignatures[cls]
			b.WriteString("  " + cls + sig + "\n")
		}
	}

	if len(propSet) > 0 {
		props := make([]string, 0, len(propSet))
		for prop := range propSet {
			props = append(props, prop)
		}
		sort.Strings(props)
		b.WriteString("Custom properties: ")
		b.WriteString(strings.Join(props, ", "))
		b.WriteString("\n")
	}
	return b.String()
}

// appendUniqueStr appends s to slice if not already present.
func appendUniqueStr(slice []string, s string) []string {
	for _, existing := range slice {
		if existing == s {
			return slice
		}
	}
	return append(slice, s)
}


// validateDesign checks that the DESIGN stage produced required artifacts.
func validateDesign(db *sql.DB, plan *SitePlan) []string {
	var issues []string

	// Check for at least one global CSS asset.
	var cssCount int
	db.QueryRow("SELECT COUNT(*) FROM assets WHERE scope = 'global' AND filename LIKE '%.css'").Scan(&cssCount)
	if cssCount == 0 {
		issues = append(issues, "No global CSS file created")
	}

	// Check for default layout.
	var layoutBefore, layoutAfter sql.NullString
	db.QueryRow("SELECT body_before_main, body_after_main FROM layouts WHERE name = 'default'").Scan(&layoutBefore, &layoutAfter)
	if !layoutBefore.Valid || layoutBefore.String == "" {
		issues = append(issues, "Layout 'default' missing or has empty body_before_main (nav)")
	}
	if !layoutAfter.Valid || layoutAfter.String == "" {
		issues = append(issues, "Layout 'default' missing or has empty body_after_main (footer)")
	}

	// Check that all referenced custom layouts exist.
	for _, page := range plan.Pages {
		layout := page.Layout
		if layout == "" || layout == "default" || layout == "none" {
			continue
		}
		var layoutExists int
		db.QueryRow("SELECT COUNT(*) FROM layouts WHERE name = ?", layout).Scan(&layoutExists)
		if layoutExists == 0 {
			issues = append(issues, fmt.Sprintf("Page %s references layout %q but it was not created", page.Path, layout))
		}
	}

	return issues
}

// hexToLuminance converts a hex color (#rrggbb or #rgb) to relative luminance (0-1).
func hexToLuminance(hex string) (float64, bool) {
	hex = strings.TrimSpace(strings.TrimPrefix(hex, "#"))
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 0, false
	}
	r, err1 := strconv.ParseUint(hex[0:2], 16, 8)
	g, err2 := strconv.ParseUint(hex[2:4], 16, 8)
	b, err3 := strconv.ParseUint(hex[4:6], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	// sRGB to linear conversion.
	linearize := func(v uint64) float64 {
		s := float64(v) / 255.0
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linearize(r) + 0.7152*linearize(g) + 0.0722*linearize(b), true
}

// contrastRatio computes the WCAG contrast ratio between two luminance values.
func contrastRatio(l1, l2 float64) float64 {
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// validateColorContrast checks that text/background colors have sufficient contrast (WCAG AA).
func validateColorContrast(plan *SitePlan) []string {
	var issues []string

	textLum, textOK := hexToLuminance(plan.ColorScheme.Text)
	bgLum, bgOK := hexToLuminance(plan.ColorScheme.Background)
	primaryLum, primaryOK := hexToLuminance(plan.ColorScheme.Primary)

	if textOK && bgOK {
		ratio := contrastRatio(textLum, bgLum)
		if ratio < 4.5 {
			issues = append(issues, fmt.Sprintf(
				"Text color %s on background %s has low contrast (%.1f:1, WCAG AA requires 4.5:1)",
				plan.ColorScheme.Text, plan.ColorScheme.Background, ratio))
		}
	}

	if primaryOK && bgOK {
		ratio := contrastRatio(primaryLum, bgLum)
		if ratio < 3.0 {
			issues = append(issues, fmt.Sprintf(
				"Primary color %s on background %s has low contrast (%.1f:1, minimum 3:1 for large text)",
				plan.ColorScheme.Primary, plan.ColorScheme.Background, ratio))
		}
	}

	return issues
}

// validateDataLayer checks that all planned tables and endpoints exist.
func validateDataLayer(db *sql.DB, plan *SitePlan) []string {
	var issues []string

	for _, t := range plan.DataTables {
		var exists int
		db.QueryRow("SELECT COUNT(*) FROM dynamic_tables WHERE table_name = ?", t.Name).Scan(&exists)
		if exists == 0 {
			issues = append(issues, fmt.Sprintf("Table %q not created", t.Name))
		}
		if t.HasAPI {
			var apiExists int
			db.QueryRow("SELECT COUNT(*) FROM api_endpoints WHERE table_name = ?", t.Name).Scan(&apiExists)
			if apiExists == 0 {
				issues = append(issues, fmt.Sprintf("API endpoint for table %q not created", t.Name))
			}
		}
		// Auth endpoints only make sense for tables that have a PASSWORD column.
		// Tables with HasAuth but no PASSWORD column just need requires_auth on
		// their API endpoint, which doesn't create an auth_endpoints row.
		if t.HasAuth && tableHasPasswordColumn(t) {
			var authExists int
			db.QueryRow("SELECT COUNT(*) FROM auth_endpoints WHERE table_name = ?", t.Name).Scan(&authExists)
			if authExists == 0 {
				issues = append(issues, fmt.Sprintf("Auth endpoint for table %q not created", t.Name))
			}
		}
	}

	return issues
}

// tableHasPasswordColumn checks if a table plan defines a PASSWORD column.
func tableHasPasswordColumn(t TablePlan) bool {
	for _, col := range t.Columns {
		if strings.EqualFold(col.Type, "PASSWORD") {
			return true
		}
	}
	return false
}
