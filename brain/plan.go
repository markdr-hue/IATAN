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

// SitePlan is the structured output from the PLAN stage.
type SitePlan struct {
	Architecture   string         `json:"architecture"` // "spa" or "multi-page"
	ColorScheme    ColorScheme    `json:"color_scheme"`
	Typography     Typography     `json:"typography"`
	Pages          []PagePlan     `json:"pages"`
	NeedsDataLayer bool           `json:"needs_data_layer"`
	DataTables     []TablePlan    `json:"data_tables,omitempty"`
	NavItems       []string       `json:"nav_items"`
	DesignNotes    string         `json:"design_notes"`
	Questions      []PlanQuestion `json:"questions,omitempty"`
}

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

type PagePlan struct {
	Path           string   `json:"path"`
	Title          string   `json:"title"`
	Purpose        string   `json:"purpose"`
	Sections       []string `json:"sections"`
	LinksTo        []string `json:"links_to,omitempty"`
	NeedsData      bool     `json:"needs_data"`
	DataTables     []string `json:"data_tables,omitempty"`
	PageAssets     []string `json:"page_assets,omitempty"`
	Layout         string   `json:"layout,omitempty"`          // layout name: "default", "none", "blog", etc.
	ComponentHints []string `json:"component_hints,omitempty"` // e.g. ["hero-cta", "feature-grid", "testimonial-cards"]
}

type ColumnDef struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Primary  bool   `json:"primary,omitempty"`
	Required bool   `json:"required,omitempty"`
}

type TablePlan struct {
	Name     string      `json:"name"`
	Columns  []ColumnDef `json:"columns"`
	HasAPI   bool        `json:"has_api"`
	HasAuth  bool        `json:"has_auth"`
	HasOAuth []string    `json:"has_oauth,omitempty"` // e.g. ["google", "github"]
	Roles    []string    `json:"roles,omitempty"`     // e.g. ["user", "admin"]
	SeedData bool        `json:"seed_data"`
}

type PlanQuestion struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

// PageBlueprint is the BLUEPRINT stage output for a single page.
type PageBlueprint struct {
	Path              string   `json:"path"`
	HTMLSkeleton      string   `json:"html_skeleton"`
	ComponentPatterns []string `json:"component_patterns"`
	ContentNotes      string   `json:"content_notes"`
	DataDisplay       string   `json:"data_display,omitempty"`
}

// SiteBlueprint is the full BLUEPRINT stage output.
type SiteBlueprint struct {
	Pages          []PageBlueprint   `json:"pages"`
	SharedPatterns map[string]string `json:"shared_patterns"`
	ContentStyle   string            `json:"content_style"`
}

// ParseSiteBlueprint parses a SiteBlueprint from raw LLM output.
func ParseSiteBlueprint(raw string) (*SiteBlueprint, error) {
	raw = extractJSON(raw)
	var bp SiteBlueprint
	if err := json.Unmarshal([]byte(raw), &bp); err != nil {
		return nil, fmt.Errorf("invalid blueprint JSON: %w", err)
	}
	return &bp, nil
}

// ValidateBlueprint checks a SiteBlueprint for completeness.
func ValidateBlueprint(bp *SiteBlueprint, plan *SitePlan) []string {
	var errs []string
	if len(bp.Pages) == 0 {
		errs = append(errs, "blueprint has no pages")
		return errs
	}

	bpPaths := make(map[string]bool, len(bp.Pages))
	for _, p := range bp.Pages {
		bpPaths[p.Path] = true
		if p.HTMLSkeleton == "" {
			errs = append(errs, fmt.Sprintf("blueprint page %s has empty html_skeleton", p.Path))
		}
	}

	// Every planned page should have a blueprint entry.
	for _, p := range plan.Pages {
		if !bpPaths[p.Path] {
			errs = append(errs, fmt.Sprintf("blueprint missing page %s", p.Path))
		}
	}

	return errs
}

// BlueprintForPage returns the blueprint entry for a given page path, or nil.
func (bp *SiteBlueprint) BlueprintForPage(path string) *PageBlueprint {
	for i := range bp.Pages {
		if bp.Pages[i].Path == path {
			return &bp.Pages[i]
		}
	}
	return nil
}

// PlanPatch describes incremental changes for an UPDATE flow.
type PlanPatch struct {
	AddPages    []PagePlan  `json:"add_pages,omitempty"`
	ModifyPages []PagePlan  `json:"modify_pages,omitempty"`
	RemovePages []string    `json:"remove_pages,omitempty"`
	UpdateNav   bool        `json:"update_nav"`
	UpdateCSS   bool        `json:"update_css"`
	AddTables   []TablePlan `json:"add_tables,omitempty"`
}

// PipelineState is the singleton row in pipeline_state tracking build progress.
type PipelineState struct {
	Stage             PipelineStage
	PlanJSON          string
	BlueprintJSON     string // JSON-encoded SiteBlueprint from BLUEPRINT_PAGES stage
	CurrentPageIndex  int
	ErrorCount        int
	LastError         string
	Paused            bool
	PauseReason       string
	UpdateDescription string // what the owner wants changed (for UPDATE_PLAN)
	StartedAt         time.Time
	UpdatedAt         time.Time
}

var jsonFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*\n?(.*?)```")

// extractJSON strips markdown code fences and any leading/trailing non-JSON text
// from raw LLM output, returning only the JSON object.
func extractJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 {
		return raw
	}

	// Try regex first (handles clean markdown fences).
	if matches := jsonFenceRe.FindStringSubmatch(raw); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	// Already a JSON object or array — return as-is.
	if raw[0] == '{' || raw[0] == '[' {
		return raw
	}

	// Fallback: find the first { and last } to extract JSON object.
	// Handles cases where the regex fails (backticks inside JSON strings)
	// or the LLM adds prose before/after the JSON.
	if start := strings.Index(raw, "{"); start >= 0 {
		if end := strings.LastIndex(raw, "}"); end > start {
			return raw[start : end+1]
		}
	}

	return raw
}

// ParseSitePlan parses a SitePlan from raw LLM output, stripping markdown fences.
func ParseSitePlan(raw string) (*SitePlan, error) {
	raw = extractJSON(raw)
	var plan SitePlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil, fmt.Errorf("invalid plan JSON: %w", err)
	}
	return &plan, nil
}

// ParsePlanPatch parses a PlanPatch from raw LLM output.
func ParsePlanPatch(raw string) (*PlanPatch, error) {
	raw = extractJSON(raw)
	var patch PlanPatch
	if err := json.Unmarshal([]byte(raw), &patch); err != nil {
		return nil, fmt.Errorf("invalid patch JSON: %w", err)
	}
	return &patch, nil
}

// ValidatePlan checks a SitePlan for structural errors.
func ValidatePlan(plan *SitePlan) []string {
	var errs []string

	if plan.Architecture != "spa" && plan.Architecture != "multi-page" {
		errs = append(errs, fmt.Sprintf("architecture must be 'spa' or 'multi-page', got %q", plan.Architecture))
	}

	if len(plan.Pages) < 2 {
		errs = append(errs, "plan must include at least 2 pages (home + 404)")
	}

	// Build path set for link validation.
	paths := make(map[string]bool, len(plan.Pages))
	for _, p := range plan.Pages {
		if paths[p.Path] {
			errs = append(errs, fmt.Sprintf("duplicate page path: %s", p.Path))
		}
		paths[p.Path] = true
	}

	// Verify homepage exists.
	if !paths["/"] {
		errs = append(errs, "plan must include a homepage at path /")
	}

	// Verify 404 exists.
	if !paths["/404"] {
		errs = append(errs, "plan must include a 404 page at path /404")
	}

	// Verify all links_to targets exist.
	// Allow "/category" to match "/category/:id" (parameterised routes).
	for _, p := range plan.Pages {
		for _, link := range p.LinksTo {
			if paths[link] {
				continue
			}
			// Check if any path is a parameterised version of this link.
			found := false
			for knownPath := range paths {
				if strings.HasPrefix(knownPath, link+"/") {
					found = true
					break
				}
			}
			if !found {
				errs = append(errs, fmt.Sprintf("page %s links_to %s which is not in the plan", p.Path, link))
			}
		}
	}

	// Verify nav_items reference valid paths.
	for _, nav := range plan.NavItems {
		if !paths[nav] {
			errs = append(errs, fmt.Sprintf("nav_items references %s which is not in the plan", nav))
		}
	}

	// Verify data tables if needed.
	if plan.NeedsDataLayer {
		if len(plan.DataTables) == 0 {
			errs = append(errs, "needs_data_layer is true but no data_tables defined")
		}
	}

	// Color scheme validation.
	if plan.ColorScheme.Primary == "" || plan.ColorScheme.Background == "" || plan.ColorScheme.Text == "" {
		errs = append(errs, "color_scheme must include at least primary, background, and text")
	} else {
		// WCAG contrast check at plan time (catch bad schemes early).
		errs = append(errs, validateColorContrast(plan)...)
	}

	// Typography validation.
	if plan.Typography.BodyFont == "" {
		errs = append(errs, "typography must include at least body_font")
	}
	if plan.Typography.Scale != "" {
		if s, err := strconv.ParseFloat(plan.Typography.Scale, 64); err != nil || s < 1.0 || s > 2.0 {
			errs = append(errs, fmt.Sprintf("typography scale must be a number between 1.0 and 2.0, got %q", plan.Typography.Scale))
		}
	}

	// Data table column validation.
	for _, t := range plan.DataTables {
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

// MarshalPlan serializes a SitePlan to JSON.
func MarshalPlan(plan *SitePlan) (string, error) {
	b, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// --- Pipeline state DB operations ---

// LoadPipelineState loads the singleton pipeline_state row.
func LoadPipelineState(db *sql.DB) (*PipelineState, error) {
	var s PipelineState
	var planJSON, blueprintJSON, lastError, pauseReason, updateDesc sql.NullString
	var startedAt, updatedAt sql.NullString

	err := db.QueryRow(`SELECT stage, plan_json, COALESCE(blueprint_json, ''), current_page_index, error_count,
		last_error, paused, pause_reason, COALESCE(update_description, ''), started_at, updated_at
		FROM pipeline_state WHERE id = 1`).Scan(
		&s.Stage, &planJSON, &blueprintJSON, &s.CurrentPageIndex, &s.ErrorCount,
		&lastError, &s.Paused, &pauseReason, &updateDesc, &startedAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("load pipeline state: %w", err)
	}

	s.PlanJSON = planJSON.String
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
		s.Stage, s.PlanJSON, s.BlueprintJSON, s.CurrentPageIndex,
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
		stage = 'PLAN', plan_json = NULL, blueprint_json = NULL, current_page_index = 0,
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
