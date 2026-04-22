package main

import (
	"database/sql"
	"strings"
	"fmt"
	"log"
)

// migration is a numbered schema change. Version is implicit from slice index.
type migration struct {
	description string
	apply       func(tx *sql.Tx) error
}

// migrations is the ordered list of schema migrations.
// Index 0 = baseline schema (version 1 after applied).
// Index 1 = first incremental migration (version 2 after applied), etc.
var migrations = []migration{
	{
		description: "baseline schema: create all tables and indexes",
		apply: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
			CREATE TABLE IF NOT EXISTS machines (
				id TEXT PRIMARY KEY,
				hostname TEXT NOT NULL,
				ip TEXT,
				os TEXT,
				status TEXT DEFAULT 'offline',
				tags TEXT DEFAULT '',
				last_seen DATETIME,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS metrics (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				machine_id TEXT NOT NULL,
				cpu_percent REAL,
				ram_used_bytes INTEGER,
				ram_total_bytes INTEGER,
				disk_used_bytes INTEGER,
				disk_total_bytes INTEGER,
				gpu_temp REAL,
				gpu_util_percent REAL,
				gpu_vram_used_bytes INTEGER,
				gpu_vram_total_bytes INTEGER,
				timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (machine_id) REFERENCES machines(id)
			);

			CREATE TABLE IF NOT EXISTS tokens (
				token_hash TEXT PRIMARY KEY,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				expires_at DATETIME NOT NULL,
				used BOOLEAN DEFAULT FALSE
			);

			CREATE TABLE IF NOT EXISTS services (
				machine_id TEXT NOT NULL,
				name TEXT NOT NULL,
				status TEXT,
				description TEXT,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY (machine_id, name)
			);

			CREATE TABLE IF NOT EXISTS containers (
				machine_id TEXT NOT NULL,
				container_id TEXT NOT NULL,
				name TEXT NOT NULL,
				status TEXT,
				image TEXT,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY (machine_id, container_id)
			);

			CREATE INDEX IF NOT EXISTS idx_metrics_machine_time ON metrics(machine_id, timestamp);

			CREATE TABLE IF NOT EXISTS gpu_metrics (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				machine_id TEXT NOT NULL,
				gpu_index INTEGER NOT NULL,
				gpu_name TEXT,
				temp_c REAL,
				util_percent REAL,
				mem_used_bytes INTEGER,
				mem_total_bytes INTEGER,
				power_watts REAL,
				fan_percent REAL,
				timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (machine_id) REFERENCES machines(id)
			);

			CREATE INDEX IF NOT EXISTS idx_gpu_metrics_machine_time ON gpu_metrics(machine_id, timestamp);

			CREATE TABLE IF NOT EXISTS terminal_sessions (
				id TEXT PRIMARY KEY,
				machine_id TEXT NOT NULL,
				started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				ended_at DATETIME,
				status TEXT DEFAULT 'active',
				FOREIGN KEY (machine_id) REFERENCES machines(id)
			);

			CREATE TABLE IF NOT EXISTS alert_rules (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				metric TEXT NOT NULL,
				operator TEXT NOT NULL,
				threshold REAL NOT NULL,
				duration_secs INTEGER DEFAULT 0,
				severity TEXT DEFAULT 'warning',
				enabled BOOLEAN DEFAULT TRUE,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS alerts (
				id TEXT PRIMARY KEY,
				rule_id TEXT,
				machine_id TEXT NOT NULL,
				message TEXT NOT NULL,
				severity TEXT NOT NULL,
				status TEXT DEFAULT 'active',
				triggered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				resolved_at DATETIME,
				FOREIGN KEY (rule_id) REFERENCES alert_rules(id),
				FOREIGN KEY (machine_id) REFERENCES machines(id)
			);

			CREATE TABLE IF NOT EXISTS users (
				id TEXT PRIMARY KEY,
				username TEXT UNIQUE NOT NULL,
				password_hash TEXT NOT NULL,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
			`)
			return err
		},
	},
	{
		description: "add tags, terminal_pin_hash, password_changed, pin_changed columns",
		apply: func(tx *sql.Tx) error {
			stmts := []string{
				`ALTER TABLE machines ADD COLUMN tags TEXT DEFAULT ''`,
				`ALTER TABLE users ADD COLUMN terminal_pin_hash TEXT`,
				`ALTER TABLE users ADD COLUMN password_changed BOOLEAN DEFAULT FALSE`,
				`ALTER TABLE users ADD COLUMN pin_changed BOOLEAN DEFAULT FALSE`,
			}
			for _, s := range stmts {
				if _, err := tx.Exec(s); err != nil {
					// Column may already exist on legacy DBs that ran the
					// ad-hoc ALTER before this migration system was added.
					// SQLite returns "duplicate column name" — safe to skip.
					if isDuplicateColumnErr(err) {
						continue
					}
					return fmt.Errorf("migration stmt %q: %w", s, err)
				}
			}
			return nil
		},
	},
	{
		description: "add agent_credentials table for durable enrollment",
		apply: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
			CREATE TABLE IF NOT EXISTS agent_credentials (
				machine_id TEXT PRIMARY KEY,
				secret_hash TEXT NOT NULL,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				last_used_at DATETIME
			);
			`)
			return err
		},
	},
	{
		description: "add source_ip and user_id to terminal_sessions for audit logging",
		apply: func(tx *sql.Tx) error {
			stmts := []string{
				`ALTER TABLE terminal_sessions ADD COLUMN source_ip TEXT`,
				`ALTER TABLE terminal_sessions ADD COLUMN user_id TEXT`,
			}
			for _, s := range stmts {
				if _, err := tx.Exec(s); err != nil {
					if isDuplicateColumnErr(err) {
						continue
					}
					return fmt.Errorf("migration stmt %q: %w", s, err)
				}
			}
			return nil
		},
	},
}

// isDuplicateColumnErr returns true when SQLite rejects an ALTER TABLE ADD
// COLUMN because the column already exists.
func isDuplicateColumnErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// modernc.org/sqlite returns "duplicate column name: <col>"
	return strings.Contains(msg, "duplicate column name")
}

// runMigrations brings the database schema up to the latest version.
// Each migration runs inside its own transaction for atomicity.
func runMigrations(database *sql.DB) error {
	// Ensure the version-tracking table exists.
	_, err := database.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("create schema_version table: %w", err)
	}

	// Read current version (0 means no migrations have ever run).
	var current int
	err = database.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&current)
	if err == sql.ErrNoRows {
		// First run — seed with version 0.
		if _, err2 := database.Exec(`INSERT INTO schema_version (version) VALUES (0)`); err2 != nil {
			return fmt.Errorf("seed schema_version: %w", err2)
		}
		current = 0
	} else if err != nil {
		return fmt.Errorf("read schema_version: %w", err)
	}

	target := len(migrations)
	if current >= target {
		log.Printf("schema: at version %d (up to date)", current)
		return nil
	}

	log.Printf("schema: at version %d, target %d — applying %d migration(s)", current, target, target-current)

	for i := current; i < target; i++ {
		m := migrations[i]
		log.Printf("schema: migration %d->%d: %s", i, i+1, m.description)

		tx, err := database.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for migration %d: %w", i+1, err)
		}

		if err := m.apply(tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d failed: %w", i+1, err)
		}

		if _, err := tx.Exec(`UPDATE schema_version SET version = ?`, i+1); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("update schema_version to %d: %w", i+1, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", i+1, err)
		}

		log.Printf("schema: migration %d->%d complete", i, i+1)
	}

	log.Printf("schema: all migrations applied — now at version %d", target)
	return nil
}
