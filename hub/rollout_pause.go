package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

const operatorRolloutPauseKey = "agent_rollout_operator_paused"

// operatorRolloutPause reads the durable operator intent, separate from the
// automatic per-build failure breaker. Missing means never paused. Unreadable
// or corrupt state withholds announcements rather than silently resuming.
func (s *Server) operatorRolloutPause() (bool, string) {
	var value string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.db.QueryRowContext(ctx, `SELECT value FROM hub_settings WHERE key = ?`, operatorRolloutPauseKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ""
	}
	if err != nil {
		log.Printf("rollout: operator pause state unreadable: %v", err)
		return true, "operator pause state unreadable; check hub database health before resuming"
	}
	switch value {
	case "0":
		return false, ""
	case "1":
		return true, "manually paused by operator (saved across restarts)"
	default:
		return true, "operator pause state invalid; explicitly pause or resume after checking hub database health"
	}
}

// Callers hold operatorRolloutMu exclusively through persistence and any
// automatic-breaker reset. Announcements hold its read side through enqueue,
// so a successful pause response is a barrier for new announcements. It cannot
// cancel an update frame already queued or an agent update already in flight.
// setOperatorRolloutPauseTx is setOperatorRolloutPause inside a caller's
// transaction, so clearing the pause can be committed together with the slot
// resets that make a resume mean anything.
func setOperatorRolloutPauseTx(tx *sql.Tx, paused bool) error {
	value := "0"
	if paused {
		value = "1"
	}
	_, err := tx.Exec(`INSERT INTO hub_settings (key, value, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		operatorRolloutPauseKey, value)
	return err
}

func (s *Server) setOperatorRolloutPause(paused bool) error {
	value := "0"
	if paused {
		value = "1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `INSERT INTO hub_settings (key, value, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		operatorRolloutPauseKey, value)
	return err
}
