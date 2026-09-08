package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Upgrade an actual pre-order schema with existing user preferences, rather
// than only exercising the new column on a fresh installation.
func TestMachineOrderMigrationPreservesExistingUser(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	const previousVersion = 23
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
		INSERT INTO schema_version VALUES (23);
		INSERT INTO users (id, username, password_hash, display_name, default_sort, dashboard_layout)
		VALUES ('existing-user', 'existing', 'synthetic-hash', 'Existing User', 'cpu', 'console')`); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	var order, name, sort, layout, hash string
	if err := db.QueryRow(`SELECT machine_order, display_name, default_sort, dashboard_layout, password_hash FROM users WHERE id = 'existing-user'`).Scan(&order, &name, &sort, &layout, &hash); err != nil {
		t.Fatal(err)
	}
	if order != "[]" || name != "Existing User" || sort != "cpu" || layout != "console" || hash != "synthetic-hash" {
		t.Fatalf("upgrade changed existing preferences: order=%q name=%q sort=%q layout=%q", order, name, sort, layout)
	}
	if _, err := db.Exec(`UPDATE users SET machine_order = '["saved-id"]', default_sort = 'manual' WHERE id = 'existing-user'`); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT machine_order FROM users WHERE id = 'existing-user'`).Scan(&order); err != nil {
		t.Fatal(err)
	}
	if order != `["saved-id"]` {
		t.Fatal("restart reset saved order")
	}
}

// prefsBundle is the subset of GET /api/me/preferences these tests assert on.
type prefsBundle struct {
	DefaultSort  string   `json:"default_sort"`
	MachineOrder []string `json:"machine_order"`
	DisplayName  string   `json:"display_name"`
}

func getPrefs(t *testing.T, e interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, token string) prefsBundle {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/me/preferences", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET preferences: status %d, body %s", rec.Code, rec.Body.String())
	}
	var p prefsBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("GET preferences: decode: %v (body %s)", err, rec.Body.String())
	}
	return p
}

func patchPrefs(t *testing.T, e interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/me/preferences", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestMachineOrderDefaultEmpty proves the migration added the column and a
// fresh user's order is an empty array (not null / not missing).
func TestMachineOrderDefaultEmpty(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	p := getPrefs(t, e, token)
	if p.MachineOrder == nil {
		t.Fatal("machine_order should be [] not null on a fresh user")
	}
	if len(p.MachineOrder) != 0 {
		t.Fatalf("machine_order = %v, want empty", p.MachineOrder)
	}
}

// TestMachineOrderPersistsAndSetsManual: supplying an order stores it and
// makes default_sort='manual' atomically, and it survives a reload.
func TestMachineOrderPersistsAndSetsManual(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	rec := patchPrefs(t, e, token, `{"machine_order":["m1","m2","m3"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH order: status %d, body %s", rec.Code, rec.Body.String())
	}
	var patched prefsBundle
	json.Unmarshal(rec.Body.Bytes(), &patched)
	if fmt.Sprint(patched.MachineOrder) != "[m1 m2 m3]" {
		t.Fatalf("PATCH response order = %v", patched.MachineOrder)
	}
	if patched.DefaultSort != "manual" {
		t.Fatalf("default_sort = %q, want manual", patched.DefaultSort)
	}

	got := getPrefs(t, e, token)
	if fmt.Sprint(got.MachineOrder) != "[m1 m2 m3]" || got.DefaultSort != "manual" {
		t.Fatalf("reload: order %v sort %q", got.MachineOrder, got.DefaultSort)
	}
}

// TestMachineOrderOmissionPreserves: a PATCH that omits machine_order leaves
// the stored order (and manual sort) untouched.
func TestMachineOrderOmissionPreserves(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	patchPrefs(t, e, token, `{"machine_order":["a","b"]}`)

	rec := patchPrefs(t, e, token, `{"display_name":"Ops"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH display_name: %d %s", rec.Code, rec.Body.String())
	}
	got := getPrefs(t, e, token)
	if fmt.Sprint(got.MachineOrder) != "[a b]" {
		t.Fatalf("order not preserved: %v", got.MachineOrder)
	}
	if got.DefaultSort != "manual" || got.DisplayName != "Ops" {
		t.Fatalf("sort %q name %q", got.DefaultSort, got.DisplayName)
	}
}

// TestMachineOrderEmptyClears: an explicit [] is accepted and clears the order.
func TestMachineOrderEmptyClears(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	patchPrefs(t, e, token, `{"machine_order":["a","b"]}`)

	rec := patchPrefs(t, e, token, `{"machine_order":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH empty order: %d %s", rec.Code, rec.Body.String())
	}
	got := getPrefs(t, e, token)
	if len(got.MachineOrder) != 0 {
		t.Fatalf("order not cleared: %v", got.MachineOrder)
	}
}

// TestMachineOrderStaleIDsAllowed: unknown/stale machine IDs are stored and
// returned without failure (the UI reconciles against the live fleet).
func TestMachineOrderStaleIDsAllowed(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	rec := patchPrefs(t, e, token, `{"machine_order":["does-not-exist-1","deleted-2"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("stale IDs should be accepted: %d %s", rec.Code, rec.Body.String())
	}
	if fmt.Sprint(getPrefs(t, e, token).MachineOrder) != "[does-not-exist-1 deleted-2]" {
		t.Fatal("stale IDs not returned")
	}
}

// TestMachineOrderTwoUserIsolation: each user owns their own order, including
// a non-admin (auth.self applies to all roles), and one cannot affect another.
func TestMachineOrderTwoUserIsolation(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	admin := loginAndGetToken(t, e)
	s.seedTestUser(t, "alice", "alicepass123", "1234", RoleOperator, true, true)
	alice := loginAndGetTokenForCredentials(t, e, "alice", "alicepass123")

	patchPrefs(t, e, admin, `{"machine_order":["admin-1","admin-2"]}`)
	rec := patchPrefs(t, e, alice, `{"machine_order":["alice-1"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator must be able to own order: %d %s", rec.Code, rec.Body.String())
	}
	if fmt.Sprint(getPrefs(t, e, alice).MachineOrder) != "[alice-1]" {
		t.Fatal("alice order wrong")
	}
	if fmt.Sprint(getPrefs(t, e, admin).MachineOrder) != "[admin-1 admin-2]" {
		t.Fatal("admin order was affected by alice")
	}
}

// TestMachineOrderRejectsMalformed covers null, type errors, empty entries,
// duplicates, and oversize.
func TestMachineOrderRejectsMalformed(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	var big strings.Builder
	big.WriteString(`{"machine_order":[`)
	for i := 0; i < maxMachineOrderCount+1; i++ {
		if i > 0 {
			big.WriteByte(',')
		}
		fmt.Fprintf(&big, `"m%d"`, i)
	}
	big.WriteString(`]}`)

	longID := `{"machine_order":["` + strings.Repeat("x", maxMachineOrderIDLen+1) + `"]}`

	cases := map[string]string{
		"null":           `{"machine_order":null}`,
		"string":         `{"machine_order":"nope"}`,
		"number-elems":   `{"machine_order":[1,2,3]}`,
		"object":         `{"machine_order":{"a":1}}`,
		"empty-entry":    `{"machine_order":["a","",""]}`,
		"duplicate":      `{"machine_order":["a","a"]}`,
		"oversize-count": big.String(),
		"oversize-id":    longID,
		"conflict-sort":  `{"machine_order":["a"],"default_sort":"name"}`,
	}
	for name, body := range cases {
		rec := patchPrefs(t, e, token, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (body %s)", name, rec.Code, rec.Body.String())
		}
	}
	// A rejected PATCH must not have persisted anything.
	if len(getPrefs(t, e, token).MachineOrder) != 0 {
		t.Fatal("a rejected machine_order was persisted")
	}
}

// TestMachineOrderManualSortAloneAccepted: default_sort='manual' is now valid
// on its own, and 'manual' alongside machine_order is allowed (not a conflict).
func TestMachineOrderManualSortAloneAccepted(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	if rec := patchPrefs(t, e, token, `{"default_sort":"manual"}`); rec.Code != http.StatusOK {
		t.Fatalf("default_sort=manual alone: %d %s", rec.Code, rec.Body.String())
	}
	if getPrefs(t, e, token).DefaultSort != "manual" {
		t.Fatal("manual sort not stored")
	}
	if rec := patchPrefs(t, e, token, `{"machine_order":["a"],"default_sort":"manual"}`); rec.Code != http.StatusOK {
		t.Fatalf("order + manual should be allowed: %d %s", rec.Code, rec.Body.String())
	}
}
