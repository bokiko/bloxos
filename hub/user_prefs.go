package main

// Phase 10 — per-user theme preferences.
//
// Stored on the users row as theme_name + theme_mode. Both endpoints
// require authentication; users only ever read or write their own row.

import (
	"database/sql"
	"net/http"

	"github.com/labstack/echo/v4"
)

var validThemeNames = map[string]struct{}{
	"bloxos":      {},
	"solarized":   {},
	"dracula":     {},
	"nord":        {},
	"tokyo-night": {},
}

var validThemeModes = map[string]struct{}{
	"light":  {},
	"dark":   {},
	"system": {},
}

type themePrefs struct {
	ThemeName string `json:"theme_name"`
	ThemeMode string `json:"theme_mode"`
}

func (s *Server) handleGetMyThemePrefs(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}

	var prefs themePrefs
	err := s.db.QueryRow(
		`SELECT COALESCE(theme_name, 'bloxos'), COALESCE(theme_mode, 'system') FROM users WHERE id = ?`,
		claims.UserID,
	).Scan(&prefs.ThemeName, &prefs.ThemeMode)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Defensive: snap unknown values back to defaults so the dashboard
	// never has to handle a stale value from a deprecated theme.
	if _, ok := validThemeNames[prefs.ThemeName]; !ok {
		prefs.ThemeName = "bloxos"
	}
	if _, ok := validThemeModes[prefs.ThemeMode]; !ok {
		prefs.ThemeMode = "system"
	}
	return c.JSON(http.StatusOK, prefs)
}

func (s *Server) handleUpdateMyThemePrefs(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}

	var body struct {
		ThemeName *string `json:"theme_name,omitempty"`
		ThemeMode *string `json:"theme_mode,omitempty"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}

	if body.ThemeName == nil && body.ThemeMode == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	if body.ThemeName != nil {
		if _, ok := validThemeNames[*body.ThemeName]; !ok {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid theme_name"})
		}
	}
	if body.ThemeMode != nil {
		if _, ok := validThemeModes[*body.ThemeMode]; !ok {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid theme_mode"})
		}
	}

	// Build a dynamic UPDATE so we only touch fields the client sent.
	query := `UPDATE users SET `
	args := []any{}
	first := true
	if body.ThemeName != nil {
		query += `theme_name = ?`
		args = append(args, *body.ThemeName)
		first = false
	}
	if body.ThemeMode != nil {
		if !first {
			query += `, `
		}
		query += `theme_mode = ?`
		args = append(args, *body.ThemeMode)
	}
	query += ` WHERE id = ?`
	args = append(args, claims.UserID)

	if _, err := s.db.Exec(query, args...); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return s.handleGetMyThemePrefs(c)
}
