/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package brain

import (
	"database/sql"
	"time"

	"github.com/markdr-hue/IATAN/db"
	"github.com/markdr-hue/IATAN/events"
	"github.com/markdr-hue/IATAN/llm"
	"github.com/markdr-hue/IATAN/security"
	"github.com/markdr-hue/IATAN/tools"
)

// BrainState represents the current state of a brain worker.
type BrainState string

const (
	StateIdle       BrainState = "idle"
	StateBuilding   BrainState = "building"
	StateMonitoring BrainState = "monitoring"
	StatePaused     BrainState = "paused"
	StateError      BrainState = "error"
)

// PipelineStage represents a stage in the deterministic build pipeline.
type PipelineStage string

const (
	StagePlan       PipelineStage = "PLAN"
	StageDesign     PipelineStage = "DESIGN"
	StageDataLayer  PipelineStage = "DATA_LAYER"
	StageBuildPages PipelineStage = "BUILD_PAGES"
	StageReview     PipelineStage = "REVIEW"
	StageComplete   PipelineStage = "COMPLETE"
	StageMonitoring PipelineStage = "MONITORING"
	StageUpdatePlan PipelineStage = "UPDATE_PLAN"
)

// BrainCommand is a message sent to a brain worker to trigger an action.
type BrainCommand struct {
	Type    string                 `json:"type"`
	SiteID  int                    `json:"site_id"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

// Command type constants.
const (
	CommandWake          = "wake"
	CommandChat          = "chat"
	CommandModeChange    = "mode_change"
	CommandShutdown      = "shutdown"
	CommandScheduledTask = "scheduled_task"
	CommandUpdate        = "update" // trigger incremental update
)

// Deps holds shared dependencies injected into pipeline workers.
type Deps struct {
	DB              *sql.DB
	SiteDBManager   *db.SiteDBManager
	Encryptor       *security.Encryptor
	LLMRegistry     *llm.Registry
	ToolRegistry    *tools.Registry
	ToolExecutor    *tools.Executor
	Bus             *events.Bus
	ProviderFactory llm.ProviderFactory

	// Monitoring interval overrides (zero means use built-in defaults).
	MonitoringBase time.Duration
	MonitoringMax  time.Duration
}
