/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package brain

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/markdr-hue/IATAN/db"
	"github.com/markdr-hue/IATAN/internal/cron"
)

const schedulerTickInterval = 30 * time.Second

// Scheduler checks for due scheduled tasks and dispatches them to brain workers.
type Scheduler struct {
	siteDBMgr *db.SiteDBManager
	mgr       *BrainManager
	logger    *slog.Logger
}

// NewScheduler creates a scheduler that checks for due tasks.
func NewScheduler(siteDBMgr *db.SiteDBManager, mgr *BrainManager) *Scheduler {
	return &Scheduler{
		siteDBMgr: siteDBMgr,
		mgr:       mgr,
		logger:    slog.With("component", "scheduler"),
	}
}

// Run starts the scheduler loop. It should be called in its own goroutine.
func (s *Scheduler) Run(ctx context.Context) {
	s.logger.Info("task scheduler started")
	defer s.logger.Info("task scheduler stopped")

	// Initialize next_run for tasks across all open site DBs.
	s.initializeNextRun()

	ticker := time.NewTicker(schedulerTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkDueTasks(ctx)
		}
	}
}

// initializeNextRun sets next_run for any enabled tasks that have a NULL next_run.
// Iterates over all open site databases.
func (s *Scheduler) initializeNextRun() {
	for _, siteID := range s.siteDBMgr.OpenSiteIDs() {
		siteDB := s.siteDBMgr.Get(siteID)
		if siteDB == nil {
			continue
		}
		s.initializeNextRunForSite(siteDB)
	}
}

func (s *Scheduler) initializeNextRunForSite(siteDB *db.SiteDB) {
	rows, err := siteDB.Query(
		`SELECT id, cron_expression, interval_seconds FROM scheduled_tasks
		 WHERE is_enabled = 1 AND next_run IS NULL`,
	)
	if err != nil {
		s.logger.Error("scheduler: failed to query uninitialized tasks", "site_id", siteDB.SiteID, "error", err)
		return
	}
	defer rows.Close()

	now := time.Now()
	for rows.Next() {
		var id int
		var cronExpr sql.NullString
		var intervalSec sql.NullInt64

		if err := rows.Scan(&id, &cronExpr, &intervalSec); err != nil {
			continue
		}

		var nextRun time.Time
		if cronExpr.Valid && cronExpr.String != "" {
			nextRun = cron.NextTime(cronExpr.String, now)
		} else if intervalSec.Valid && intervalSec.Int64 > 0 {
			nextRun = now.Add(time.Duration(intervalSec.Int64) * time.Second)
		} else {
			continue
		}

		if _, err := siteDB.ExecWrite(
			"UPDATE scheduled_tasks SET next_run = ? WHERE id = ?",
			nextRun, id,
		); err != nil {
			s.logger.Error("scheduler: failed to initialize next_run", "site_id", siteDB.SiteID, "task_id", id, "error", err)
			continue
		}
		s.logger.Info("initialized next_run for task", "site_id", siteDB.SiteID, "task_id", id, "next_run", nextRun)
	}
}

// checkDueTasks finds tasks where next_run <= now across all site DBs and dispatches them.
func (s *Scheduler) checkDueTasks(ctx context.Context) {
	now := time.Now()

	for _, siteID := range s.siteDBMgr.OpenSiteIDs() {
		siteDB := s.siteDBMgr.Get(siteID)
		if siteDB == nil {
			continue
		}
		s.checkDueTasksForSite(ctx, siteDB, siteID, now)
	}
}

func (s *Scheduler) checkDueTasksForSite(ctx context.Context, siteDB *db.SiteDB, siteID int, now time.Time) {
	rows, err := siteDB.Query(
		`SELECT id, name, prompt, cron_expression, interval_seconds
		 FROM scheduled_tasks
		 WHERE is_enabled = 1 AND next_run IS NOT NULL AND next_run <= ?`,
		now,
	)
	if err != nil {
		s.logger.Error("scheduler: failed to query due tasks", "site_id", siteID, "error", err)
		return
	}
	defer rows.Close()

	type dueTask struct {
		id          int
		name        string
		prompt      string
		cronExpr    sql.NullString
		intervalSec sql.NullInt64
	}

	var tasks []dueTask
	for rows.Next() {
		var t dueTask
		if err := rows.Scan(&t.id, &t.name, &t.prompt, &t.cronExpr, &t.intervalSec); err != nil {
			s.logger.Error("scheduler: failed to scan task", "site_id", siteID, "error", err)
			continue
		}
		tasks = append(tasks, t)
	}
	for _, t := range tasks {
		s.logger.Info("dispatching scheduled task", "task_id", t.id, "task_name", t.name, "site_id", siteID)

		// Record the task run.
		runResult, err := siteDB.ExecWrite(
			"INSERT INTO task_runs (task_id, status) VALUES (?, 'running')",
			t.id,
		)
		var runID int64
		if err == nil {
			runID, _ = runResult.LastInsertId()
		}

		// Send command to brain worker.
		err = s.mgr.SendCommand(siteID, BrainCommand{
			Type:   CommandScheduledTask,
			SiteID: siteID,
			Payload: map[string]interface{}{
				"prompt":  t.prompt,
				"task_id": t.id,
				"run_id":  runID,
			},
		})
		if err != nil {
			s.logger.Error("scheduler: failed to dispatch task", "task_id", t.id, "error", err)
			if runID > 0 {
				if _, dbErr := siteDB.ExecWrite(
					"UPDATE task_runs SET status = 'failed', error_message = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?",
					fmt.Sprintf("dispatch error: %v", err), runID,
				); dbErr != nil {
					s.logger.Error("scheduler: failed to update task_run as failed", "run_id", runID, "error", dbErr)
				}
			}
		}

		// Update last_run and compute next_run.
		var nextRun time.Time
		if t.cronExpr.Valid && t.cronExpr.String != "" {
			nextRun = cron.NextTime(t.cronExpr.String, now)
		} else if t.intervalSec.Valid && t.intervalSec.Int64 > 0 {
			nextRun = now.Add(time.Duration(t.intervalSec.Int64) * time.Second)
		} else {
			if _, err := siteDB.ExecWrite("UPDATE scheduled_tasks SET is_enabled = 0, last_run = ? WHERE id = ?", now, t.id); err != nil {
				s.logger.Error("scheduler: failed to disable task", "task_id", t.id, "error", err)
			}
			continue
		}

		if _, err := siteDB.ExecWrite(
			"UPDATE scheduled_tasks SET last_run = ?, next_run = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
			now, nextRun, t.id,
		); err != nil {
			s.logger.Error("scheduler: failed to update next_run", "task_id", t.id, "error", err)
		}
		s.logger.Info("task scheduled next run", "task_id", t.id, "next_run", nextRun)
	}
}
