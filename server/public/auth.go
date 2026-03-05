/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package public

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/markdr-hue/IATAN/security"
)

// authEndpointConfig holds the configuration for auto-generated auth routes.
type authEndpointConfig struct {
	ID             int
	TableName      string
	Path           string
	UsernameColumn string
	PasswordColumn string
	PublicColumns  []string
	JWTExpiryHours int
}

// loadAuthEndpoint loads an auth endpoint configuration from the per-site database.
func (h *Handler) loadAuthEndpoint(siteDB *sql.DB, path string) (*authEndpointConfig, error) {
	var ae authEndpointConfig
	var publicColsJSON string
	err := siteDB.QueryRow(
		"SELECT id, table_name, path, username_column, password_column, public_columns, jwt_expiry_hours FROM auth_endpoints WHERE path = ?",
		path,
	).Scan(&ae.ID, &ae.TableName, &ae.Path, &ae.UsernameColumn, &ae.PasswordColumn, &publicColsJSON, &ae.JWTExpiryHours)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(publicColsJSON), &ae.PublicColumns)
	if ae.JWTExpiryHours == 0 {
		ae.JWTExpiryHours = 24
	}

	// Validate column names to prevent SQL injection via stored config.
	if err := security.ValidateColumnName(ae.UsernameColumn); err != nil {
		return nil, fmt.Errorf("invalid username_column in auth config: %w", err)
	}
	if err := security.ValidateColumnName(ae.PasswordColumn); err != nil {
		return nil, fmt.Errorf("invalid password_column in auth config: %w", err)
	}
	if err := security.ValidateColumnNames(ae.PublicColumns); err != nil {
		return nil, fmt.Errorf("invalid public_columns in auth config: %w", err)
	}

	return &ae, nil
}

// handleAuthRequest handles /api/{path}/register, /api/{path}/login, /api/{path}/me.
// Returns true if the request was handled as an auth endpoint.
func (h *Handler) handleAuthRequest(w http.ResponseWriter, r *http.Request, siteID int, siteDB *sql.DB, fullPath string) bool {
	// Check if the path matches an auth endpoint pattern: {authPath}/{action}
	parts := strings.SplitN(fullPath, "/", 2)
	if len(parts) != 2 {
		return false
	}

	authPath := parts[0]
	action := parts[1]

	// Try to load auth endpoint config.
	ae, err := h.loadAuthEndpoint(siteDB, authPath)
	if err != nil {
		return false // Not an auth endpoint, let DynamicAPI handle it.
	}

	physTable := fmt.Sprintf("site_%d_%s", siteID, ae.TableName)

	switch action {
	case "register":
		if r.Method != http.MethodPost {
			writePublicError(w, http.StatusMethodNotAllowed, "POST required")
			return true
		}
		h.authRegister(w, r, siteID, siteDB, physTable, ae)
		return true
	case "login":
		if r.Method != http.MethodPost {
			writePublicError(w, http.StatusMethodNotAllowed, "POST required")
			return true
		}
		h.authLogin(w, r, siteID, siteDB, physTable, ae)
		return true
	case "me":
		if r.Method != http.MethodGet {
			writePublicError(w, http.StatusMethodNotAllowed, "GET required")
			return true
		}
		h.authMe(w, r, siteID, siteDB, physTable, ae)
		return true
	}

	return false
}

// authRegister handles POST /api/{path}/register.
func (h *Handler) authRegister(w http.ResponseWriter, r *http.Request, siteID int, siteDB *sql.DB, physTable string, ae *authEndpointConfig) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writePublicError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Validate required fields.
	username, ok := body[ae.UsernameColumn].(string)
	if !ok || username == "" {
		writePublicError(w, http.StatusBadRequest, fmt.Sprintf("%s is required", ae.UsernameColumn))
		return
	}
	password, ok := body[ae.PasswordColumn].(string)
	if !ok || password == "" {
		writePublicError(w, http.StatusBadRequest, fmt.Sprintf("%s is required", ae.PasswordColumn))
		return
	}

	// Check if user already exists.
	var existingID int
	err := siteDB.QueryRow(
		fmt.Sprintf("SELECT id FROM %s WHERE %s = ?", physTable, ae.UsernameColumn),
		username,
	).Scan(&existingID)
	if err == nil {
		writePublicError(w, http.StatusConflict, fmt.Sprintf("user with this %s already exists", ae.UsernameColumn))
		return
	}

	// Hash the password.
	hashedPassword, err := security.HashPassword(password)
	if err != nil {
		writePublicError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}
	body[ae.PasswordColumn] = hashedPassword

	// Remove id if provided.
	delete(body, "id")

	// Insert the row — validate column names from request body.
	columns := make([]string, 0, len(body))
	placeholders := make([]string, 0, len(body))
	values := make([]interface{}, 0, len(body))
	for col, val := range body {
		if err := security.ValidateColumnName(col); err != nil {
			writePublicError(w, http.StatusBadRequest, "invalid field name: "+col)
			return
		}
		columns = append(columns, col)
		placeholders = append(placeholders, "?")
		values = append(values, val)
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", physTable, strings.Join(columns, ", "), strings.Join(placeholders, ", "))
	result, err := siteDB.Exec(query, values...)
	if err != nil {
		writePublicError(w, http.StatusBadRequest, "registration failed")
		return
	}

	userID, _ := result.LastInsertId()

	// Generate JWT token.
	token, err := h.generateUserToken(int(userID), username, "user", siteID, ae)
	if err != nil {
		writePublicError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writePublicJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"user_id": userID,
		"token":   token,
	})
}

// authLogin handles POST /api/{path}/login.
func (h *Handler) authLogin(w http.ResponseWriter, r *http.Request, siteID int, siteDB *sql.DB, physTable string, ae *authEndpointConfig) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writePublicError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	username, ok := body[ae.UsernameColumn].(string)
	if !ok || username == "" {
		writePublicError(w, http.StatusBadRequest, fmt.Sprintf("%s is required", ae.UsernameColumn))
		return
	}
	password, ok := body[ae.PasswordColumn].(string)
	if !ok || password == "" {
		writePublicError(w, http.StatusBadRequest, fmt.Sprintf("%s is required", ae.PasswordColumn))
		return
	}

	// Find the user by username.
	var userID int
	var hashedPassword string
	err := siteDB.QueryRow(
		fmt.Sprintf("SELECT id, %s FROM %s WHERE %s = ?", ae.PasswordColumn, physTable, ae.UsernameColumn),
		username,
	).Scan(&userID, &hashedPassword)
	if err != nil {
		writePublicError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Verify password.
	if !security.CheckPassword(password, hashedPassword) {
		writePublicError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Generate JWT token.
	token, err := h.generateUserToken(userID, username, "user", siteID, ae)
	if err != nil {
		writePublicError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writePublicJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"user_id": userID,
		"token":   token,
	})
}

// authMe handles GET /api/{path}/me.
func (h *Handler) authMe(w http.ResponseWriter, r *http.Request, siteID int, siteDB *sql.DB, physTable string, ae *authEndpointConfig) {
	// Extract and validate JWT token.
	claims, err := h.extractUserClaims(r)
	if err != nil {
		writePublicError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Fetch user data (excluding password columns).
	cols := "id, " + ae.UsernameColumn
	if len(ae.PublicColumns) > 0 {
		cols = strings.Join(ae.PublicColumns, ", ")
	}

	query := fmt.Sprintf("SELECT %s FROM %s WHERE id = ?", cols, physTable)
	rows, err := siteDB.Query(query, claims.UserID)
	if err != nil {
		writePublicError(w, http.StatusInternalServerError, "query error")
		return
	}
	defer rows.Close()

	results := h.scanRowsToMaps(rows)
	if len(results) == 0 {
		writePublicError(w, http.StatusNotFound, "user not found")
		return
	}

	writePublicJSON(w, http.StatusOK, results[0])
}

// generateUserToken creates a JWT token for a site user.
func (h *Handler) generateUserToken(userID int, username, role string, siteID int, ae *authEndpointConfig) (string, error) {
	if h.deps.JWTManager == nil {
		return "", fmt.Errorf("JWT manager not configured")
	}
	// Use the site-level JWT manager with the configured expiry.
	_ = siteID // JWT is signed with the global secret; siteID is in context.
	_ = ae     // Expiry could be customized per auth endpoint in the future.
	return h.deps.JWTManager.Generate(userID, username, role)
}

// extractUserClaims extracts and validates JWT claims from the Authorization header.
func (h *Handler) extractUserClaims(r *http.Request) (*security.Claims, error) {
	if h.deps.JWTManager == nil {
		return nil, fmt.Errorf("JWT manager not configured")
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, fmt.Errorf("no authorization header")
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == authHeader {
		return nil, fmt.Errorf("invalid authorization format")
	}

	return h.deps.JWTManager.Validate(token)
}

// validateSiteTokenOrJWT checks if the token is either a valid site API key
// or a valid user JWT token. Returns true if either check passes.
func (h *Handler) validateSiteTokenOrJWT(siteID int, token string) bool {
	if token == "" {
		return false
	}

	// First try: site API key.
	if h.validateSiteToken(siteID, token) {
		return true
	}

	// Second try: user JWT.
	if h.deps.JWTManager != nil {
		_, err := h.deps.JWTManager.Validate(token)
		return err == nil
	}

	return false
}
