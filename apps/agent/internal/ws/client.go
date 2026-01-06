package ws

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Message types
const (
	TypeAuth          = "auth"
	TypeAuthenticated = "authenticated"
	TypeStats         = "stats"
	TypeHeartbeat     = "heartbeat"
	TypeHeartbeatAck  = "heartbeat_ack"
	TypeCommand       = "command"
	TypeCommandResult = "command_result"
	TypeMinerStatus   = "miner_status"
	TypeError         = "error"
)

// Message represents a WebSocket message
type Message struct {
	Type      string      `json:"type"`
	Token     string      `json:"token,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Command   *Command    `json:"command,omitempty"`
	CommandID string      `json:"commandId,omitempty"`
	Success   bool        `json:"success,omitempty"`
	Error     string      `json:"error,omitempty"`
	RigID     string      `json:"rigId,omitempty"`
	RigName   string      `json:"rigName,omitempty"`
	Message   string      `json:"message,omitempty"`
	Timestamp int64       `json:"timestamp,omitempty"`
}

// Command represents a command from the server
type Command struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload,omitempty"`
	CreatedAt time.Time   `json:"createdAt"`
}

// CommandHandler is a function that handles commands from the server
type CommandHandler func(cmd *Command) (success bool, err error)

// Client is a WebSocket client with auto-reconnect
type Client struct {
	serverURL      string
	token          string
	conn           *websocket.Conn
	connected      bool
	authenticated  bool
	rigID          string
	rigName        string
	mu             sync.RWMutex
	done           chan struct{}
	reconnectDelay time.Duration
	maxReconnect   time.Duration
	debug          bool

	// Handlers
	onCommand CommandHandler
	onConnect func()
	onDisconnect func()

	// Heartbeat
	heartbeatInterval time.Duration
	heartbeatTicker   *time.Ticker
}

// NewClient creates a new WebSocket client
func NewClient(serverURL, token string, debug bool) *Client {
	return &Client{
		serverURL:         serverURL,
		token:             token,
		debug:             debug,
		done:              make(chan struct{}),
		reconnectDelay:    1 * time.Second,
		maxReconnect:      60 * time.Second,
		heartbeatInterval: 30 * time.Second,
	}
}

// SetCommandHandler sets the handler for commands from the server
func (c *Client) SetCommandHandler(handler CommandHandler) {
	c.onCommand = handler
}

// SetConnectHandler sets the handler called when connected
func (c *Client) SetConnectHandler(handler func()) {
	c.onConnect = handler
}

// SetDisconnectHandler sets the handler called when disconnected
func (c *Client) SetDisconnectHandler(handler func()) {
	c.onDisconnect = handler
}

// Connect starts the WebSocket connection with auto-reconnect
func (c *Client) Connect() error {
	go c.connectLoop()
	return nil
}

// connectLoop handles connection and reconnection
func (c *Client) connectLoop() {
	delay := c.reconnectDelay

	for {
		select {
		case <-c.done:
			return
		default:
		}

		err := c.connect()
		if err != nil {
			log.Printf("WebSocket connection failed: %v", err)
			
			// Exponential backoff
			log.Printf("Reconnecting in %v...", delay)
			time.Sleep(delay)
			delay = delay * 2
			if delay > c.maxReconnect {
				delay = c.maxReconnect
			}
			continue
		}

		// Reset delay on successful connection
		delay = c.reconnectDelay

		// Read messages until disconnection
		c.readLoop()

		// Disconnected
		c.mu.Lock()
		c.connected = false
		c.authenticated = false
		if c.conn != nil {
			c.conn.Close()
			c.conn = nil
		}
		c.mu.Unlock()

		if c.onDisconnect != nil {
			c.onDisconnect()
		}

		log.Println("WebSocket disconnected, reconnecting...")
	}
}

// connect establishes the WebSocket connection
func (c *Client) connect() error {
	// Parse server URL and convert to WebSocket URL
	u, err := url.Parse(c.serverURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	// Convert http(s) to ws(s)
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// Already WebSocket
	default:
		u.Scheme = "ws"
	}

	// Set WebSocket path with token as query parameter
	u.Path = "/api/agent/ws"
	q := u.Query()
	q.Set("token", c.token)
	u.RawQuery = q.Encode()

	if c.debug {
		log.Printf("Connecting to %s", u.String())
	}

	// Connect
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	// Wait for authentication response
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to read auth response: %w", err)
	}

	var msg Message
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		conn.Close()
		return fmt.Errorf("failed to parse auth response: %w", err)
	}

	if msg.Type == TypeError {
		conn.Close()
		return fmt.Errorf("auth failed: %s", msg.Message)
	}

	if msg.Type != TypeAuthenticated {
		conn.Close()
		return fmt.Errorf("unexpected response type: %s", msg.Type)
	}

	c.mu.Lock()
	c.authenticated = true
	c.rigID = msg.RigID
	c.rigName = msg.RigName
	c.mu.Unlock()

	log.Printf("Connected and authenticated as rig: %s (%s)", c.rigName, c.rigID)

	// Start heartbeat
	c.startHeartbeat()

	if c.onConnect != nil {
		c.onConnect()
	}

	return nil
}

// readLoop reads messages from the WebSocket
func (c *Client) readLoop() {
	for {
		select {
		case <-c.done:
			return
		default:
		}

		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()

		if conn == nil {
			return
		}

		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			return
		}

		var msg Message
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			log.Printf("Failed to parse message: %v", err)
			continue
		}

		c.handleMessage(&msg)
	}
}

// handleMessage processes incoming messages
func (c *Client) handleMessage(msg *Message) {
	switch msg.Type {
	case TypeHeartbeatAck:
		if c.debug {
			log.Printf("Heartbeat acknowledged")
		}

	case TypeCommand:
		if msg.Command != nil {
			log.Printf("Received command: %s (ID: %s)", msg.Command.Type, msg.Command.ID)
			c.handleCommand(msg.Command)
		}

	case TypeError:
		log.Printf("Server error: %s", msg.Message)

	default:
		if c.debug {
			log.Printf("Unknown message type: %s", msg.Type)
		}
	}
}

// handleCommand processes a command from the server
func (c *Client) handleCommand(cmd *Command) {
	var success bool
	var errMsg string

	if c.onCommand != nil {
		ok, err := c.onCommand(cmd)
		success = ok
		if err != nil {
			errMsg = err.Error()
		}
	} else {
		errMsg = "no command handler registered"
	}

	// Send result back to server
	result := Message{
		Type:      TypeCommandResult,
		CommandID: cmd.ID,
		Success:   success,
		Error:     errMsg,
	}

	if err := c.Send(&result); err != nil {
		log.Printf("Failed to send command result: %v", err)
	}
}

// startHeartbeat starts the heartbeat ticker
func (c *Client) startHeartbeat() {
	if c.heartbeatTicker != nil {
		c.heartbeatTicker.Stop()
	}

	c.heartbeatTicker = time.NewTicker(c.heartbeatInterval)

	go func() {
		for {
			select {
			case <-c.done:
				return
			case <-c.heartbeatTicker.C:
				c.mu.RLock()
				connected := c.connected
				c.mu.RUnlock()

				if !connected {
					return
				}

				msg := &Message{Type: TypeHeartbeat}
				if err := c.Send(msg); err != nil {
					log.Printf("Failed to send heartbeat: %v", err)
					return
				}

				if c.debug {
					log.Printf("Heartbeat sent")
				}
			}
		}
	}()
}

// Send sends a message to the server
func (c *Client) Send(msg *Message) error {
	c.mu.RLock()
	conn := c.conn
	connected := c.connected
	c.mu.RUnlock()

	if !connected || conn == nil {
		return fmt.Errorf("not connected")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	return nil
}

// SendStats sends stats to the server
func (c *Client) SendStats(data interface{}) error {
	msg := &Message{
		Type: TypeStats,
		Data: data,
	}
	return c.Send(msg)
}

// SendMinerStatus sends miner status to the server
func (c *Client) SendMinerStatus(data interface{}) error {
	msg := &Message{
		Type: TypeMinerStatus,
		Data: data,
	}
	return c.Send(msg)
}

// IsConnected returns true if connected and authenticated
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected && c.authenticated
}

// Close closes the WebSocket connection
func (c *Client) Close() {
	close(c.done)

	if c.heartbeatTicker != nil {
		c.heartbeatTicker.Stop()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.conn.Close()
		c.conn = nil
	}

	c.connected = false
	c.authenticated = false
}

// GetRigID returns the rig ID assigned by the server
func (c *Client) GetRigID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rigID
}

// GetRigName returns the rig name assigned by the server
func (c *Client) GetRigName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rigName
}
