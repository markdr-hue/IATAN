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
	Path       string   `json:"path"`
	Title      string   `json:"title"`
	Purpose    string   `json:"purpose"`
	Sections   []string `json:"sections"`
	LinksTo    []string `json:"links_to,omitempty"`
	NeedsData  bool     `json:"needs_data"`
	DataTables []string `json:"data_tables,omitempty"`
	PageAssets []string `json:"page_assets,omitempty"`
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
	SeedData bool        `json:"seed_data"`
}

type PlanQuestion struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
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
	Stage            PipelineStage
	PlanJSON         string
	CurrentPageIndex int
	ErrorCount       int
	LastError        string
	Paused           bool
	PauseReason      string
	StartedAt        time.Time
	UpdatedAt        time.Time
}

var jsonFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*\n?(.*?)```")

// ParseSitePlan parses a SitePlan from raw LLM output, stripping markdown fences.
func ParseSitePlan(raw string) (*SitePlan, error) {
	raw = strings.TrimSpace(raw)

	// Strip markdown code fences if present.
	if matches := jsonFenceRe.FindStringSubmatch(raw); len(matches) > 1 {
		raw = strings.TrimSpace(matches[1])
	}

	var plan SitePlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil, fmt.Errorf("invalid plan JSON: %w", err)
	}
	return &plan, nil
}

// ParsePlanPatch parses a PlanPatch from raw LLM output.
func ParsePlanPatch(raw string) (*PlanPatch, error) {
	raw = strings.TrimSpace(raw)
	if matches := jsonFenceRe.FindStringSubmatch(raw); len(matches) > 1 {
		raw = strings.TrimSpace(matches[1])
	}
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
	for _, p := range plan.Pages {
		for _, link := range p.LinksTo {
			if !paths[link] {
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
	}

	// Typography validation.
	if plan.Typography.BodyFont == "" {
		errs = append(errs, "typography must include at least body_font")
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
	var planJSON, lastError, pauseReason sql.NullString
	var startedAt, updatedAt sql.NullString

	err := db.QueryRow(`SELECT stage, plan_json, current_page_index, error_count,
		last_error, paused, pause_reason, started_at, updated_at
		FROM pipeline_state WHERE id = 1`).Scan(
		&s.Stage, &planJSON, &s.CurrentPageIndex, &s.ErrorCount,
		&lastError, &s.Paused, &pauseReason, &startedAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("load pipeline state: %w", err)
	}

	s.PlanJSON = planJSON.String
	s.LastError = lastError.String
	s.PauseReason = pauseReason.String
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
		stage = ?, plan_json = ?, current_page_index = ?,
		error_count = ?, last_error = ?, paused = ?,
		pause_reason = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1`,
		s.Stage, s.PlanJSON, s.CurrentPageIndex,
		s.ErrorCount, s.LastError, s.Paused, s.PauseReason,
	)
	return err
}

// AdvanceStage moves the pipeline to the next stage.
func AdvanceStage(sdb *db.SiteDB, stage PipelineStage) error {
	_, err := sdb.ExecWrite(`UPDATE pipeline_state SET stage = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1`, stage)
	return err
}

// ResetPipeline resets pipeline state for a fresh build.
func ResetPipeline(sdb *db.SiteDB) error {
	_, err := sdb.ExecWrite(`UPDATE pipeline_state SET
		stage = 'PLAN', plan_json = NULL, current_page_index = 0,
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
