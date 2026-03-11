/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package brain

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/markdr-hue/IATAN/db"
)

// --- Analysis: output of the ANALYZE stage ---

// Analysis captures what the user wants, mapped to platform capabilities.
type Analysis struct {
	AppType       string         `json:"app_type"`                 // "chat-app", "blog", "portfolio", etc.
	CoreBehaviors []string       `json:"core_behaviors"`           // ["real-time messaging", "channel rooms"]
	RequiresAuth  bool           `json:"requires_auth"`            // does this need login/register?
	AuthStrategy  string         `json:"auth_strategy"`            // "jwt", "localStorage-only", "none"
	RealTimeNeeds []RealTimeSpec `json:"realtime_needs,omitempty"` // WebSocket/SSE specs
	DataNeeds     []DataSpec     `json:"data_needs,omitempty"`     // tables needed and why
	Exclusions     []string         `json:"exclusions,omitempty"`      // ["no auth endpoints", "no OAuth"]
	DesignMood     string           `json:"design_mood"`               // "dark-retro-terminal", "clean-modern"
	Architecture   string           `json:"architecture,omitempty"`    // "spa", "multi-page", or "single-page"
	WebhookNeeds   []WebhookNeed   `json:"webhook_needs,omitempty"`   // planned webhooks
	ScheduledTasks []TaskNeed      `json:"scheduled_tasks,omitempty"` // planned scheduled tasks
	Questions      []PlanQuestion  `json:"questions,omitempty"`
}

type WebhookNeed struct {
	Name      string   `json:"name"`                 // "stripe-events"
	Direction string   `json:"direction"`             // "incoming" or "outgoing"
	Purpose   string   `json:"purpose"`               // "receive Stripe payment notifications"
	URL       string   `json:"url,omitempty"`          // outgoing only
	Events    []string `json:"event_types,omitempty"`  // ["payment.completed", "data.insert"]
}

type TaskNeed struct {
	Name           string `json:"name"`                        // "daily-digest"
	Purpose        string `json:"purpose"`                     // "send daily summary email to owner"
	CronExpression string `json:"cron_expression,omitempty"`   // "0 8 * * *"
	IntervalSec    int    `json:"interval_seconds,omitempty"`  // alternative: every N seconds
}

type RealTimeSpec struct {
	Purpose    string `json:"purpose"`                // "chat messages in channels"
	Type       string `json:"type"`                   // "websocket" or "sse"
	Path       string `json:"path"`                   // "chat"
	RoomScoped bool   `json:"room_scoped,omitempty"`  // true = use room_column
	RoomColumn string `json:"room_column,omitempty"`  // "channel_id"
	WriteTable string `json:"write_table,omitempty"`  // "messages"
}

type DataSpec struct {
	TableName  string      `json:"table_name"`
	Purpose    string      `json:"purpose"` // "store chat messages with channel association"
	Columns    []ColumnDef `json:"columns"`
	NeedsAPI   bool        `json:"needs_api"`
	NeedsAuth  bool        `json:"needs_auth"`
	PublicRead bool        `json:"public_read,omitempty"`
	SeedData   bool        `json:"seed_data,omitempty"`
}

// --- Blueprint: output of the BLUEPRINT stage ---

// Blueprint is the complete build specification that drives the BUILD stage.
type Blueprint struct {
	AppType      string          `json:"app_type"`
	AuthStrategy string          `json:"auth_strategy"`
	Architecture string          `json:"architecture"` // "spa" or "multi-page"
	ColorScheme  ColorScheme     `json:"color_scheme"`
	Typography   Typography      `json:"typography"`
	DesignNotes  string          `json:"design_notes"`
	Endpoints    []EndpointSpec  `json:"endpoints,omitempty"`
	DataTables   []TableSpec     `json:"data_tables,omitempty"`
	Pages          []PageBlueprint `json:"pages"`
	NavItems       []string        `json:"nav_items"`
	Exclusions     []string        `json:"exclusions,omitempty"`
	Webhooks       []WebhookSpec   `json:"webhooks,omitempty"`
	ScheduledTasks []TaskSpec2     `json:"scheduled_tasks,omitempty"`
	Questions      []PlanQuestion  `json:"questions,omitempty"`
}

type WebhookSpec struct {
	Name      string   `json:"name"`
	Direction string   `json:"direction"`            // "incoming" or "outgoing"
	URL       string   `json:"url,omitempty"`         // outgoing only
	Events    []string `json:"event_types,omitempty"` // events to subscribe to
}

type TaskSpec2 struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Prompt         string `json:"prompt"`                      // what the brain should do
	CronExpression string `json:"cron_expression,omitempty"`
	IntervalSec    int    `json:"interval_seconds,omitempty"`
}

type EndpointSpec struct {
	Action       string   `json:"action"`                   // "create_api", "create_auth", "create_websocket", "create_stream", "create_upload"
	Path         string   `json:"path"`                     // "messages", "chat", "uploads"
	TableName    string   `json:"table_name,omitempty"`     // which table to bind
	RequiresAuth bool     `json:"requires_auth,omitempty"`
	PublicRead   bool     `json:"public_read,omitempty"`
	RoomColumn   string   `json:"room_column,omitempty"`    // WebSocket: column for room scoping
	WriteTable   string   `json:"write_to_table,omitempty"` // WebSocket: table to write messages to
	EventTypes   []string `json:"event_types,omitempty"`    // Stream/WebSocket: event types
	// Auth-specific
	UsernameCol string `json:"username_column,omitempty"`
	PasswordCol string `json:"password_column,omitempty"`
}

type TableSpec struct {
	Name     string      `json:"name"`
	Purpose  string      `json:"purpose"`
	Columns  []ColumnDef `json:"columns"`
	SeedData bool        `json:"seed_data,omitempty"`
}

type PageBlueprint struct {
	Path           string   `json:"path"`
	Title          string   `json:"title"`
	Purpose        string   `json:"purpose"`
	Sections       []string `json:"sections"`
	LinksTo        []string `json:"links_to,omitempty"`
	Layout         string   `json:"layout,omitempty"`
	ComponentHints []string `json:"component_hints,omitempty"`
	UsesEndpoints  []string `json:"uses_endpoints,omitempty"` // ["GET /api/messages", "WS /api/chat/ws"]
	UsesAuth       bool     `json:"uses_auth,omitempty"`
	TechNotes      string   `json:"tech_notes"`               // technical build instructions for this page
	DataTables     []string `json:"data_tables,omitempty"`
}

// BlueprintPatch describes incremental changes for an UPDATE_BLUEPRINT flow.
type BlueprintPatch struct {
	AddPages       []PageBlueprint `json:"add_pages,omitempty"`
	ModifyPages    []PageBlueprint `json:"modify_pages,omitempty"`
	RemovePages    []string        `json:"remove_pages,omitempty"`
	AddEndpoints   []EndpointSpec  `json:"add_endpoints,omitempty"`
	AddTables      []TableSpec     `json:"add_tables,omitempty"`
	AddWebhooks    []WebhookSpec   `json:"add_webhooks,omitempty"`
	AddTasks       []TaskSpec2     `json:"add_scheduled_tasks,omitempty"`
	UpdateCSS      bool            `json:"update_css"`
	UpdateNav      bool            `json:"update_nav"`
}

// --- Shared types (kept from v1) ---

type ColorScheme struct {
	Primary    string `json:"primary"`
	Secondary  string `json:"secondary"`
	Accent     string `json:"accent"`
	Background string `json:"background"`
	Surface    string `json:"surface"`
	Text       string `json:"text"`
	TextMuted  string `json:"text_muted"`
}

type Typography struct {
	HeadingFont string `json:"heading_font"`
	BodyFont    string `json:"body_font"`
	Scale       string `json:"scale"` // e.g. "1.25" (major third)
}

type ColumnDef struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Primary  bool   `json:"primary,omitempty"`
	Required bool   `json:"required,omitempty"`
}

type PlanQuestion struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

// --- Pipeline state ---

// PipelineState is the singleton row in pipeline_state tracking build progress.
type PipelineState struct {
	Stage             PipelineStage
	AnalysisJSON      string // stored in plan_json column
	BlueprintJSON     string // stored in blueprint_json column
	CurrentPageIndex  int
	ErrorCount        int
	LastError         string
	Paused            bool
	PauseReason       string
	UpdateDescription string // what the owner wants changed (for UPDATE_BLUEPRINT)
	StartedAt         time.Time
	UpdatedAt         time.Time
}

// --- JSON parsing ---

var jsonFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*\n?(.*?)```")
var trailingCommaRe = regexp.MustCompile(`,\s*([\]}])`)

// extractJSON strips markdown code fences and any leading/trailing non-JSON text
// from raw LLM output, returning only the JSON object.
func extractJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 {
		return raw
	}

	// Find ALL code fences — prefer the one that starts with { or [.
	if allMatches := jsonFenceRe.FindAllStringSubmatch(raw, -1); len(allMatches) > 0 {
		for _, m := range allMatches {
			candidate := strings.TrimSpace(m[1])
			if len(candidate) > 0 && (candidate[0] == '{' || candidate[0] == '[') {
				return candidate
			}
		}
		// No fence started with JSON — use the first one anyway.
		return strings.TrimSpace(allMatches[0][1])
	}

	// Already a JSON object or array — return as-is.
	if raw[0] == '{' || raw[0] == '[' {
		return raw
	}

	// Fallback: find the first { and last } to extract JSON object.
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			return raw[start : end+1]
		}
	}

	return raw
}

// repairJSON fixes common LLM JSON mistakes (trailing commas, // comments).
func repairJSON(s string) string {
	// Strip single-line // comments (only full-line comments to avoid mangling URLs).
	lines := strings.Split(s, "\n")
	cleaned := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		cleaned = append(cleaned, line)
	}
	s = strings.Join(cleaned, "\n")

	// Remove trailing commas before } or ].
	s = trailingCommaRe.ReplaceAllString(s, "$1")

	return s
}

// ParseAnalysis parses an Analysis from raw LLM output.
func ParseAnalysis(raw string) (*Analysis, error) {
	raw = repairJSON(extractJSON(raw))
	var a Analysis
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, fmt.Errorf("invalid analysis JSON: %w", err)
	}
	return &a, nil
}

// ParseBlueprint parses a Blueprint from raw LLM output.
func ParseBlueprint(raw string) (*Blueprint, error) {
	raw = repairJSON(extractJSON(raw))
	var bp Blueprint
	if err := json.Unmarshal([]byte(raw), &bp); err != nil {
		return nil, fmt.Errorf("invalid blueprint JSON: %w", err)
	}
	return &bp, nil
}

// ParseBlueprintPatch parses a BlueprintPatch from raw LLM output.
func ParseBlueprintPatch(raw string) (*BlueprintPatch, error) {
	raw = repairJSON(extractJSON(raw))
	var patch BlueprintPatch
	if err := json.Unmarshal([]byte(raw), &patch); err != nil {
		return nil, fmt.Errorf("invalid blueprint patch JSON: %w", err)
	}
	return &patch, nil
}

// MarshalJSON serializes any struct to JSON string.
func marshalToJSON(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// --- Validation ---

// ValidateAnalysis checks an Analysis for structural errors.
func ValidateAnalysis(a *Analysis) []string {
	var errs []string
	if a.AppType == "" {
		errs = append(errs, "app_type is required")
	}
	if len(a.CoreBehaviors) == 0 {
		errs = append(errs, "core_behaviors must have at least one entry")
	}
	if a.AuthStrategy == "" {
		errs = append(errs, "auth_strategy is required (use 'none' if no auth needed)")
	}
	if a.DesignMood == "" {
		errs = append(errs, "design_mood is required")
	}
	// Architecture is optional and free-form — no restriction on valid values.
	// Validate real-time specs.
	for i, rt := range a.RealTimeNeeds {
		if rt.Type != "websocket" && rt.Type != "sse" {
			errs = append(errs, fmt.Sprintf("realtime_needs[%d].type must be 'websocket' or 'sse'", i))
		}
		if rt.Path == "" {
			errs = append(errs, fmt.Sprintf("realtime_needs[%d].path is required", i))
		}
	}
	// Validate data specs.
	for i, d := range a.DataNeeds {
		if d.TableName == "" {
			errs = append(errs, fmt.Sprintf("data_needs[%d].table_name is required", i))
		}
		if len(d.Columns) == 0 {
			errs = append(errs, fmt.Sprintf("data_needs[%d] (%s) must have at least one column", i, d.TableName))
		}
	}
	return errs
}

// ValidateBlueprint checks a Blueprint for structural errors.
func ValidateBlueprint(bp *Blueprint) []string {
	var errs []string

	// Architecture is free-form — no restriction on valid values.

	if len(bp.Pages) < 1 {
		errs = append(errs, "blueprint must include at least 1 page")
	}

	// Build path set for link validation.
	paths := make(map[string]bool, len(bp.Pages))
	for _, p := range bp.Pages {
		if paths[p.Path] {
			errs = append(errs, fmt.Sprintf("duplicate page path: %s", p.Path))
		}
		paths[p.Path] = true
	}

	if !paths["/"] {
		errs = append(errs, "blueprint must include a homepage at path /")
	}

	// Auto-add listing pages for parameterised routes.
	for _, p := range bp.Pages {
		if !strings.Contains(p.Path, ":") {
			continue
		}
		parts := strings.Split(p.Path, "/")
		var baseParts []string
		for _, part := range parts {
			if strings.HasPrefix(part, ":") {
				break
			}
			baseParts = append(baseParts, part)
		}
		basePath := strings.Join(baseParts, "/")
		if basePath == "" {
			basePath = "/"
		}
		if !paths[basePath] && basePath != "/" {
			listingPage := PageBlueprint{
				Path:     basePath,
				Title:    strings.TrimPrefix(basePath, "/"),
				Purpose:  fmt.Sprintf("Lists available items and navigates to %s with the selected ID.", p.Path),
				Sections: []string{"listing"},
				LinksTo:  []string{p.Path},
			}
			listingPage.DataTables = append(listingPage.DataTables, p.DataTables...)
			bp.Pages = append(bp.Pages, listingPage)
			paths[basePath] = true
			for i, nav := range bp.NavItems {
				if nav == p.Path {
					bp.NavItems[i] = basePath
				}
			}
		}
	}

	// Verify all links_to targets exist.
	for _, p := range bp.Pages {
		for _, link := range p.LinksTo {
			if paths[link] {
				continue
			}
			found := false
			for knownPath := range paths {
				if strings.HasPrefix(knownPath, link+"/:") {
					found = true
					break
				}
			}
			if !found {
				errs = append(errs, fmt.Sprintf("page %s links_to %s which is not in the blueprint", p.Path, link))
			}
		}
	}

	// Verify nav_items.
	for _, nav := range bp.NavItems {
		if paths[nav] {
			continue
		}
		found := false
		for knownPath := range paths {
			if strings.HasPrefix(knownPath, nav+"/:") {
				found = true
				break
			}
		}
		if !found {
			errs = append(errs, fmt.Sprintf("nav_items references %s which is not in the blueprint", nav))
		}
	}

	// Validate endpoint specs.
	validActions := map[string]bool{
		"create_api": true, "create_auth": true, "create_websocket": true,
		"create_stream": true, "create_upload": true,
	}
	for i, ep := range bp.Endpoints {
		if !validActions[ep.Action] {
			errs = append(errs, fmt.Sprintf("endpoints[%d].action %q is not valid", i, ep.Action))
		}
		if ep.Path == "" {
			errs = append(errs, fmt.Sprintf("endpoints[%d].path is required", i))
		}
	}

	// Color scheme validation.
	if bp.ColorScheme.Primary == "" || bp.ColorScheme.Background == "" || bp.ColorScheme.Text == "" {
		errs = append(errs, "color_scheme must include at least primary, background, and text")
	}

	// Typography validation.
	if bp.Typography.BodyFont == "" {
		errs = append(errs, "typography must include at least body_font")
	}
	if bp.Typography.Scale != "" {
		if s, err := strconv.ParseFloat(bp.Typography.Scale, 64); err != nil || s < 1.0 || s > 2.0 {
			errs = append(errs, fmt.Sprintf("typography scale must be between 1.0 and 2.0, got %q", bp.Typography.Scale))
		}
	}

	// Data table validation.
	for i, t := range bp.DataTables {
		if t.Name == "" {
			errs = append(errs, fmt.Sprintf("data_tables[%d].name is required", i))
			continue
		}
		colNames := make(map[string]bool, len(t.Columns))
		nonSystem := 0
		for _, c := range t.Columns {
			if colNames[c.Name] {
				errs = append(errs, fmt.Sprintf("table %s has duplicate column name: %s", t.Name, c.Name))
			}
			colNames[c.Name] = true
			if c.Name != "id" && c.Name != "created_at" && c.Name != "updated_at" {
				nonSystem++
			}
		}
		if nonSystem == 0 && len(t.Columns) > 0 {
			errs = append(errs, fmt.Sprintf("table %s has no non-system columns", t.Name))
		}
	}

	return errs
}

// --- Pipeline state DB operations ---

// LoadPipelineState loads the singleton pipeline_state row.
func LoadPipelineState(d *sql.DB) (*PipelineState, error) {
	var s PipelineState
	var analysisJSON, blueprintJSON, lastError, pauseReason, updateDesc sql.NullString
	var startedAt, updatedAt sql.NullString

	err := d.QueryRow(`SELECT stage, plan_json, blueprint_json, current_page_index, error_count,
		last_error, paused, pause_reason, COALESCE(update_description, ''), started_at, updated_at
		FROM pipeline_state WHERE id = 1`).Scan(
		&s.Stage, &analysisJSON, &blueprintJSON, &s.CurrentPageIndex, &s.ErrorCount,
		&lastError, &s.Paused, &pauseReason, &updateDesc, &startedAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("load pipeline state: %w", err)
	}

	s.AnalysisJSON = analysisJSON.String
	s.BlueprintJSON = blueprintJSON.String
	s.LastError = lastError.String
	s.PauseReason = pauseReason.String
	s.UpdateDescription = updateDesc.String
	if startedAt.Valid {
		s.StartedAt, _ = time.Parse("2006-01-02 15:04:05", startedAt.String)
	}
	if updatedAt.Valid {
		s.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt.String)
	}

	return &s, nil
}

// SavePipelineState persists the full pipeline state.
func SavePipelineState(sdb *db.SiteDB, s *PipelineState) error {
	_, err := sdb.ExecWrite(`UPDATE pipeline_state SET
		stage = ?, plan_json = ?, blueprint_json = ?, current_page_index = ?,
		error_count = ?, last_error = ?, paused = ?,
		pause_reason = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		s.Stage, s.AnalysisJSON, s.BlueprintJSON, s.CurrentPageIndex,
		s.ErrorCount, s.LastError, s.Paused, s.PauseReason,
	)
	return err
}

// AdvanceStage moves the pipeline to the next stage.
func AdvanceStage(sdb *db.SiteDB, stage PipelineStage) error {
	_, err := sdb.ExecWrite(`UPDATE pipeline_state SET stage = ?, error_count = 0, updated_at = CURRENT_TIMESTAMP WHERE id = 1`, stage)
	return err
}

// ResetPipeline resets pipeline state for a fresh build.
func ResetPipeline(sdb *db.SiteDB) error {
	_, err := sdb.ExecWrite(`UPDATE pipeline_state SET
		stage = 'ANALYZE', plan_json = NULL, blueprint_json = NULL, current_page_index = 0,
		error_count = 0, last_error = NULL, paused = 0,
		pause_reason = NULL, started_at = CURRENT_TIMESTAMP,
		updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`)
	return err
}

// PausePipeline pauses the pipeline with a reason.
func PausePipeline(sdb *db.SiteDB, reason string) error {
	_, err := sdb.ExecWrite(`UPDATE pipeline_state SET paused = 1, pause_reason = ?,
		updated_at = CURRENT_TIMESTAMP WHERE id = 1`, reason)
	return err
}

// ResumePipeline unpauses the pipeline.
func ResumePipeline(sdb *db.SiteDB) error {
	_, err := sdb.ExecWrite(`UPDATE pipeline_state SET paused = 0, pause_reason = NULL,
		updated_at = CURRENT_TIMESTAMP WHERE id = 1`)
	return err
}

// IncrementErrorCount increments the error counter and records the error.
func IncrementErrorCount(sdb *db.SiteDB, errMsg string) (int, error) {
	_, err := sdb.ExecWrite(`UPDATE pipeline_state SET error_count = error_count + 1,
		last_error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1`, errMsg)
	if err != nil {
		return 0, err
	}
	var count int
	err = sdb.QueryRow(`SELECT error_count FROM pipeline_state WHERE id = 1`).Scan(&count)
	return count, err
}

// --- Stage log operations ---

// LogStageStart creates a new stage_log entry and returns its ID.
func LogStageStart(sdb *db.SiteDB, stage PipelineStage) (int64, error) {
	result, err := sdb.ExecWrite(`INSERT INTO stage_log (stage, status) VALUES (?, 'started')`, stage)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// LogStageComplete marks a stage_log entry as completed with metrics.
func LogStageComplete(sdb *db.SiteDB, logID int64, inputTokens, outputTokens, toolCalls int, duration time.Duration) error {
	_, err := sdb.ExecWrite(`UPDATE stage_log SET status = 'completed',
		input_tokens = ?, output_tokens = ?, tool_calls = ?,
		duration_ms = ?, completed_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		inputTokens, outputTokens, toolCalls, duration.Milliseconds(), logID,
	)
	return err
}

// LogStageError marks a stage_log entry as failed.
func LogStageError(sdb *db.SiteDB, logID int64, errMsg string) error {
	_, err := sdb.ExecWrite(`UPDATE stage_log SET status = 'failed',
		error_message = ?, completed_at = CURRENT_TIMESTAMP
		WHERE id = ?`, errMsg, logID)
	return err
}
