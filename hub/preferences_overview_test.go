package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// overviewBundle is the subset of GET /api/me/preferences these tests assert
// on. Declared locally so preferences_machine_order_test.go's own bundle stays
// exactly as narrow as it was.
type overviewBundle struct {
	OverviewLayout  string          `json:"overview_layout"`
	OverviewWidgets map[string]bool `json:"overview_widgets"`
	DefaultSort     string          `json:"default_sort"`
	MachineOrder    []string        `json:"machine_order"`
	DisplayName     string          `json:"display_name"`
}

func getOverviewPrefs(t *testing.T, e interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, token string) overviewBundle {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/me/preferences", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET preferences: status %d, body %s", rec.Code, rec.Body.String())
	}
	var bundle overviewBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("GET preferences: decode: %v (body %s)", err, rec.Body.String())
	}
	return bundle
}

func assertRecommendedWidgets(t *testing.T, got map[string]bool) {
	t.Helper()
	want := map[string]bool{"availability": true, "attention": true, "urgent_alert": true}
	if len(got) != len(want) {
		t.Fatalf("widgets: got %v, want exactly %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("widgets[%s]: got %v, want %v (full: %v)", key, got[key], value, got)
		}
	}
}

// Upgrade a real pre-overview schema carrying preferences a user chose, rather
// than only exercising the new columns on a fresh installation.
func TestOverviewMigrationPreservesExistingUser(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	const previousVersion = 24
	for _, migration := range migrations[:previousVersion] {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migration.apply(tx); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL);
		INSERT INTO schema_version VALUES (24);
		INSERT INTO users (id, username, password_hash, display_name, default_sort, machine_order)
		VALUES ('existing-user', 'existing', 'synthetic-hash', 'Existing User', 'manual', '["saved-id"]')`); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}

	var layout, widgets, name, sort, order, hash string
	if err := db.QueryRow(`SELECT overview_layout, overview_widgets, display_name, default_sort, machine_order, password_hash
		FROM users WHERE id = 'existing-user'`).Scan(&layout, &widgets, &name, &sort, &order, &hash); err != nil {
		t.Fatal(err)
	}
	if layout != "machine-first" {
		t.Fatalf("existing user did not get the recommended arrangement: %q", layout)
	}
	if widgets != `{"availability":true,"attention":true,"urgent_alert":true}` {
		t.Fatalf("existing user did not get the recommended modules: %q", widgets)
	}
	// Nothing the user actually chose may be touched by the upgrade.
	if name != "Existing User" || sort != "manual" || order != `["saved-id"]` || hash != "synthetic-hash" {
		t.Fatalf("upgrade changed existing preferences: name=%q sort=%q order=%q", name, sort, order)
	}

	// A chosen overview must survive a restart: re-running migrations is not
	// allowed to reset it back to the default.
	if _, err := db.Exec(`UPDATE users SET overview_layout = 'power-focus',
		overview_widgets = '{"availability":false,"attention":true,"urgent_alert":false}' WHERE id = 'existing-user'`); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT overview_layout, overview_widgets FROM users WHERE id = 'existing-user'`).Scan(&layout, &widgets); err != nil {
		t.Fatal(err)
	}
	if layout != "power-focus" || widgets != `{"availability":false,"attention":true,"urgent_alert":false}` {
		t.Fatalf("restart reset a chosen overview: layout=%q widgets=%q", layout, widgets)
	}
}

// A fresh account gets the recommended arrangement, with every module key
// present — never null, never a partial object.
func TestOverviewPreferencesDefault(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	bundle := getOverviewPrefs(t, e, token)
	if bundle.OverviewLayout != "machine-first" {
		t.Fatalf("default arrangement: got %q, want machine-first", bundle.OverviewLayout)
	}
	assertRecommendedWidgets(t, bundle.OverviewWidgets)
}

func TestOverviewLayoutPersistsEveryValue(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	for _, layout := range []string{"balanced", "power-focus", "machine-first"} {
		rec := patchPrefs(t, e, token, `{"overview_layout":"`+layout+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH %s: status %d, body %s", layout, rec.Code, rec.Body.String())
		}
		// The PATCH response is the full re-read bundle, so it must already
		// agree with a subsequent GET.
		var echoed overviewBundle
		if err := json.Unmarshal(rec.Body.Bytes(), &echoed); err != nil {
			t.Fatalf("PATCH %s: decode: %v", layout, err)
		}
		if echoed.OverviewLayout != layout {
			t.Fatalf("PATCH %s echoed %q", layout, echoed.OverviewLayout)
		}
		if got := getOverviewPrefs(t, e, token).OverviewLayout; got != layout {
			t.Fatalf("GET after PATCH %s: got %q", layout, got)
		}
	}
}

// Whatever key order the client sends, the column holds the canonical object —
// so two clients that mean the same thing cannot store different bytes.
func TestOverviewWidgetsPersistCanonically(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	rec := patchPrefs(t, e, token, `{"overview_widgets":{"urgent_alert":false,"availability":true,"attention":false}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH widgets: status %d, body %s", rec.Code, rec.Body.String())
	}
	got := getOverviewPrefs(t, e, token).OverviewWidgets
	want := map[string]bool{"availability": true, "attention": false, "urgent_alert": false}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("widgets[%s]: got %v, want %v", key, got[key], value)
		}
	}

	var stored string
	if err := s.db.QueryRow(`SELECT overview_widgets FROM users WHERE id = 'test-admin-id'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `{"availability":true,"attention":false,"urgent_alert":false}` {
		t.Fatalf("stored widgets are not canonical: %q", stored)
	}
}

// An unrelated PATCH must not disturb a chosen overview — the handler updates
// only the fields it was given.
func TestOverviewSurvivesUnrelatedPatch(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	if rec := patchPrefs(t, e, token, `{"overview_layout":"power-focus","overview_widgets":{"availability":false,"attention":false,"urgent_alert":true}}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH overview: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := patchPrefs(t, e, token, `{"display_name":"Ops"}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH display_name: status %d, body %s", rec.Code, rec.Body.String())
	}

	bundle := getOverviewPrefs(t, e, token)
	if bundle.DisplayName != "Ops" {
		t.Fatalf("display_name: got %q", bundle.DisplayName)
	}
	if bundle.OverviewLayout != "power-focus" {
		t.Fatalf("unrelated PATCH reset the arrangement: %q", bundle.OverviewLayout)
	}
	want := map[string]bool{"availability": false, "attention": false, "urgent_alert": true}
	for key, value := range want {
		if bundle.OverviewWidgets[key] != value {
			t.Fatalf("unrelated PATCH changed widgets[%s]: got %v, want %v", key, bundle.OverviewWidgets[key], value)
		}
	}
}

// Every malformed body is refused with 400 AND persists nothing. A partial
// widgets object is included deliberately: it is ambiguous about whether a
// missing key means "off" or "unchanged", and guessing would silently turn a
// module off.
func TestOverviewRejectsMalformed(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	for _, body := range []string{
		`{"overview_layout":"classic"}`,
		`{"overview_layout":"dashboard_layout"}`,
		`{"overview_layout":""}`,
		`{"overview_layout":1}`,
		`{"overview_widgets":null}`,
		`{"overview_widgets":"availability"}`,
		`{"overview_widgets":[]}`,
		`{"overview_widgets":{"availability":true}}`,
		`{"overview_widgets":{"availability":true,"attention":true,"urgent_alert":true,"highest_load":true}}`,
		`{"overview_widgets":{"availability":"yes","attention":true,"urgent_alert":true}}`,
	} {
		rec := patchPrefs(t, e, token, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("PATCH %s: status %d (want 400), body %s", body, rec.Code, rec.Body.String())
		}
	}

	// Nothing above may have leaked into storage.
	bundle := getOverviewPrefs(t, e, token)
	if bundle.OverviewLayout != "machine-first" {
		t.Fatalf("a rejected body changed the arrangement: %q", bundle.OverviewLayout)
	}
	assertRecommendedWidgets(t, bundle.OverviewWidgets)
	_ = s
}

// The overview is a per-user preference, not a per-installation one.
func TestOverviewIsPerUser(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)
	s.seedTestUser(t, "operator", "operator-pass", "4321", RoleOperator, true, true)
	operatorToken := loginAndGetTokenForCredentials(t, e, "operator", "operator-pass")

	if rec := patchPrefs(t, e, adminToken, `{"overview_layout":"power-focus"}`); rec.Code != http.StatusOK {
		t.Fatalf("admin PATCH: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := patchPrefs(t, e, operatorToken, `{"overview_layout":"balanced"}`); rec.Code != http.StatusOK {
		t.Fatalf("operator PATCH: status %d, body %s", rec.Code, rec.Body.String())
	}

	if got := getOverviewPrefs(t, e, adminToken).OverviewLayout; got != "power-focus" {
		t.Fatalf("admin arrangement: got %q", got)
	}
	if got := getOverviewPrefs(t, e, operatorToken).OverviewLayout; got != "balanced" {
		t.Fatalf("operator arrangement: got %q", got)
	}
}
