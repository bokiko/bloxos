package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

var designColumns = map[string]string{"wall": "wall_color", "grove": "grove_color", "console": "console_color"}

// Ledger, like classic, has a single fixed palette and therefore no colour
// column: colour there is reserved for severity, so there is nothing to pick.
func validDesignLayout(v string) bool {
	return v == "classic" || v == "ledger" || designColumns[v] != ""
}
func validDesignColor(v string) bool  { return v == "original" || v == "bright" || v == "dark" }

type designPrefs struct {
	Layout string            `json:"layout"`
	Colors map[string]string `json:"colors"`
}

func (s *Server) handleGetMyDesign(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	var layout, wall, grove, console string
	err := s.db.QueryRow(`SELECT dashboard_layout, wall_color, grove_color, console_color FROM users WHERE id = ?`, claims.UserID).Scan(&layout, &wall, &grove, &console)
	if err == sql.ErrNoRows {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not load design")
	}
	if !validDesignLayout(layout) {
		layout = "classic"
	}
	colors := map[string]string{"wall": wall, "grove": grove, "console": console}
	for k, v := range colors {
		if !validDesignColor(v) {
			colors[k] = "original"
		}
	}
	return c.JSON(http.StatusOK, designPrefs{Layout: layout, Colors: colors})
}

// Partial writes preserve other layouts' colors, legacy palettes and other users.
func (s *Server) handlePatchMyDesign(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	var body struct {
		Layout *string           `json:"layout"`
		Colors map[string]string `json:"colors"`
	}
	decoder := json.NewDecoder(io.LimitReader(c.Request().Body, 2049))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid design preferences")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid design preferences")
	}
	if body.Layout == nil && len(body.Colors) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "no design fields to update")
	}
	sets := []string{}
	args := []any{}
	if body.Layout != nil {
		if !validDesignLayout(*body.Layout) {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid layout")
		}
		sets = append(sets, "dashboard_layout = ?")
		args = append(args, *body.Layout)
	}
	for layout, color := range body.Colors {
		column, exists := designColumns[layout]
		if !exists || !validDesignColor(color) {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid layout color")
		}
		sets = append(sets, column+" = ?")
		args = append(args, color)
	}
	args = append(args, claims.UserID)
	result, err := s.db.Exec("UPDATE users SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not save design")
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	return s.handleGetMyDesign(c)
}
