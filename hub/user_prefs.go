package main

// Monoform — per-user appearance preference.
//
// Stored on the users row as theme_name + theme_mode, kept for wire and schema
// compatibility. There is now exactly one theme ("monoform") in two modes:
// "dark" (the default) and "light". Everything else — the retired "gray"
// contrast mode, the pre-Monoform "system", a retired palette name such as
// "dracula", NULL — is a historical value.
//
// Those values are normalized on READ, not migrated. The column keeps whatever
// it holds: rewriting every user's row for a value that is already corrected
// in the one place it is consumed buys nothing, and would have to be repeated
// for every future rename. A write from any current client replaces the stale
// value naturally.
//
// Both endpoints require authentication; users only ever read or write their
// own row.

import (
	"database/sql"
	"net/http"

	"github.com/labstack/echo/v4"
)

var validThemeNames = map[string]struct{}{
	"monoform": {},
}

var validThemeModes = map[string]struct{}{
	"dark":  {},
	"light": {},
}

// defaultThemeMode is what an unset, unrecognised or retired theme_mode
// resolves to. It must stay in step with normalizeAppearance() in
// dashboard/src/contexts/ThemeContext.tsx and with the pre-hydration bootstrap
// in dashboard/src/app/layout.tsx — all three implement the same rule.
const defaultThemeMode = "dark"

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
		`SELECT COALESCE(theme_name, 'monoform'), COALESCE(theme_mode, 'dark') FROM users WHERE id = ?`,
		claims.UserID,
	).Scan(&prefs.ThemeName, &prefs.ThemeMode)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Defensive: snap unknown values back to the defaults so the dashboard
	// never has to handle a stale value. Rows written before this release hold
	// "gray"; rows written before Monoform hold e.g. "dracula" / "system".
	if _, ok := validThemeNames[prefs.ThemeName]; !ok {
		prefs.ThemeName = "monoform"
	}
	if _, ok := validThemeModes[prefs.ThemeMode]; !ok {
		prefs.ThemeMode = defaultThemeMode
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
