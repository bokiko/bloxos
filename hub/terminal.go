package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

type TerminalSession struct {
	ID            string
	MachineID     string
	TerminalToken string
	AgentWS       *websocket.Conn
	BrowserWS     *websocket.Conn
	CreatedAt     time.Time
	LastActivity  time.Time
	SourceIP      string
	UserID        string
	mu            sync.Mutex
	Done          chan struct{}
	closeOnce     sync.Once
}

// maxTerminalSessions is the maximum number of concurrent terminal sessions allowed.
// TODO: make this per-user when multi-user support is added.
const maxTerminalSessions = 3

// terminalTokenHeader carries the agent-side terminal token on the
// /ws/terminal handshake. Preferred over the deprecated ?terminal_token= query
// param, which leaks into reverse-proxy access logs.
const terminalTokenHeader = "X-Bloxos-Terminal-Token"

func configuredAllowedOrigins() []string {
	if ao := os.Getenv("ALLOWED_ORIGINS"); ao != "" {
		parts := strings.Split(ao, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if pu := strings.TrimSpace(os.Getenv("PUBLIC_URL")); pu != "" {
		return []string{pu}
	}
	return nil
}

func generateTerminalBrowserToken(sessionID, userID string) (string, error) {
	claims := jwt.MapClaims{
		"type":       "terminal_browser",
		"user_id":    userID,
		"session_id": sessionID,
		"exp":        time.Now().Add(1 * time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// handleStartTerminal creates a terminal session and tells the agent to spawn a PTY.
func handleStartTerminal(c echo.Context) error {
	ip := getRealIP(c)
	if !rateLimiter.Allow("terminal", ip, 5) {
		log.Printf("rate limit exceeded: terminal from %s", ip)
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	}

	machineID := c.Param("id")

	// Server-side PIN verification (Finding #3).
	var body struct {
		Pin string `json:"pin"`
	}
	if err := c.Bind(&body); err != nil || body.Pin == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "pin is required"})
	}
	userID := extractUserIDFromRequest(c)
	if userID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}
	if err := verifyTerminalPIN(userID, body.Pin); err != nil {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "invalid PIN"})
	}

	// Enforce max concurrent terminal sessions per user.
	termSessionsMu.RLock()
	activeCount := 0
	for _, session := range termSessions {
		if session.UserID == userID {
			activeCount++
		}
	}
	termSessionsMu.RUnlock()
	if activeCount >= maxTerminalSessions {
		log.Printf("terminal session rejected: max concurrent sessions reached (%d)", maxTerminalSessions)
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "max concurrent terminal sessions reached (3)"})
	}

	agentsMu.RLock()
	agent, ok := agents[machineID]
	agentsMu.RUnlock()
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "agent not connected"})
	}
	if agent.Conn == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "agent connection unavailable"})
	}

	// Extract audit metadata.
	sourceIP := getRealIP(c)

	sessionID := uuid.New().String()
	terminalToken := uuid.New().String()
	browserToken, err := generateTerminalBrowserToken(sessionID, userID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create terminal session"})
	}

	now := time.Now()
	session := &TerminalSession{
		ID:            sessionID,
		MachineID:     machineID,
		TerminalToken: terminalToken,
		CreatedAt:     now,
		LastActivity:  now,
		SourceIP:      sourceIP,
		UserID:        userID,
		Done:          make(chan struct{}),
	}
	termSessionsMu.Lock()
	termSessions[sessionID] = session
	termSessionsMu.Unlock()

	// Expire terminal token after 30 seconds if agent hasn't connected.
	go func() {
		time.Sleep(30 * time.Second)
		termSessionsMu.RLock()
		s, exists := termSessions[sessionID]
		termSessionsMu.RUnlock()
		if exists {
			s.mu.Lock()
			agentConnected := s.AgentWS != nil
			s.mu.Unlock()
			if !agentConnected {
				log.Printf("terminal %s: terminal_token expired (agent never connected)", sessionID)
				cleanupTerminalSession(sessionID)
			}
		}
	}()

	_, err = db.Exec(`INSERT INTO terminal_sessions (id, machine_id, source_ip, user_id) VALUES (?, ?, ?, ?)`, sessionID, machineID, sourceIP, userID)
	if err != nil {
		log.Printf("terminal session DB insert error: %v", err)
	}

	cmd := map[string]string{
		"type":           "start_terminal",
		"id":             "term-" + sessionID[:8],
		"session_id":     sessionID,
		"terminal_token": terminalToken,
	}
	cmdData, _ := json.Marshal(cmd)
	agent.WriteMu.Lock()
	err = agent.Conn.WriteMessage(websocket.TextMessage, cmdData)
	agent.WriteMu.Unlock()
	if err != nil {
		termSessionsMu.Lock()
		delete(termSessions, sessionID)
		termSessionsMu.Unlock()
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to send start_terminal to agent"})
	}

	log.Printf("terminal session %s created for machine %s", sessionID, machineID)
	return c.JSON(http.StatusOK, map[string]string{
		"session_id":    sessionID,
		"browser_token": browserToken,
	})
}

func handleCloseTerminal(c echo.Context) error {
	sessionID := c.Param("session_id")

	termSessionsMu.RLock()
	_, ok := termSessions[sessionID]
	termSessionsMu.RUnlock()
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
	}

	// Route through the single teardown path so session.Done is closed via
	// closeOnce (releasing any parked handleTerminalWS goroutine), the sockets
	// are closed, and the DB row is finalized. The old inline teardown deleted
	// the map entry and closed the sockets but never closed Done, leaking the
	// waiter goroutine on every close.
	cleanupTerminalSession(sessionID)

	log.Printf("terminal session %s closed via API", sessionID)
	return c.JSON(http.StatusOK, map[string]string{"status": "closed"})
}

func handleTerminalWS(c echo.Context) error {
	sessionID := c.Param("session_id")
	role := c.QueryParam("role")
	browserUserID := ""

	if role != "agent" && role != "browser" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "role must be 'agent' or 'browser'"})
	}

	// Auth check (Finding #4).
	if role == "agent" {
		// Agent must present a valid terminal_token matching the session.
		// Prefer the header (kept out of proxy access logs); fall back to the
		// deprecated query param for agents predating this change.
		termToken := c.Request().Header.Get(terminalTokenHeader)
		if termToken == "" {
			termToken = c.QueryParam("terminal_token") // deprecated
		}
		if termToken == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "terminal_token required for agent role"})
		}
		termSessionsMu.RLock()
		sess, exists := termSessions[sessionID]
		termSessionsMu.RUnlock()
		if !exists {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		}
		if sess.TerminalToken != termToken {
			log.Printf("terminal %s: agent presented invalid terminal_token", sessionID)
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid terminal_token"})
		}
	}
	if role == "browser" {
		tokenStr := c.QueryParam("browser_token")
		if tokenStr == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "browser_token required for browser role"})
		}
		tkn, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return jwtSecret, nil
		})
		if err != nil || !tkn.Valid {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
		}
		claims, ok := tkn.Claims.(jwt.MapClaims)
		if !ok {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid token claims"})
		}
		if tokenType, _ := claims["type"].(string); tokenType != "terminal_browser" {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "invalid browser token type"})
		}
		if tokenSessionID, _ := claims["session_id"].(string); tokenSessionID != sessionID {
			return c.JSON(http.StatusForbidden, map[string]string{"error": "browser token does not match session"})
		}
		browserUserID, _ = claims["user_id"].(string)
	}

	termSessionsMu.RLock()
	session, ok := termSessions[sessionID]
	termSessionsMu.RUnlock()
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
	}
	if role == "browser" && session.UserID != "" && browserUserID != session.UserID {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "browser token does not match session owner"})
	}

	// Use origin-checking upgrader for terminal WebSocket (Finding #4).
	termUpgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // agent connections have no origin
			}
			for _, allowed := range configuredAllowedOrigins() {
				if origin == allowed {
					return true
				}
			}
			if parsedOrigin, err := url.Parse(origin); err == nil && parsedOrigin.Host == r.Host {
				return true
			}
			log.Printf("terminal WS: rejected origin %s", origin)
			return false
		},
	}
	ws, err := termUpgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}

	session.mu.Lock()
	if role == "agent" {
		session.AgentWS = ws
		log.Printf("terminal %s: agent connected", sessionID)
	} else {
		session.BrowserWS = ws
		log.Printf("terminal %s: browser connected", sessionID)
	}

	agentWS := session.AgentWS
	browserWS := session.BrowserWS
	session.mu.Unlock()

	if agentWS != nil && browserWS != nil {
		goSafelyOnce("terminalRelay/"+sessionID, func() { terminalRelay(sessionID, session) })
	} else {
		go func() {
			time.Sleep(30 * time.Second)
			session.mu.Lock()
			a := session.AgentWS
			b := session.BrowserWS
			session.mu.Unlock()
			if a == nil || b == nil {
				log.Printf("terminal %s: timeout waiting for both sides, cleaning up", sessionID)
				cleanupTerminalSession(sessionID)
			}
		}()
	}

	<-waitForSessionEnd(sessionID)
	return nil
}

func waitForSessionEnd(sessionID string) <-chan struct{} {
	termSessionsMu.RLock()
	session, ok := termSessions[sessionID]
	termSessionsMu.RUnlock()
	if !ok {
		// Session already gone — return an already-closed channel.
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return session.Done
}

func terminalRelay(sessionID string, session *TerminalSession) {
	log.Printf("terminal %s: relay started", sessionID)
	// Terminal audit: session metadata only. No command capture by design — see hardening plan item #13.

	session.mu.Lock()
	agentWS := session.AgentWS
	browserWS := session.BrowserWS
	session.mu.Unlock()

	done := make(chan struct{})

	// Inactivity timeout goroutine: close session after 30 minutes of no traffic.
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				session.mu.Lock()
				idle := time.Since(session.LastActivity)
				session.mu.Unlock()
				if idle > 30*time.Minute {
					log.Printf("terminal %s: closing due to inactivity (%s)", sessionID, idle.Truncate(time.Second))
					select {
					case <-done:
					default:
						close(done)
					}
					return
				}
			}
		}
	}()

	go func() {
		defer func() {
			select {
			case <-done:
			default:
				close(done)
			}
		}()
		for {
			msgType, msg, err := browserWS.ReadMessage()
			if err != nil {
				log.Printf("terminal %s: browser read error: %v", sessionID, err)
				return
			}
			session.mu.Lock()
			session.LastActivity = time.Now()
			session.mu.Unlock()
			if err := agentWS.WriteMessage(msgType, msg); err != nil {
				log.Printf("terminal %s: agent write error: %v", sessionID, err)
				return
			}
		}
	}()

	go func() {
		defer func() {
			select {
			case <-done:
			default:
				close(done)
			}
		}()
		for {
			msgType, msg, err := agentWS.ReadMessage()
			if err != nil {
				log.Printf("terminal %s: agent read error: %v", sessionID, err)
				return
			}
			session.mu.Lock()
			session.LastActivity = time.Now()
			session.mu.Unlock()
			if err := browserWS.WriteMessage(msgType, msg); err != nil {
				log.Printf("terminal %s: browser write error: %v", sessionID, err)
				return
			}
		}
	}()

	<-done
	log.Printf("terminal %s: relay ended, cleaning up", sessionID)
	cleanupTerminalSession(sessionID)
}

func cleanupTerminalSession(sessionID string) {
	termSessionsMu.Lock()
	session, ok := termSessions[sessionID]
	if ok {
		delete(termSessions, sessionID)
	}
	termSessionsMu.Unlock()

	if !ok {
		return
	}

	// Signal waiters that the session has ended. closeOnce ensures this is
	// safe to call from multiple goroutines or repeatedly.
	session.closeOnce.Do(func() { close(session.Done) })

	session.mu.Lock()
	if session.AgentWS != nil {
		session.AgentWS.Close()
	}
	if session.BrowserWS != nil {
		session.BrowserWS.Close()
	}
	session.mu.Unlock()

	// Terminal audit: log session metadata on close (hardening plan #13).
	duration := time.Since(session.CreatedAt).Truncate(time.Second)
	log.Printf("terminal session %s closed: machine=%s user=%s ip=%s duration=%s",
		sessionID, session.MachineID, session.UserID, session.SourceIP, duration)

	_, _ = db.Exec(`UPDATE terminal_sessions SET ended_at = CURRENT_TIMESTAMP, status = 'closed' WHERE id = ?`, sessionID)
}
