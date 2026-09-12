package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// The rollout controller reads its own tables, and newServer runs BEFORE
// migrations. Building the controller there meant every fresh install and
// every upgrade constructed it against a schema without agent_rollout_slot,
// retained that error, and withheld agent updates forever — on a database the
// very next statement fixed.
//
// Unit fixtures migrate first and then build a server, so they cannot see
// this. This test goes through the production order: open, construct, migrate,
// then initialise.
func TestTheRolloutControllerIsBuiltAfterMigrations(t *testing.T) {
	db, err := sql.Open("sqlite", databaseDSN(filepath.Join(t.TempDir(), "fresh.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Exactly main()'s order, on a database with no schema at all.
	s := newServer(db)
	t.Cleanup(func() { s.Shutdown(2) })

	if s.rollout != nil {
		t.Fatal("newServer must not build the controller: its tables do not exist yet")
	}
	// Until it is built, updates are WITHHELD — never waved through to the
	// unrestricted legacy announce.
	if s.rollout == nil && s.rolloutErr != nil {
		t.Fatalf("newServer must not record a controller error either: %v", s.rolloutErr)
	}

	if err := s.initDB(); err != nil {
		t.Fatalf("initDB: %v", err)
	}
	if err := s.initRollout(); err != nil {
		t.Fatalf("the controller must be usable once migrations have run: %v", err)
	}
	if s.rollout == nil {
		t.Fatal("initRollout reported success but left no controller")
	}
	if s.rolloutErr != nil {
		t.Fatalf("a usable controller must clear the retained error: %v", s.rolloutErr)
	}

	// And it actually works against the migrated schema.
	if r, _, err := s.rollout.reserve("linux/amd64", "m1",
		"aaaa000000000000000000000000000000000000000000000000000000000001", 8); err != nil {
		t.Fatalf("reserve on the migrated schema: %v", err)
	} else if r == nil {
		t.Fatal("the first machine must be admitted as the canary")
	}
}

// A controller that could not be built must withhold, not fall through.
func TestAMissingControllerWithholdsRatherThanAnnouncing(t *testing.T) {
	db, err := sql.Open("sqlite", databaseDSN(filepath.Join(t.TempDir(), "broken.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	s := newServer(db)
	t.Cleanup(func() { s.Shutdown(2) })

	// No migrations: initRollout must fail and say so.
	if err := s.initRollout(); err == nil {
		t.Fatal("building the controller against an unmigrated database must fail")
	}
	if s.rollout != nil {
		t.Fatal("a failed initialisation must leave no controller")
	}
	if s.rolloutErr == nil {
		t.Fatal("the failure must be retained so announcements can refuse")
	}
}
