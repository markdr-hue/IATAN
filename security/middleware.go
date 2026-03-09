/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package security

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const ClaimsKey contextKey = "claims"

// Authenticator is chi middleware that validates JWT from Authorization header
// or from the "token" query parameter (needed for SSE/EventSource which cannot set headers).
func (j *JWTManager) Authenticator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var tokenString string

		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
			if tokenString == authHeader {
				http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
				return
			}
		} else if cookie, err := r.Cookie(AuthCookieName); err == nil && cookie.Value != "" {
			tokenString = cookie.Value
		} else if qToken := r.URL.Query().Get("token"); qToken != "" {
			// Query-param tokens are only intended for SSE/EventSource which
			// cannot set custom headers. Browsers log query strings in history
			// and referrer headers, so avoid using this for non-SSE requests.
			tokenString = qToken
		} else {
			http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
			return
		}

		claims, err := j.Validate(tokenString)
		if err != nil {
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), ClaimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole middleware checks that the authenticated user has the required role
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := r.Context().Value(ClaimsKey).(*Claims)
			if !ok || claims.Role != role {
				http.Error(w, `{"error":"insufficient permissions"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetClaims extracts claims from request context
func GetClaims(r *http.Request) *Claims {
	claims, _ := r.Context().Value(ClaimsKey).(*Claims)
	return claims
}
