package main

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

/* ============================================================================
 * Phase 7 — fleet refresh
 *
 * POST /api/machines/:id/refresh — refresh single machine
 * POST /api/refresh                — refresh entire fleet
 *
 * Both endpoints push a `refresh_metrics` command over the existing agent
 * WebSocket. The agent responds by calling sendAll() immediately rather than
 * waiting for its next 30s tick. Fresh metrics arrive at the dashboard via
 * the existing SSE stream; these handlers don't need to wait or relay.
 *
 * RBAC: fleet.control scope (operators + admins).
 * ============================================================================ */

func (s *Server) handleMachineRefresh(c echo.Context) error {
	machineID := c.Param("id")

	s.agentsMu.RLock()
	agent, ok := s.agents[machineID]
	s.agentsMu.RUnlock()
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "agent not connected"})
	}

	cmdID := "refresh-" + uuid.New().String()[:8]
	cmd := CommandToAgent{
		Type:   "refresh_metrics",
		Target: "",
		ID:     cmdID,
	}
	cmdData, _ := json.Marshal(cmd)

	agent.WriteMu.Lock()
	err := agent.Conn.WriteMessage(websocket.TextMessage, cmdData)
	agent.WriteMu.Unlock()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to send refresh"})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"status":     "refresh requested",
		"machine_id": machineID,
	})
}

func (s *Server) handleFleetRefresh(c echo.Context) error {
	s.agentsMu.RLock()
	machineIDs := make([]string, 0, len(s.agents))
	for id := range s.agents {
		machineIDs = append(machineIDs, id)
	}
	s.agentsMu.RUnlock()

	refreshed := 0
	for _, id := range machineIDs {
		s.agentsMu.RLock()
		agent, ok := s.agents[id]
		s.agentsMu.RUnlock()
		if !ok {
			continue
		}

		cmdID := "refresh-" + uuid.New().String()[:8]
		cmd := CommandToAgent{
			Type:   "refresh_metrics",
			Target: "",
			ID:     cmdID,
		}
		cmdData, _ := json.Marshal(cmd)

		agent.WriteMu.Lock()
		err := agent.Conn.WriteMessage(websocket.TextMessage, cmdData)
		agent.WriteMu.Unlock()
		if err == nil {
			refreshed++
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"status":    "refresh requested",
		"refreshed": refreshed,
		"total":     len(machineIDs),
	})
}
