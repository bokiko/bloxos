package main

import (
	"database/sql"
	"sync"
)

// Server owns the per-instance state the hub's handlers operate on. Hanging
// this off a struct (rather than package-level globals) lets each test build a
// fully isolated instance instead of reassigning shared globals.
type Server struct {
	db *sql.DB

	// Connected agents keyed by machine ID.
	agents   map[string]*ConnectedAgent
	agentsMu sync.RWMutex
}

// newServer returns a Server backed by db with an empty agent registry.
func newServer(db *sql.DB) *Server {
	return &Server{
		db:     db,
		agents: make(map[string]*ConnectedAgent),
	}
}
