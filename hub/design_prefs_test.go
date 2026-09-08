package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesignPreferences(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	admin := loginAndGetToken(t, e)
	s.seedTestUser(t, "design-viewer", "viewerpass123", "1234", RoleViewer, true, true)
	viewer := loginAndGetTokenForCredentials(t, e, "design-viewer", "viewerpass123")
	request := func(token, method, body string, want int) designPrefs {
		t.Helper()
		req := httptest.NewRequest(method, "/api/me/design", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, body, rec.Code, want, rec.Body.String())
		}
		var prefs designPrefs
		if want == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &prefs); err != nil {
				t.Fatal(err)
			}
		}
		return prefs
	}
	request("", http.MethodGet, "", 401)
	request("", http.MethodPatch, `{"layout":"wall"}`, 401)
	initial := request(admin, http.MethodGet, "", 200)
	if initial.Layout != "classic" || initial.Colors["grove"] != "original" {
		t.Fatalf("bad migration defaults: %+v", initial)
	}
	for _, layout := range []string{"wall", "grove", "console"} {
		for _, color := range []string{"original", "bright", "dark"} {
			p := request(viewer, http.MethodPatch, `{"layout":"`+layout+`","colors":{"`+layout+`":"`+color+`"}}`, 200)
			if p.Layout != layout || p.Colors[layout] != color {
				t.Fatalf("not persisted: %+v", p)
			}
		}
	}
	got := request(viewer, http.MethodPatch, `{"layout":"classic"}`, 200)
	// Re-selecting an active choice must succeed, including an unchanged full
	// snapshot. Exercise the actual SQLite driver, not an assumed row-count rule.
	for i := 0; i < 3; i++ {
		request(viewer, http.MethodPatch, `{"layout":"classic","colors":{"wall":"dark","grove":"dark","console":"dark"}}`, 200)
	}
	for _, color := range got.Colors {
		if color != "dark" {
			t.Fatal("layout-only PATCH lost saved colors")
		}
	}
	for _, body := range []string{`{}`, `null`, `{"layout":"invented"}`, `{"colors":{"wall":"system"}}`, `{"colors":{"classic":"bright"}}`, `{"colors":{"wall_color = 1":"dark"}}`, `{"user_id":"someone-else","layout":"wall"}`, `{"layout":"wall"} {}`, `{"layout":"wall","colors":{"grove":null}}`} {
		request(viewer, http.MethodPatch, body, 400)
	}
	if p := request(viewer, http.MethodGet, "", 200); p.Layout != "classic" {
		t.Fatal("invalid patch partially applied")
	}
	if p := request(admin, http.MethodGet, "", 200); p.Layout != "classic" || p.Colors["wall"] != "original" {
		t.Fatal("viewer mutated admin preferences")
	}
	var name, mode string
	if err := s.db.QueryRow(`SELECT theme_name, theme_mode FROM users WHERE username = 'design-viewer'`).Scan(&name, &mode); err != nil {
		t.Fatal(err)
	}
	if name != "bloxos" || mode != "system" {
		t.Fatal("new design changed legacy theme")
	}
	if _, err := s.db.Exec(`UPDATE users SET dashboard_layout = 'unknown', wall_color = 'unknown' WHERE username = 'design-viewer'`); err != nil {
		t.Fatal(err)
	}
	if p := request(viewer, http.MethodGet, "", 200); p.Layout != "classic" || p.Colors["wall"] != "original" {
		t.Fatal("invalid stored value did not fall back")
	}
	if err := runMigrations(s.db); err != nil {
		t.Fatal(err)
	}
}
