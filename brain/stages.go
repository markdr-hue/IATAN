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
			w.siteDB.ExecWrite(
				"INSERT INTO questions (question, urgency, status, options) VALUES (?, 'normal', 'pending', ?)",
				q.Question, opts,
			)
			if w.deps.Bus != nil {
				w.deps.Bus.Publish(events.NewEvent(events.EventQuestionAsked, w.siteID, map[string]interface{}{
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

	_, lastModel, totalTokens, toolCalls, err := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 12, 8192)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageDesign, err
	}

	// Validate design output.
	issues := validateDesign(w.siteDB.Writer(), plan)
	if len(issues) > 0 {
		w.logger.Warn("design validation failed", "issues", issues)
		LogStageError(w.siteDB, logID, strings.Join(issues, "; "))
		return StageDesign, fmt.Errorf("design validation: %s", strings.Join(issues, "; "))
	}

	w.publishBrainMessage("Design system created successfully.")
	LogStageComplete(w.siteDB, logID, totalTokens, 0, toolCalls, time.Since(start))
	_ = lastModel

	return StageBlueprintPages, nil
}

// --- BLUEPRINT_PAGES stage ---

func (w *PipelineWorker) runBlueprintPages(ctx context.Context) (PipelineStage, error) {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageBlueprintPages)

	plan, err := w.loadPlan()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageBlueprintPages, err
	}

	provider, modelID, err := w.getProvider()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageBlueprintPages, err
	}

	// Load CSS class map from the design stage output for blueprint context.
	cssContent := w.getGlobalCSS()
	cssClassMap := extractCSSClassMap(cssContent)
	layoutSummary := w.getLayoutSummary()

	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	var siteDescription string
	if site != nil && site.Description != nil {
		siteDescription = *site.Description
	}

	prompt := buildBlueprintPrompt(plan, cssClassMap, layoutSummary, siteDescription)
	userMsg := "Generate the page blueprints for every page in the plan."
	w.saveChatMessageOnce(llm.Message{Role: llm.RoleUser, Content: userMsg})

	// Single LLM call, no tools — like PLAN stage.
	content, tokens, err := w.callLLM(ctx, provider, modelID, prompt, userMsg, 8192)
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageBlueprintPages, err
	}

	bp, err := ParseSiteBlueprint(content)
	if err != nil {
		// Retry once with stricter prompt.
		w.logger.Warn("blueprint parse failed, retrying", "error", err)
		retryPrompt := prompt + fmt.Sprintf("\n\nCRITICAL: Your previous response was not valid JSON. Parse error: %s\nRespond with ONLY a JSON object, no markdown, no explanation.", err.Error())
		content, tokens2, err2 := w.callLLM(ctx, provider, modelID, retryPrompt, "Respond with ONLY the JSON blueprint.", 8192)
		tokens += tokens2
		if err2 != nil {
			LogStageError(w.siteDB, logID, err2.Error())
			return StageBlueprintPages, err2
		}
		bp, err = ParseSiteBlueprint(content)
		if err != nil {
			LogStageError(w.siteDB, logID, err.Error())
			return StageBlueprintPages, fmt.Errorf("blueprint parse failed after retry: %w", err)
		}
	}

	// Validate blueprint completeness (non-fatal: missing pages get generic treatment).
	if issues := ValidateBlueprint(bp, plan); len(issues) > 0 {
		w.logger.Warn("blueprint validation issues", "issues", issues)
	}

	// Save blueprint JSON to pipeline_state.
	bpJSON, _ := json.Marshal(bp)
	_, err = w.siteDB.ExecWrite(`UPDATE pipeline_state SET blueprint_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1`, string(bpJSON))
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return StageBlueprintPages, err
	}

	w.publishBrainMessage("Page blueprints created.")
	LogStageComplete(w.siteDB, logID, tokens, 0, 0, time.Since(start))

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

	// Load site description for content context.
	site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
	var siteDescription string
	if site != nil && site.Description != nil {
		siteDescription = *site.Description
	}

	// Load blueprint from pipeline_state (created by BLUEPRINT_PAGES stage).
	var blueprint *SiteBlueprint
	if state != nil && state.BlueprintJSON != "" {
		blueprint, _ = ParseSiteBlueprint(state.BlueprintJSON)
	}

	// Build homepage first to establish tone, then build remaining pages concurrently.
	// This lets us collect warnings and extract key content terms from the homepage.
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
				if err := w.buildSinglePage(ctx, plan, page, allPaths, layoutSummary, cssClassMap, siteDescription, nil, nil, blueprint); err != nil {
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

		err := w.buildSinglePage(ctx, plan, page, allPaths, layoutSummary, cssClassMap, siteDescription, contentTerms, previousWarnings, blueprint)
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

func (w *PipelineWorker) buildSinglePage(ctx context.Context, plan *SitePlan, page PagePlan, allPaths []string, layoutSummary, cssClassMap, siteDescription string, contentTerms, previousWarnings []string, blueprint *SiteBlueprint) error {
	start := time.Now()
	logID, _ := LogStageStart(w.siteDB, StageBuildPages)

	provider, modelID, err := w.getProvider()
	if err != nil {
		LogStageError(w.siteDB, logID, err.Error())
		return err
	}

	// Load table schemas if page needs data (supports multiple tables per page).
	// Use writer pool for WAL visibility (tables/endpoints written in DATA_LAYER).
	var tableSchema string
	if page.NeedsData && len(page.DataTables) > 0 {
		var parts []string
		for _, tableName := range page.DataTables {
			var entry strings.Builder
			var schemaDef sql.NullString
			w.siteDB.Writer().QueryRow("SELECT schema_def FROM dynamic_tables WHERE table_name = ?", tableName).Scan(&schemaDef)
			if schemaDef.Valid {
				entry.WriteString(schemaDef.String)
			} else {
				entry.WriteString(fmt.Sprintf("Table: %s", tableName))
			}
			// Query API endpoint path, auth requirement, and public columns.
			var apiPath, publicCols sql.NullString
			var requiresAuth bool
			w.siteDB.Writer().QueryRow(
				"SELECT path, requires_auth, public_columns FROM api_endpoints WHERE table_name = ?", tableName,
			).Scan(&apiPath, &requiresAuth, &publicCols)
			if apiPath.Valid {
				entry.WriteString(fmt.Sprintf("\nAPI endpoint: /api/%s", apiPath.String))
				if requiresAuth {
					entry.WriteString(" (REQUIRES AUTH — include Authorization header)")
				}
				if publicCols.Valid && publicCols.String != "" && publicCols.String != "[]" {
					entry.WriteString(fmt.Sprintf("\nPublic columns (only these are returned by GET): %s", publicCols.String))
				}
			}
			parts = append(parts, entry.String())
		}
		tableSchema = strings.Join(parts, "\n\n")

		// Find the auth endpoint (login/register path) for this site so the
		// LLM knows how to obtain and use JWT tokens.
		var authPath, usernameCol sql.NullString
		w.siteDB.Writer().QueryRow("SELECT path, username_column FROM auth_endpoints LIMIT 1").Scan(&authPath, &usernameCol)
		if authPath.Valid {
			loginField := "username"
			if usernameCol.Valid && usernameCol.String != "" {
				loginField = usernameCol.String
			}
			tableSchema += fmt.Sprintf("\n\nAuth endpoint: POST /api/%s/login -> returns {\"token\": \"jwt...\"}", authPath.String)
			tableSchema += fmt.Sprintf("\nRegister: POST /api/%s/register", authPath.String)
			tableSchema += fmt.Sprintf("\nLogin/register JSON field: \"%s\" (use this exact key in the request body)", loginField)
			tableSchema += fmt.Sprintf("\nExample body: {\"%s\": \"...\", \"password\": \"...\"}", loginField)
			tableSchema += "\nUse: fetch(url, {headers: {'Authorization': 'Bearer ' + token}})"

			// OAuth providers for this auth endpoint.
			oauthRows, _ := w.siteDB.Writer().Query("SELECT name, display_name FROM oauth_providers WHERE auth_endpoint_path = ? AND is_enabled = 1", authPath.String)
			if oauthRows != nil {
				var oauthProviders []string
				for oauthRows.Next() {
					var name, displayName string
					oauthRows.Scan(&name, &displayName)
					oauthProviders = append(oauthProviders, fmt.Sprintf("%s (/api/%s/oauth/%s)", displayName, authPath.String, name))
				}
				oauthRows.Close()
				if len(oauthProviders) > 0 {
					tableSchema += "\nOAuth: " + strings.Join(oauthProviders, ", ")
				}
			}

			// Role info.
			var defaultRole, roleCol sql.NullString
			w.siteDB.Writer().QueryRow("SELECT default_role, role_column FROM auth_endpoints WHERE path = ?", authPath.String).Scan(&defaultRole, &roleCol)
			if defaultRole.Valid && defaultRole.String != "" {
				tableSchema += fmt.Sprintf("\nRoles: default=%s (stored in column: %s)", defaultRole.String, roleCol.String)
			}
		}
	}

	// List available SVG assets for page content.
	svgAssets := w.getSVGAssetList()

	prompt := buildPagePrompt(page, plan, allPaths, layoutSummary, cssClassMap, siteDescription, tableSchema, svgAssets, contentTerms, previousWarnings, blueprint)
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

	// First: rebuild any missing planned pages (Go-driven, no LLM guessing).
	plan, _ := w.loadPlan()
	if plan != nil {
		allPaths := make([]string, len(plan.Pages))
		for i, p := range plan.Pages {
			allPaths[i] = p.Path
		}
		layoutSummary := w.getLayoutSummary()
		cssClassMap := extractCSSClassMap(w.getGlobalCSS())

		site, _ := models.GetSiteByID(w.deps.DB, w.siteID)
		var siteDescription string
		if site != nil && site.Description != nil {
			siteDescription = *site.Description
		}

		// Load blueprint for rebuilding context.
		var blueprint *SiteBlueprint
		reviewState, _ := LoadPipelineState(w.siteDB.DB)
		if reviewState != nil && reviewState.BlueprintJSON != "" {
			blueprint, _ = ParseSiteBlueprint(reviewState.BlueprintJSON)
		}

		for _, page := range plan.Pages {
			var exists int
			// Use writer pool so we see pages created during BUILD_PAGES (WAL visibility).
			w.siteDB.Writer().QueryRow("SELECT COUNT(*) FROM pages WHERE path = ? AND is_deleted = 0", page.Path).Scan(&exists)
			if exists == 0 {
				w.logger.Warn("review: rebuilding missing planned page", "path", page.Path)
				w.publishBrainMessage(fmt.Sprintf("Rebuilding missing page: %s", page.Path))
				if err := w.buildSinglePage(ctx, plan, page, allPaths, layoutSummary, cssClassMap, siteDescription, nil, nil, blueprint); err != nil {
					w.logger.Error("review: failed to rebuild page", "path", page.Path, "error", err)
				}
			}
		}
	}

	// Run Go-based validation (zero tokens) with a multi-pass fix loop
	// (same pattern as DATA_LAYER fix-up at lines 287-308).
	issues := validateSite(w.siteDB.DB)
	issues = append(issues, validateColorContrast(plan)...)
	totalTokens := 0

	for fixAttempt := 0; fixAttempt < 3; fixAttempt++ {
		if len(issues) == 0 {
			break
		}
		if fixAttempt == 2 {
			// Final attempt still has issues — log and proceed (don't block deployment).
			w.publishBrainMessage(fmt.Sprintf("Review: %d issues remain after %d fix attempts: %s", len(issues), fixAttempt+1, strings.Join(issues, "; ")))
			break
		}

		w.publishBrainMessage(fmt.Sprintf("Review found %d issues (attempt %d/3), fixing...", len(issues), fixAttempt+1))

		provider, modelID, err := w.getProvider()
		if err != nil {
			break
		}

		prompt := buildReviewPrompt(issues, w.siteDB.DB, plan)
		messages := []llm.Message{{Role: llm.RoleUser, Content: "Fix the following issues:\n" + strings.Join(issues, "\n")}}
		if fixAttempt == 0 {
			w.saveChatMessageOnce(messages[0])
		}

		toolDefs := w.deps.ToolRegistry.ToLLMToolsFiltered(toToolSet(reviewTools))
		_, _, tokens, _, _ := w.runToolLoop(ctx, provider, modelID, prompt, messages, toolDefs, 4, 8192)
		totalTokens += tokens

		// Re-validate after fixes.
		issues = validateSite(w.siteDB.DB)
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
	w.deps.DB.Exec("UPDATE sites SET mode = 'monitoring', updated_at = CURRENT_TIMESTAMP WHERE id = ?", w.siteID)
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
	if existingPlan != nil {
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
	}

	// Clear the update description now that it's been consumed.
	w.siteDB.ExecWrite("UPDATE pipeline_state SET update_description = NULL WHERE id = 1")

	LogStageComplete(w.siteDB, logID, tokens, 0, 0, time.Since(start))

	// Route to appropriate next stage.
	// Always regenerate blueprints for new/modified pages.
	if patch.UpdateCSS {
		return StageDesign, nil // DESIGN → BLUEPRINT_PAGES → DATA_LAYER? → BUILD_PAGES
	}
	if len(patch.AddTables) > 0 {
		return StageBlueprintPages, nil // BLUEPRINT_PAGES → DATA_LAYER → BUILD_PAGES
	}
	return StageBlueprintPages, nil // BLUEPRINT_PAGES → BUILD_PAGES
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

// extractCSSClassMap extracts class names with property signatures and custom
// properties from CSS content. Returns a compact reference string (~70% smaller
// than full CSS) that tells the LLM what each class does.
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

	// Also pick up classes from the existing selector regex that the rule block regex might miss
	// (e.g. classes in complex selectors, pseudo-classes).
	for _, m := range cssSelectorRe.FindAllStringSubmatch(cssContent, -1) {
		if len(m) > 1 {
			if _, exists := classSignatures[m[1]]; !exists {
				classSignatures[m[1]] = ""
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
	if len(classSignatures) > 0 {
		classes := make([]string, 0, len(classSignatures))
		for cls := range classSignatures {
			classes = append(classes, cls)
		}
		sort.Strings(classes)
		b.WriteString("Available CSS classes:\n")
		for _, cls := range classes {
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

	// SPA: check for router JS.
	if plan.Architecture == "spa" {
		var routerCount int
		db.QueryRow("SELECT COUNT(*) FROM assets WHERE scope = 'global' AND filename LIKE '%router%' AND filename LIKE '%.js'").Scan(&routerCount)
		if routerCount == 0 {
			issues = append(issues, "SPA architecture but no router JS file found")
		}
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
