package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// Monoform's authoritative machine work surface is the Machine fleet table, so
// a newly created account must start on it. The `default_view` column was
// created as `TEXT NOT NULL DEFAULT 'grid'` and SQLite cannot ALTER a column
// default, so the value is applied at each INSERT site instead of by rebuilding
// `users`. These tests pin that behavior to the real handlers — a future edit
// that drops `default_view` from either INSERT silently reverts new accounts to
// cards, and nothing else would catch it.
//
// The change is forward-only by construction: an existing row is never
// rewritten, because its stored value is a preference the user chose, not an
// unset default. TestExistingUserKeepsStoredDefaultView guards that half.

func newMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := runMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return db
}

func userDefaultView(t *testing.T, db *sql.DB, username string) string {
	t.Helper()
	var view string
	if err := db.QueryRow(`SELECT default_view FROM users WHERE username = ?`, username).Scan(&view); err != nil {
		t.Fatalf("read default_view for %q: %v", username, err)
	}
	return view
}

// The admin created by first-run setup is a new user like any other.
func TestSetupAdminDefaultsToTableView(t *testing.T) {
	db := newMigratedDB(t)
	s := &Server{db: db}

	// handleSetup rate-limits by IP against the process-wide limiter, which is
	// only constructed in main(); tests must supply their own.
	rateLimiter = NewRateLimiter()

	// handleSetup checks the process-wide setup token; seed it for this test.
	setupMu.Lock()
	previousToken := setupTokenValue
	setupTokenValue = "test-setup-token"
	setupMu.Unlock()
	t.Cleanup(func() {
		setupMu.Lock()
		setupTokenValue = previousToken
		setupMu.Unlock()
	})

	body := `{"username":"bootstrap-admin","password":"correct-horse-battery","pin":"481516","setup_token":"test-setup-token"}`
	req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)

	if err := s.handleSetup(c); err != nil {
		t.Fatalf("handleSetup: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("handleSetup status = %d, body %s", rec.Code, rec.Body.String())
	}

	if got := userDefaultView(t, db, "bootstrap-admin"); got != "list" {
		t.Fatalf("setup admin default_view = %q, want %q (the Machine fleet table)", got, "list")
	}
}

// Admin-provisioned users get the same default.
func TestCreatedUserDefaultsToTableView(t *testing.T) {
	db := newMigratedDB(t)
	s := &Server{db: db}

	for _, role := range []string{"admin", "operator", "viewer"} {
		t.Run(role, func(t *testing.T) {
			username := "new-" + role
			body := `{"username":"` + username + `","password":"correct-horse-battery","pin":"481516","role":"` + role + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := echo.New().NewContext(req, rec)

			if err := s.handleCreateUser(c); err != nil {
				t.Fatalf("handleCreateUser: %v", err)
			}
			if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
				t.Fatalf("handleCreateUser status = %d, body %s", rec.Code, rec.Body.String())
			}

			if got := userDefaultView(t, db, username); got != "list" {
				t.Fatalf("new %s default_view = %q, want %q (the Machine fleet table)", role, got, "list")
			}
		})
	}
}

// The forward-only half: upgrading must not rewrite a preference somebody set.
// A user who is on cards stays on cards until they choose otherwise.
func TestExistingUserKeepsStoredDefaultView(t *testing.T) {
	db := newMigratedDB(t)
	if _, err := db.Exec(
		`INSERT INTO users (id, username, password_hash, default_view) VALUES (?, ?, ?, ?)`,
		"legacy-user", "legacy", "synthetic-hash", "grid",
	); err != nil {
		t.Fatalf("seed existing user: %v", err)
	}

	// Re-running migrations is what an upgrade does; it must be a no-op here.
	if err := runMigrations(db); err != nil {
		t.Fatalf("re-run migrations: %v", err)
	}

	if got := userDefaultView(t, db, "legacy"); got != "grid" {
		t.Fatalf("existing user default_view = %q, want it left at %q", got, "grid")
	}
}

// The constant must stay a value the preferences API will accept, or new
// accounts would be created in a state their own PATCH endpoint rejects.
func TestDefaultViewForNewUserIsValid(t *testing.T) {
	if _, ok := validDefaultViews[defaultViewForNewUser]; !ok {
		t.Fatalf("defaultViewForNewUser = %q is not in validDefaultViews", defaultViewForNewUser)
	}
	if defaultViewForNewUser != "list" {
		t.Fatalf("defaultViewForNewUser = %q, want \"list\" (Monoform's desktop default)", defaultViewForNewUser)
	}
}
