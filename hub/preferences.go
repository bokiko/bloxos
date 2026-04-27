package main

// Phase 11 — per-user workflow personalization.
//
// One GET endpoint returns the entire preferences bundle so the dashboard
// loads everything in a single round-trip after login. Mutations are split
// into focused endpoints (PATCH scalars, POST/DELETE avatar, pin/unpin,
// saved filters) so each can validate and roll back independently.
//
// All authenticated routes use the `auth.self` RBAC scope — every user
// can only ever read or write their own row. The `:user_id` parameter on
// GET /api/users/:user_id/avatar is read-only and not auth-restricted to
// the requesting user since admins legitimately need to render other
// users' avatars (e.g. user-management list).

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const (
	maxAvatarBytes        = 200 * 1024
	maxDisplayNameLen     = 60
	maxSavedFilterNameLen = 60
	maxSavedFiltersPerUser = 20
	maxWelcomeMessageLen  = 500
)

// displayNameRegex permits Unicode letters/digits, plus a small set of
// punctuation. RE2 supports `\p{L}` and `\p{N}` natively so this compiles
// and matches Cyrillic, CJK, etc.
var displayNameRegex = regexp.MustCompile(`^[\p{L}\p{N} '._-]*$`)

var validDensities = map[string]struct{}{
	"comfortable": {},
	"compact":     {},
}

var validDefaultViews = map[string]struct{}{
	"grid": {},
	"list": {},
}

var validDefaultSorts = map[string]struct{}{
	"name":     {},
	"status":   {},
	"cpu":      {},
	"gpu_temp": {},
}

// SavedFilter is the public shape of a saved-filter row. The `Filter`
// field is opaque JSON the client wrote; the hub doesn't introspect it
// beyond size/syntax validation.
type SavedFilter struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Filter    json.RawMessage `json:"filter"`
	CreatedAt time.Time       `json:"created_at"`
}

// UserPreferences is the bundle returned by GET /api/me/preferences.
type UserPreferences struct {
	DisplayName    string         `json:"display_name"`
	HasAvatar      bool           `json:"has_avatar"`
	AvatarSHA      string         `json:"avatar_sha,omitempty"`
	Density        string         `json:"density"`
	DefaultView    string         `json:"default_view"`
	DefaultSort    string         `json:"default_sort"`
	PinnedMachines []string       `json:"pinned_machines"`
	SavedFilters   []SavedFilter  `json:"saved_filters"`
}

// handleGetMyPreferences returns the entire prefs bundle in one round-trip.
func handleGetMyPreferences(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}

	prefs := UserPreferences{
		PinnedMachines: []string{},
		SavedFilters:   []SavedFilter{},
	}
	var avatarSHA sql.NullString
	var hasAvatar sql.NullInt64
	err := db.QueryRow(`
		SELECT
			COALESCE(display_name, ''),
			COALESCE(density, 'comfortable'),
			COALESCE(default_view, 'grid'),
			COALESCE(default_sort, 'name'),
			avatar_sha,
			CASE WHEN avatar_data IS NOT NULL AND length(avatar_data) > 0 THEN 1 ELSE 0 END
		FROM users WHERE id = ?
	`, claims.UserID).Scan(
		&prefs.DisplayName,
		&prefs.Density,
		&prefs.DefaultView,
		&prefs.DefaultSort,
		&avatarSHA,
		&hasAvatar,
	)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	prefs.HasAvatar = hasAvatar.Valid && hasAvatar.Int64 == 1
	if prefs.HasAvatar && avatarSHA.Valid {
		prefs.AvatarSHA = avatarSHA.String
	}
	// Snap unknown values back to defaults defensively.
	if _, ok := validDensities[prefs.Density]; !ok {
		prefs.Density = "comfortable"
	}
	if _, ok := validDefaultViews[prefs.DefaultView]; !ok {
		prefs.DefaultView = "grid"
	}
	if _, ok := validDefaultSorts[prefs.DefaultSort]; !ok {
		prefs.DefaultSort = "name"
	}

	// Pinned machines (most-recent first).
	rows, err := db.Query(`
		SELECT machine_id FROM user_pinned_machines WHERE user_id = ? ORDER BY pinned_at DESC
	`, claims.UserID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()
	for rows.Next() {
		var mid string
		if err := rows.Scan(&mid); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		prefs.PinnedMachines = append(prefs.PinnedMachines, mid)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Saved filters (most-recent first).
	frows, err := db.Query(`
		SELECT id, name, filter_json, created_at
		FROM user_saved_filters WHERE user_id = ? ORDER BY created_at DESC
	`, claims.UserID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer frows.Close()
	for frows.Next() {
		var f SavedFilter
		var filterJSON string
		if err := frows.Scan(&f.ID, &f.Name, &filterJSON, &f.CreatedAt); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		f.Filter = json.RawMessage(filterJSON)
		prefs.SavedFilters = append(prefs.SavedFilters, f)
	}
	if err := frows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, prefs)
}

// handlePatchMyPreferences accepts {display_name?, density?, default_view?, default_sort?}.
func handlePatchMyPreferences(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}

	var body struct {
		DisplayName *string `json:"display_name,omitempty"`
		Density     *string `json:"density,omitempty"`
		DefaultView *string `json:"default_view,omitempty"`
		DefaultSort *string `json:"default_sort,omitempty"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}

	if body.DisplayName == nil && body.Density == nil && body.DefaultView == nil && body.DefaultSort == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	var sets []string
	var args []any

	if body.DisplayName != nil {
		dn := strings.TrimSpace(*body.DisplayName)
		// Count runes, not bytes — Unicode display names should not be
		// truncated by accidental byte-length math.
		if len([]rune(dn)) > maxDisplayNameLen {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("display_name must be %d characters or fewer", maxDisplayNameLen)})
		}
		if !displayNameRegex.MatchString(dn) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "display_name contains invalid characters"})
		}
		sets = append(sets, "display_name = ?")
		args = append(args, dn)
	}
	if body.Density != nil {
		if _, ok := validDensities[*body.Density]; !ok {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid density"})
		}
		sets = append(sets, "density = ?")
		args = append(args, *body.Density)
	}
	if body.DefaultView != nil {
		if _, ok := validDefaultViews[*body.DefaultView]; !ok {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid default_view"})
		}
		sets = append(sets, "default_view = ?")
		args = append(args, *body.DefaultView)
	}
	if body.DefaultSort != nil {
		if _, ok := validDefaultSorts[*body.DefaultSort]; !ok {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid default_sort"})
		}
		sets = append(sets, "default_sort = ?")
		args = append(args, *body.DefaultSort)
	}

	query := fmt.Sprintf(`UPDATE users SET %s WHERE id = ?`, strings.Join(sets, ", "))
	args = append(args, claims.UserID)
	if _, err := db.Exec(query, args...); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return handleGetMyPreferences(c)
}

// handleUploadAvatar accepts a PNG up to 200 KB.
func handleUploadAvatar(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}

	fh, err := c.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing file field"})
	}
	if fh.Size > int64(maxAvatarBytes) {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("avatar exceeds %d bytes", maxAvatarBytes),
		})
	}
	src, err := fh.Open()
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "could not open uploaded file"})
	}
	defer src.Close()
	data, err := io.ReadAll(io.LimitReader(src, int64(maxAvatarBytes)+1))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "could not read uploaded file"})
	}
	if len(data) > maxAvatarBytes {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("avatar exceeds %d bytes", maxAvatarBytes),
		})
	}
	if !bytes.HasPrefix(data, []byte(pngMagic)) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "avatar is not a PNG (magic bytes mismatch)"})
	}
	if _, decErr := png.Decode(bytes.NewReader(data)); decErr != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "avatar failed PNG decode"})
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])[:16]

	if _, err := db.Exec(
		`UPDATE users SET avatar_data = ?, avatar_mime = ?, avatar_sha = ? WHERE id = ?`,
		data, "image/png", sha, claims.UserID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return handleGetMyPreferences(c)
}

// handleDeleteAvatar clears the avatar columns.
func handleDeleteAvatar(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}
	if _, err := db.Exec(
		`UPDATE users SET avatar_data = NULL, avatar_mime = NULL, avatar_sha = NULL WHERE id = ?`,
		claims.UserID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return handleGetMyPreferences(c)
}

// handleGetAvatar returns the avatar blob for any user. Cached aggressively
// when the request's `?v=<sha>` matches the stored SHA.
func handleGetAvatar(c echo.Context) error {
	userID := c.Param("user_id")
	if userID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing user_id"})
	}
	var (
		data     []byte
		mime     sql.NullString
		shaStore sql.NullString
	)
	err := db.QueryRow(
		`SELECT avatar_data, avatar_mime, avatar_sha FROM users WHERE id = ?`, userID,
	).Scan(&data, &mime, &shaStore)
	if err == sql.ErrNoRows || len(data) == 0 || !mime.Valid || mime.String == "" {
		return c.NoContent(http.StatusNotFound)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	requested := c.QueryParam("v")
	if shaStore.Valid && requested != "" && requested == shaStore.String {
		c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		c.Response().Header().Set("Cache-Control", "public, max-age=60")
	}
	return c.Blob(http.StatusOK, mime.String, data)
}

// handlePinMachine adds machine_id to the user's pinned set (idempotent).
func handlePinMachine(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}
	machineID := c.Param("machine_id")
	if machineID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing machine_id"})
	}
	// Verify the machine exists so we don't accumulate orphan pins for
	// hosts that never enrolled.
	var exists int
	if err := db.QueryRow(`SELECT 1 FROM machines WHERE id = ?`, machineID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO user_pinned_machines (user_id, machine_id) VALUES (?, ?)`,
		claims.UserID, machineID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.NoContent(http.StatusNoContent)
}

// handleUnpinMachine removes a pin (idempotent).
func handleUnpinMachine(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}
	machineID := c.Param("machine_id")
	if machineID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing machine_id"})
	}
	if _, err := db.Exec(
		`DELETE FROM user_pinned_machines WHERE user_id = ? AND machine_id = ?`,
		claims.UserID, machineID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.NoContent(http.StatusNoContent)
}

// handleListSavedFilters returns this user's saved filters.
func handleListSavedFilters(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}
	rows, err := db.Query(`
		SELECT id, name, filter_json, created_at
		FROM user_saved_filters WHERE user_id = ? ORDER BY created_at DESC
	`, claims.UserID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()
	out := make([]SavedFilter, 0)
	for rows.Next() {
		var f SavedFilter
		var filterJSON string
		if err := rows.Scan(&f.ID, &f.Name, &filterJSON, &f.CreatedAt); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		f.Filter = json.RawMessage(filterJSON)
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusOK, out)
}

// handleCreateSavedFilter accepts {name, filter}. Caps at maxSavedFiltersPerUser.
func handleCreateSavedFilter(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}
	var body struct {
		Name   string          `json:"name"`
		Filter json.RawMessage `json:"filter"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
	}
	if len([]rune(name)) > maxSavedFilterNameLen {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("name must be %d characters or fewer", maxSavedFilterNameLen)})
	}
	if len(body.Filter) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "filter is required"})
	}
	// Validate the filter is a JSON object — an array or string would let
	// the client store junk that breaks the dashboard parser.
	var probe map[string]any
	if err := json.Unmarshal(body.Filter, &probe); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "filter must be a JSON object"})
	}

	// Enforce the per-user cap.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_saved_filters WHERE user_id = ?`, claims.UserID).Scan(&count); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if count >= maxSavedFiltersPerUser {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("saved filter limit reached (%d)", maxSavedFiltersPerUser)})
	}

	id := uuid.New().String()
	if _, err := db.Exec(
		`INSERT INTO user_saved_filters (id, user_id, name, filter_json) VALUES (?, ?, ?, ?)`,
		id, claims.UserID, name, string(body.Filter),
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	// Return the created row so the dashboard can prepend without refetching.
	var created SavedFilter
	var filterJSON string
	err := db.QueryRow(
		`SELECT id, name, filter_json, created_at FROM user_saved_filters WHERE id = ?`,
		id,
	).Scan(&created.ID, &created.Name, &filterJSON, &created.CreatedAt)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	created.Filter = json.RawMessage(filterJSON)
	return c.JSON(http.StatusCreated, created)
}

// handleDeleteSavedFilter removes one of the user's filters.
func handleDeleteSavedFilter(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}
	id := c.Param("id")
	if id == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing id"})
	}
	res, err := db.Exec(
		`DELETE FROM user_saved_filters WHERE id = ? AND user_id = ?`,
		id, claims.UserID,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "saved filter not found"})
	}
	return c.NoContent(http.StatusNoContent)
}
