package main

import (
	"database/sql"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

type userRecord struct {
	ID              string    `json:"id"`
	Username        string    `json:"username"`
	Role            string    `json:"role"`
	CreatedAt       time.Time `json:"created_at"`
	PasswordChanged bool      `json:"password_changed"`
	PINChanged      bool      `json:"pin_changed"`
}

var pinPattern = regexp.MustCompile(`^\d{4,}$`)

func handleCreateUser(c echo.Context) error {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		PIN      string `json:"pin"`
		Role     string `json:"role"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}

	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "username is required"})
	}
	if err := validatePassword(body.Password); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if !pinPattern.MatchString(body.PIN) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "PIN must be at least 4 digits"})
	}
	if body.Role == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "role is required"})
	}
	role, ok := normalizeUserRole(body.Role)
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "role must be admin, operator, or viewer"})
	}

	var existing string
	err := db.QueryRow(`SELECT id FROM users WHERE username = ?`, body.Username).Scan(&existing)
	if err == nil {
		return c.JSON(http.StatusConflict, map[string]string{"error": "username already exists"})
	}
	if err != sql.ErrNoRows {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
	}
	pinHash, err := bcrypt.GenerateFromPassword([]byte(body.PIN), bcrypt.DefaultCost)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash PIN"})
	}

	id := uuid.New().String()
	// Admin-provisioned users must rotate the temporary credentials on first login.
	_, err = db.Exec(
		`INSERT INTO users (id, username, password_hash, terminal_pin_hash, password_changed, pin_changed, role) VALUES (?, ?, ?, ?, FALSE, FALSE, ?)`,
		id, body.Username, string(passwordHash), string(pinHash), role,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create user"})
	}

	rec, err := fetchUser(id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "user created but fetch failed"})
	}
	return c.JSON(http.StatusCreated, rec)
}

func handleListUsers(c echo.Context) error {
	rows, err := db.Query(
		`SELECT id, username, COALESCE(role, 'admin'), created_at,
		        COALESCE(password_changed, FALSE), COALESCE(pin_changed, FALSE)
		 FROM users ORDER BY created_at ASC`,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer rows.Close()

	users := []userRecord{}
	for rows.Next() {
		var u userRecord
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &u.PasswordChanged, &u.PINChanged); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		users = append(users, u)
	}
	return c.JSON(http.StatusOK, users)
}

func handleUpdateUser(c echo.Context) error {
	targetID := c.Param("id")
	if targetID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "user id required"})
	}
	claims, ok := authClaimsFromContext(c)
	if !ok {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}

	var body struct {
		Role     *string `json:"role,omitempty"`
		Password *string `json:"password,omitempty"`
		PIN      *string `json:"pin,omitempty"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}
	if body.Role == nil && body.Password == nil && body.PIN == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	var currentRole string
	if err := db.QueryRow(`SELECT COALESCE(role, 'admin') FROM users WHERE id = ?`, targetID).Scan(&currentRole); err != nil {
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if body.Role != nil {
		if targetID == claims.UserID {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot change own role"})
		}
		newRole, ok := normalizeUserRole(*body.Role)
		if !ok || *body.Role == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "role must be admin, operator, or viewer"})
		}
		if currentRole == string(RoleAdmin) && newRole != RoleAdmin {
			last, err := isLastAdmin()
			if err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
			}
			if last {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot demote the last admin"})
			}
		}
		if _, err := db.Exec(`UPDATE users SET role = ? WHERE id = ?`, newRole, targetID); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update role"})
		}
	}

	if body.Password != nil {
		if err := validatePassword(*body.Password); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*body.Password), bcrypt.DefaultCost)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
		}
		// Force the target to rotate on next login.
		if _, err := db.Exec(
			`UPDATE users SET password_hash = ?, password_changed = FALSE WHERE id = ?`,
			string(hash), targetID,
		); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update password"})
		}
	}

	if body.PIN != nil {
		if !pinPattern.MatchString(*body.PIN) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "PIN must be at least 4 digits"})
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*body.PIN), bcrypt.DefaultCost)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash PIN"})
		}
		if _, err := db.Exec(
			`UPDATE users SET terminal_pin_hash = ?, pin_changed = FALSE WHERE id = ?`,
			string(hash), targetID,
		); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update PIN"})
		}
	}

	rec, err := fetchUser(targetID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "user updated but fetch failed"})
	}
	return c.JSON(http.StatusOK, rec)
}

func handleDeleteUser(c echo.Context) error {
	targetID := c.Param("id")
	if targetID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "user id required"})
	}
	claims, ok := authClaimsFromContext(c)
	if !ok {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}
	if targetID == claims.UserID {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot delete own account"})
	}

	var role string
	if err := db.QueryRow(`SELECT COALESCE(role, 'admin') FROM users WHERE id = ?`, targetID).Scan(&role); err != nil {
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "user not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if role == string(RoleAdmin) {
		last, err := isLastAdmin()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
		}
		if last {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "cannot delete the last admin"})
		}
	}

	if _, err := db.Exec(`DELETE FROM users WHERE id = ?`, targetID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

func fetchUser(id string) (userRecord, error) {
	var u userRecord
	err := db.QueryRow(
		`SELECT id, username, COALESCE(role, 'admin'), created_at,
		        COALESCE(password_changed, FALSE), COALESCE(pin_changed, FALSE)
		 FROM users WHERE id = ?`,
		id,
	).Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &u.PasswordChanged, &u.PINChanged)
	return u, err
}

func isLastAdmin() (bool, error) {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ?`, RoleAdmin).Scan(&count); err != nil {
		return false, err
	}
	return count <= 1, nil
}
