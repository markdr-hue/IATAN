/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package admin

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/markdr-hue/IATAN/events"
)

// QuestionsHandler handles questions listing and answering.
type QuestionsHandler struct {
	deps *Deps
}

type question struct {
	ID        int       `json:"id"`
	SiteID    int       `json:"site_id"`
	Question  string    `json:"question"`
	Answer    *string   `json:"answer"`
	Status    string    `json:"status"`
	Type      *string   `json:"type,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// List returns questions across all sites, optionally filtered by status.
func (h *QuestionsHandler) List(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")

	// Get all site IDs from global DB.
	siteRows, err := h.deps.DB.Query("SELECT id FROM sites ORDER BY id")
	if err != nil {
		writeJSON(w, http.StatusOK, []question{})
		return
	}
	defer siteRows.Close()
	var siteIDs []int
	for siteRows.Next() {
		var id int
		if err := siteRows.Scan(&id); err == nil {
			siteIDs = append(siteIDs, id)
		}
	}

	var questions []question
	for _, siteID := range siteIDs {
		siteDB, err := h.deps.SiteDBManager.Open(siteID)
		if err != nil {
			continue
		}
		var rows *sql.Rows
		if statusFilter != "" {
			rows, err = siteDB.Query(
				`SELECT q.id, q.question,
				        (SELECT a.answer FROM answers a WHERE a.question_id = q.id ORDER BY a.created_at DESC LIMIT 1),
				        q.status, q.type, q.created_at, q.created_at
				 FROM questions q WHERE q.status = ? ORDER BY q.created_at DESC`,
				statusFilter,
			)
		} else {
			rows, err = siteDB.Query(
				`SELECT q.id, q.question,
				        (SELECT a.answer FROM answers a WHERE a.question_id = q.id ORDER BY a.created_at DESC LIMIT 1),
				        q.status, q.type, q.created_at, q.created_at
				 FROM questions q ORDER BY q.created_at DESC`,
			)
		}
		if err != nil {
			continue
		}
		for rows.Next() {
			var q question
			q.SiteID = siteID
			if err := rows.Scan(&q.ID, &q.Question, &q.Answer, &q.Status, &q.Type, &q.CreatedAt, &q.UpdatedAt); err != nil {
				continue
			}
			questions = append(questions, q)
		}
		rows.Close()
	}

	if questions == nil {
		questions = []question{}
	}

	writeJSON(w, http.StatusOK, questions)
}

// ListBySite returns questions for a specific site, optionally filtered by status.
func (h *QuestionsHandler) ListBySite(w http.ResponseWriter, r *http.Request) {
	siteID, siteDB := requireSiteDB(w, r, h.deps.SiteDBManager)
	if siteDB == nil {
		return
	}

	statusFilter := r.URL.Query().Get("status")

	var (
		rows *sql.Rows
		err  error
	)

	if statusFilter != "" {
		rows, err = siteDB.Query(
			`SELECT q.id, q.question, q.context, q.options, q.urgency, q.status, q.created_at, q.type, q.secret_name,
			        (SELECT a.answer FROM answers a WHERE a.question_id = q.id ORDER BY a.created_at DESC LIMIT 1) as answer
			 FROM questions q WHERE q.status = ? ORDER BY q.created_at DESC`,
			statusFilter,
		)
	} else {
		rows, err = siteDB.Query(
			`SELECT q.id, q.question, q.context, q.options, q.urgency, q.status, q.created_at, q.type, q.secret_name,
			        (SELECT a.answer FROM answers a WHERE a.question_id = q.id ORDER BY a.created_at DESC LIMIT 1) as answer
			 FROM questions q ORDER BY q.created_at DESC`,
		)
	}

	if err != nil {
		writeJSON(w, http.StatusOK, []siteQuestion{})
		return
	}
	defer rows.Close()

	var questions []siteQuestion
	for rows.Next() {
		var q siteQuestion
		q.SiteID = siteID
		if err := rows.Scan(&q.ID, &q.Question, &q.Context, &q.Options, &q.Urgency, &q.Status, &q.CreatedAt, &q.Type, &q.SecretName, &q.Answer); err != nil {
			continue
		}
		questions = append(questions, q)
	}

	if questions == nil {
		questions = []siteQuestion{}
	}

	writeJSON(w, http.StatusOK, questions)
}

type siteQuestion struct {
	ID         int       `json:"id"`
	SiteID     int       `json:"site_id"`
	Question   string    `json:"question"`
	Context    *string   `json:"context"`
	Options    *string   `json:"options"`
	Urgency    string    `json:"urgency"`
	Status     string    `json:"status"`
	Answer     *string   `json:"answer"`
	CreatedAt  time.Time `json:"created_at"`
	Type       *string   `json:"type,omitempty"`
	SecretName *string   `json:"secret_name,omitempty"`
}

type answerRequest struct {
	Answer string `json:"answer"`
	SiteID int    `json:"site_id"`
}

// Answer provides an answer to a pending question.
func (h *QuestionsHandler) Answer(w http.ResponseWriter, r *http.Request) {
	questionID, err := strconv.Atoi(chi.URLParam(r, "questionID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid question ID")
		return
	}

	var req answerRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Answer == "" {
		writeError(w, http.StatusBadRequest, "answer is required")
		return
	}

	// Resolve which site DB contains this question.
	siteID := req.SiteID
	if siteID == 0 {
		// Search across all site DBs for the question.
		for _, id := range h.deps.SiteDBManager.OpenSiteIDs() {
			sdb, err := h.deps.SiteDBManager.Open(id)
			if err != nil {
				continue
			}
			var exists int
			if sdb.QueryRow("SELECT 1 FROM questions WHERE id = ?", questionID).Scan(&exists) == nil {
				siteID = id
				break
			}
		}
	}
	if siteID == 0 {
		writeError(w, http.StatusNotFound, "question not found")
		return
	}

	siteDB, err := h.deps.SiteDBManager.Open(siteID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open site database")
		return
	}

	// Check if this is a secret-type question.
	var qType, secretName sql.NullString
	_ = siteDB.QueryRow(
		"SELECT type, secret_name FROM questions WHERE id = ?", questionID,
	).Scan(&qType, &secretName)

	if qType.Valid && qType.String == "secret" && secretName.Valid && secretName.String != "" {
		// Encrypt and store the secret value.
		encrypted, encErr := h.deps.Encryptor.Encrypt(req.Answer)
		if encErr != nil {
			h.deps.Logger.Error("failed to encrypt secret", "error", encErr)
			writeError(w, http.StatusInternalServerError, "failed to encrypt secret")
			return
		}
		_, _ = siteDB.ExecWrite(
			`INSERT INTO secrets (name, value_encrypted, updated_at)
			 VALUES (?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(name) DO UPDATE SET value_encrypted = excluded.value_encrypted, updated_at = CURRENT_TIMESTAMP`,
			secretName.String, encrypted,
		)
		// Replace the answer text with a safe reference (don't store the actual secret).
		req.Answer = fmt.Sprintf("Secret '%s' configured", secretName.String)

		if h.deps.Bus != nil {
			h.deps.Bus.Publish(events.NewEvent(events.EventSecretStored, siteID, map[string]interface{}{
				"name":        secretName.String,
				"question_id": questionID,
			}))
		}
	}

	// Update question status
	_, err = siteDB.ExecWrite(
		"UPDATE questions SET status = 'answered' WHERE id = ?",
		questionID,
	)
	if err != nil {
		h.deps.Logger.Error("failed to answer question", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to answer question")
		return
	}

	// Insert answer record
	_, _ = siteDB.ExecWrite(
		"INSERT INTO answers (question_id, answer) VALUES (?, ?)",
		questionID, req.Answer,
	)

	if h.deps.Bus != nil {
		h.deps.Bus.Publish(events.NewEvent(events.EventQuestionAnswered, siteID, map[string]interface{}{
			"question_id": questionID,
			"answer":      req.Answer,
		}))
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":     questionID,
		"status": "answered",
	})
}
