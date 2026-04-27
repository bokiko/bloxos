package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
)

type UserRole string

const (
	RoleAdmin    UserRole = "admin"
	RoleOperator UserRole = "operator"
	RoleViewer   UserRole = "viewer"
)

const (
	scopeAuthSelf         = "auth.self"
	scopeFleetRead        = "fleet.read"
	scopeFleetControl     = "fleet.control"
	scopeFleetMetadata    = "fleet.metadata"
	scopeFleetAdmin       = "fleet.admin"
	scopeAlertsRead       = "alerts.read"
	scopeAlertsAck        = "alerts.ack"
	scopeAlertsAdmin      = "alerts.admin"
	scopeAPIMachinesRead  = "api_machines.read"
	scopeAPIMachinesCtrl  = "api_machines.control"
	scopeAPIMachinesAdmin = "api_machines.admin"
	scopeTokensAdmin      = "install_tokens.admin"
	scopeUsersAdmin       = "users.admin"
	scopeBrandingAdmin    = "branding.admin"
)

const authClaimsContextKey = "auth_claims"

type requestAuthClaims struct {
	UserID   string
	Username string
}

var roleScopes = map[UserRole][]string{
	RoleAdmin: {
		scopeAuthSelf,
		scopeFleetRead,
		scopeFleetControl,
		scopeFleetMetadata,
		scopeFleetAdmin,
		scopeAlertsRead,
		scopeAlertsAck,
		scopeAlertsAdmin,
		scopeAPIMachinesRead,
		scopeAPIMachinesCtrl,
		scopeAPIMachinesAdmin,
		scopeTokensAdmin,
		scopeUsersAdmin,
		scopeBrandingAdmin,
	},
	RoleOperator: {
		scopeAuthSelf,
		scopeFleetRead,
		scopeFleetControl,
		scopeFleetMetadata,
		scopeAlertsRead,
		scopeAlertsAck,
		scopeAPIMachinesRead,
		scopeAPIMachinesCtrl,
	},
	RoleViewer: {
		scopeAuthSelf,
		scopeFleetRead,
		scopeAlertsRead,
		scopeAPIMachinesRead,
	},
}

var routeScopeRequirements = map[string]string{
	routeScopeKey(http.MethodGet, "/api/events"):                               scopeFleetRead,
	routeScopeKey(http.MethodGet, "/api/inventory"):                            scopeFleetRead,
	routeScopeKey(http.MethodGet, "/api/machines"):                             scopeFleetRead,
	routeScopeKey(http.MethodGet, "/api/machines/:id"):                         scopeFleetRead,
	routeScopeKey(http.MethodGet, "/api/machines/:id/services"):                scopeFleetRead,
	routeScopeKey(http.MethodGet, "/api/machines/:id/containers"):              scopeFleetRead,
	routeScopeKey(http.MethodGet, "/api/machines/:id/metrics/history"):         scopeFleetRead,
	routeScopeKey(http.MethodPost, "/api/machines/:id/command"):                scopeFleetControl,
	routeScopeKey(http.MethodPost, "/api/machines/:id/refresh"):                scopeFleetControl,
	routeScopeKey(http.MethodPost, "/api/refresh"):                             scopeFleetControl,
	routeScopeKey(http.MethodGet, "/api/versions"):                             scopeFleetRead,
	routeScopeKey(http.MethodPost, "/api/versions/pause"):                      scopeFleetAdmin,
	routeScopeKey(http.MethodPost, "/api/versions/resume"):                     scopeFleetAdmin,
	routeScopeKey(http.MethodPut, "/api/machines/:id/tags"):                    scopeFleetMetadata,
	routeScopeKey(http.MethodDelete, "/api/machines/:id"):                      scopeFleetAdmin,
	routeScopeKey(http.MethodPost, "/api/machines/:id/terminal"):               scopeFleetControl,
	routeScopeKey(http.MethodDelete, "/api/machines/:id/terminal/:session_id"): scopeFleetControl,
	routeScopeKey(http.MethodGet, "/api/alerts"):                               scopeAlertsRead,
	routeScopeKey(http.MethodGet, "/api/alerts/active/count"):                  scopeAlertsRead,
	routeScopeKey(http.MethodPost, "/api/alerts/:id/acknowledge"):              scopeAlertsAck,
	routeScopeKey(http.MethodGet, "/api/alert-rules"):                          scopeAlertsRead,
	routeScopeKey(http.MethodPut, "/api/alert-rules/:id"):                      scopeAlertsAdmin,
	routeScopeKey(http.MethodPost, "/api/auth/change-password"):                scopeAuthSelf,
	routeScopeKey(http.MethodPost, "/api/auth/change-pin"):                     scopeAuthSelf,
	routeScopeKey(http.MethodPost, "/api/auth/sse-token"):                      scopeAuthSelf,
	routeScopeKey(http.MethodPost, "/api/tokens"):                              scopeTokensAdmin,
	routeScopeKey(http.MethodPost, "/api/bulk/command"):                        scopeFleetControl,
	routeScopeKey(http.MethodGet, "/api/api-machines"):                         scopeAPIMachinesRead,
	routeScopeKey(http.MethodPost, "/api/api-machines"):                        scopeAPIMachinesAdmin,
	routeScopeKey(http.MethodPatch, "/api/api-machines/:id"):                   scopeAPIMachinesAdmin,
	routeScopeKey(http.MethodDelete, "/api/api-machines/:id"):                  scopeAPIMachinesAdmin,
	routeScopeKey(http.MethodPost, "/api/api-machines/:id/poll"):               scopeAPIMachinesCtrl,
	routeScopeKey(http.MethodGet, "/api/users"):                                scopeUsersAdmin,
	routeScopeKey(http.MethodPost, "/api/users"):                               scopeUsersAdmin,
	routeScopeKey(http.MethodPatch, "/api/users/:id"):                          scopeUsersAdmin,
	routeScopeKey(http.MethodDelete, "/api/users/:id"):                         scopeUsersAdmin,

	// Phase 10 — branding (admin) and per-user theme prefs.
	routeScopeKey(http.MethodPatch, "/api/branding"):                           scopeBrandingAdmin,
	routeScopeKey(http.MethodPost, "/api/branding/logo"):                       scopeBrandingAdmin,
	routeScopeKey(http.MethodPost, "/api/branding/favicon"):                    scopeBrandingAdmin,
	routeScopeKey(http.MethodDelete, "/api/branding/:kind"):                    scopeBrandingAdmin,
	routeScopeKey(http.MethodGet, "/api/me/theme"):                             scopeAuthSelf,
	routeScopeKey(http.MethodPatch, "/api/me/theme"):                           scopeAuthSelf,
}

func permissionMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		path := c.Path()
		if path == "" {
			return next(c)
		}

		requiredScope, ok := routeScopeRequirements[routeScopeKey(c.Request().Method, path)]
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "missing RBAC scope mapping"})
		}

		claims, ok := authClaimsFromContext(c)
		if !ok || claims.UserID == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
		}

		role, err := lookupUserRole(claims.UserID)
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "user not found"})
		}
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		if !roleHasScope(role, requiredScope) {
			return c.JSON(http.StatusForbidden, map[string]string{
				"error":          "insufficient_scope",
				"required_scope": requiredScope,
				"role":           string(role),
			})
		}

		return next(c)
	}
}

func setAuthClaimsOnContext(c echo.Context, userID, username string) {
	c.Set(authClaimsContextKey, requestAuthClaims{
		UserID:   userID,
		Username: username,
	})
}

func authClaimsFromContext(c echo.Context) (requestAuthClaims, bool) {
	claims, ok := c.Get(authClaimsContextKey).(requestAuthClaims)
	return claims, ok
}

func routeScopeKey(method, path string) string {
	return method + " " + path
}

func lookupUserRole(userID string) (UserRole, error) {
	var rawRole string
	err := db.QueryRow(`SELECT COALESCE(role, 'admin') FROM users WHERE id = ?`, userID).Scan(&rawRole)
	if err != nil {
		return "", err
	}
	role, ok := normalizeUserRole(rawRole)
	if !ok {
		return "", fmt.Errorf("invalid role %q", rawRole)
	}
	return role, nil
}

func normalizeUserRole(raw string) (UserRole, bool) {
	switch UserRole(strings.TrimSpace(raw)) {
	case "", RoleAdmin:
		return RoleAdmin, true
	case RoleOperator:
		return RoleOperator, true
	case RoleViewer:
		return RoleViewer, true
	default:
		return "", false
	}
}

func scopesForRole(role UserRole) []string {
	granted := append([]string(nil), roleScopes[role]...)
	slices.Sort(granted)
	return granted
}

func roleHasScope(role UserRole, requiredScope string) bool {
	return slices.Contains(roleScopes[role], requiredScope)
}

// publicAPIRoutes lists the /api/* endpoints that are served without RBAC enforcement.
var publicAPIRoutes = map[string]struct{}{
	routeScopeKey(http.MethodPost, "/api/auth/login"):       {},
	routeScopeKey(http.MethodGet, "/api/setup/status"):      {},
	routeScopeKey(http.MethodPost, "/api/setup"):            {},
	// Phase 10 — branding metadata + image bytes are public so the
	// dashboard can fetch them before authentication (login screen,
	// document.title, favicon). Writes still require branding.admin.
	routeScopeKey(http.MethodGet, "/api/branding"):          {},
	routeScopeKey(http.MethodGet, "/api/branding/logo"):     {},
	routeScopeKey(http.MethodGet, "/api/branding/favicon"):  {},
}

// auditRBACRouteCoverage verifies every protected /api/* route registered on e has a
// scope mapping in requirements, and that requirements has no entries for routes
// that aren't registered. Called at startup so a missing or orphaned mapping fails
// the boot instead of surfacing as a per-request 500.
func auditRBACRouteCoverage(e *echo.Echo, requirements map[string]string) error {
	registered := make(map[string]struct{})
	var missing []string
	for _, r := range e.Routes() {
		if !strings.HasPrefix(r.Path, "/api/") {
			continue
		}
		key := routeScopeKey(r.Method, r.Path)
		if _, isPublic := publicAPIRoutes[key]; isPublic {
			continue
		}
		registered[key] = struct{}{}
		if _, ok := requirements[key]; !ok {
			missing = append(missing, key)
		}
	}

	var orphans []string
	for key := range requirements {
		if _, ok := registered[key]; !ok {
			orphans = append(orphans, key)
		}
	}

	if len(missing) == 0 && len(orphans) == 0 {
		return nil
	}
	slices.Sort(missing)
	slices.Sort(orphans)

	var parts []string
	if len(missing) > 0 {
		parts = append(parts, fmt.Sprintf("missing scope mapping for %d route(s): %v", len(missing), missing))
	}
	if len(orphans) > 0 {
		parts = append(parts, fmt.Sprintf("orphan scope mapping for %d route(s): %v", len(orphans), orphans))
	}
	return errors.New(strings.Join(parts, "; "))
}
