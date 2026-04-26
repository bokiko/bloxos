package main

import (
	cryptoRand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

func ensureDefaultAdmin() {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	if err != nil {
		log.Printf("error checking users count: %v", err)
		return
	}
	if count > 0 {
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("bloxos"), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("error hashing default password: %v", err)
		return
	}

	id := uuid.New().String()
	_, err = db.Exec(`INSERT INTO users (id, username, password_hash) VALUES (?, ?, ?)`,
		id, "admin", string(hash))
	if err != nil {
		log.Printf("error creating default admin: %v", err)
		return
	}
	log.Println("created default admin user (admin/bloxos)")
}

// --- First-Boot Setup (Finding #11) ---

// setupTokenValue holds the current setup token in memory (for env-provided tokens
// or file-based tokens). Cleared after successful setup.
var setupTokenValue string

// generateSetupToken creates a one-time setup token if no users exist.
// Token source priority: BLOXOS_SETUP_TOKEN env var > file > auto-generate.
func generateSetupToken() {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		log.Printf("setup: error checking user count: %v", err)
		return
	}
	if count > 0 {
		return // Users exist, no setup needed.
	}

	// Check env var first.
	if envToken := os.Getenv("BLOXOS_SETUP_TOKEN"); envToken != "" {
		setupTokenValue = envToken
		log.Println("Setup token loaded from BLOXOS_SETUP_TOKEN env var")
		log.Println("==========================================================")
		log.Println("FIRST-BOOT SETUP REQUIRED")
		log.Println("  POST /api/setup with the provided setup token")
		log.Println("==========================================================")
		return
	}

	// Check if token file already exists.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("setup: cannot determine home dir: %v", err)
		return
	}
	tokenDir := homeDir + "/.bloxos"
	tokenFile := tokenDir + "/setup-token"

	if data, err := os.ReadFile(tokenFile); err == nil && len(data) > 0 {
		setupTokenValue = strings.TrimSpace(string(data))
		log.Printf("Setup token loaded from %s", tokenFile)
		log.Println("==========================================================")
		log.Println("FIRST-BOOT SETUP REQUIRED")
		log.Printf("  Setup token is in %s", tokenFile)
		log.Println("  POST /api/setup with the token to create your admin user")
		log.Println("==========================================================")
		return
	}

	// Generate new token.
	tokenBytes := make([]byte, 16)
	if _, err := cryptoRand.Read(tokenBytes); err != nil {
		log.Printf("setup: failed to generate random token: %v", err)
		return
	}
	token := hex.EncodeToString(tokenBytes) // 32-char hex string

	if err := os.MkdirAll(tokenDir, 0700); err != nil {
		log.Printf("setup: cannot create %s: %v", tokenDir, err)
		return
	}
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		log.Printf("setup: cannot write token file: %v", err)
		return
	}

	setupTokenValue = token
	log.Printf("Setup token written to %s", tokenFile)
	log.Println("==========================================================")
	log.Println("FIRST-BOOT SETUP REQUIRED")
	log.Printf("  Setup token is in %s", tokenFile)
	log.Println("  POST /api/setup with the token to create your admin user")
	log.Println("==========================================================")
}

// handleSetupStatus returns whether the system needs first-boot setup.
func handleSetupStatus(c echo.Context) error {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusOK, map[string]bool{"needs_setup": count == 0})
}

// handleSetup processes the first-boot setup request.
func handleSetup(c echo.Context) error {
	// Check if setup is still needed.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if count > 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "setup already completed"})
	}

	// Rate limit.
	ip := getRealIP(c)
	if !rateLimiter.Allow("setup", ip, 5) {
		log.Printf("rate limit exceeded: setup from %s", ip)
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	}

	var body struct {
		SetupToken string `json:"setup_token"`
		Username   string `json:"username"`
		Password   string `json:"password"`
		PIN        string `json:"pin"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}

	// Validate setup token.
	if setupTokenValue == "" || body.SetupToken != setupTokenValue {
		log.Printf("setup: invalid setup token attempt from %s", ip)
		return c.JSON(http.StatusForbidden, map[string]string{"error": "invalid setup token"})
	}

	// Validate inputs.
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "username is required"})
	}
	if len(body.Password) < 8 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
	}
	if !regexp.MustCompile(`^\d{4,}$`).MatchString(body.PIN) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "PIN must be at least 4 digits"})
	}

	// Hash password and PIN.
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
	}
	pinHash, err := bcrypt.GenerateFromPassword([]byte(body.PIN), bcrypt.DefaultCost)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash PIN"})
	}

	// Create admin user with credentials already rotated.
	id := uuid.New().String()
	_, err = db.Exec(`INSERT INTO users (id, username, password_hash, terminal_pin_hash, password_changed, pin_changed, role) VALUES (?, ?, ?, ?, TRUE, TRUE, ?)`,
		id, body.Username, string(passwordHash), string(pinHash), RoleAdmin)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create user"})
	}

	// Clear the setup token and delete the file.
	setupTokenValue = ""
	homeDir, err := os.UserHomeDir()
	if err == nil {
		tokenFile := homeDir + "/.bloxos/setup-token"
		os.Remove(tokenFile) // Best-effort deletion.
	}

	log.Printf("setup: admin user '%s' created via first-boot setup", body.Username)
	return c.JSON(http.StatusOK, map[string]interface{}{
		"message":  "Setup complete",
		"username": body.Username,
	})
}

func handleLogin(c echo.Context) error {
	ip := getRealIP(c)
	if !rateLimiter.Allow("login", ip, 5) {
		log.Printf("rate limit exceeded: login from %s", ip)
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}

	var userID, storedUsername, passwordHash, rawRole string
	err := db.QueryRow(`SELECT id, username, password_hash, COALESCE(role, 'admin') FROM users WHERE username = ?`, body.Username).
		Scan(&userID, &storedUsername, &passwordHash, &rawRole)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	role, ok := normalizeUserRole(rawRole)
	if !ok {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "invalid user role"})
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(body.Password)); err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
	}

	// Generate JWT.
	claims := jwt.MapClaims{
		"user_id":  userID,
		"username": storedUsername,
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString(jwtSecret)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
	}

	// Check if password change is required (Finding #2).
	var passwordChanged sql.NullBool
	db.QueryRow(`SELECT password_changed FROM users WHERE username = ?`, storedUsername).Scan(&passwordChanged)
	pwChangeRequired := !passwordChanged.Valid || !passwordChanged.Bool

	// Check if PIN change is required (Finding #2).
	var pinChanged sql.NullBool
	db.QueryRow(`SELECT pin_changed FROM users WHERE username = ?`, storedUsername).Scan(&pinChanged)
	pinChangeRequired := !pinChanged.Valid || !pinChanged.Bool

	return c.JSON(http.StatusOK, map[string]interface{}{
		"token":                    tokenStr,
		"expires_in":               86400,
		"role":                     role,
		"scopes":                   scopesForRole(role),
		"password_change_required": pwChangeRequired,
		"pin_change_required":      pinChangeRequired,
	})
}

func jwtMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Allow SSE with token query param.
		tokenStr := ""
		auth := c.Request().Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			tokenStr = auth[7:]
		}
		if tokenStr == "" {
			tokenStr = c.QueryParam("token")
		}
		if tokenStr == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token"})
		}

		token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return jwtSecret, nil
		})

		if err != nil || !token.Valid {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
		}

		// Reject SSE-scoped tokens from non-SSE endpoints (Finding #8).
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			if tokenType, _ := claims["type"].(string); tokenType == "sse" {
				path := c.Request().URL.Path
				if path != "/api/events" {
					return c.JSON(http.StatusForbidden, map[string]string{"error": "SSE token cannot access this endpoint"})
				}
			}
			userID, _ := claims["user_id"].(string)
			username, _ := claims["username"].(string)
			setAuthClaimsOnContext(c, userID, username)
		}

		return next(c)
	}
}

// rotationAllowlist contains paths that work even when credential rotation is incomplete.
var rotationAllowlist = map[string]bool{
	"/api/auth/login":           true,
	"/api/auth/change-password": true,
	"/api/auth/change-pin":      true,
	"/api/auth/sse-token":       true,
	"/health":                   true,
}

// credentialRotationMiddleware blocks access to protected endpoints until the user
// has changed both the default password and PIN (Finding #10).
func credentialRotationMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Skip allowlisted paths.
		path := c.Request().URL.Path
		if rotationAllowlist[path] {
			return next(c)
		}

		claims, ok := authClaimsFromContext(c)
		if !ok {
			return next(c)
		}
		userID := claims.UserID
		if userID == "" {
			return next(c)
		}

		var passwordChanged, pinChanged sql.NullBool
		err := db.QueryRow(`SELECT password_changed, pin_changed FROM users WHERE id = ?`, userID).Scan(&passwordChanged, &pinChanged)
		if err != nil {
			// User not found or DB error — let the request proceed (handler will fail on its own).
			return next(c)
		}

		pwOK := passwordChanged.Valid && passwordChanged.Bool
		pinOK := pinChanged.Valid && pinChanged.Bool

		if !pwOK || !pinOK {
			return c.JSON(http.StatusForbidden, map[string]interface{}{
				"error":            "credentials_not_rotated",
				"message":          "You must change your default password and PIN before accessing this resource",
				"password_changed": pwOK,
				"pin_changed":      pinOK,
			})
		}

		return next(c)
	}
}

func extractUserIDFromRequest(c echo.Context) string {
	if claims, ok := authClaimsFromContext(c); ok && claims.UserID != "" {
		return claims.UserID
	}
	auth := c.Request().Header.Get("Authorization")
	tokenStr := ""
	if len(auth) > 7 && auth[:7] == "Bearer " {
		tokenStr = auth[7:]
	}
	if tokenStr == "" {
		tokenStr = c.QueryParam("token")
	}
	if tokenStr == "" {
		return ""
	}
	tkn, _ := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})
	if tkn == nil {
		return ""
	}
	claims, ok := tkn.Claims.(jwt.MapClaims)
	if !ok {
		return ""
	}
	userID, _ := claims["user_id"].(string)
	return userID
}

// loadOrGenerateJWTSecret loads from file or generates a new secret (Finding #2).
func loadOrGenerateJWTSecret() []byte {
	// Check env var first (explicit override).
	if envSecret := os.Getenv("BLOXOS_JWT_SECRET"); envSecret != "" {
		log.Println("JWT secret loaded from BLOXOS_JWT_SECRET env var")
		return []byte(envSecret)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("WARNING: cannot determine home dir, using random JWT secret: %v", err)
		return generateRandomSecret()
	}

	secretDir := homeDir + "/.bloxos"
	secretFile := secretDir + "/jwt-secret"

	// Try to read existing secret.
	data, err := os.ReadFile(secretFile)
	if err == nil && len(data) >= 32 {
		log.Printf("JWT secret loaded from %s", secretFile)
		return data
	}

	// Generate new secret.
	secret := generateRandomSecret()
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		log.Printf("WARNING: cannot create %s: %v", secretDir, err)
		return secret
	}
	if err := os.WriteFile(secretFile, secret, 0600); err != nil {
		log.Printf("WARNING: cannot write %s: %v", secretFile, err)
		return secret
	}
	log.Printf("JWT secret generated and saved to %s", secretFile)
	return secret
}

func generateRandomSecret() []byte {
	secret := make([]byte, 32)
	if _, err := cryptoRand.Read(secret); err != nil {
		log.Fatalf("failed to generate random JWT secret: %v", err)
	}
	return secret
}

// warnDefaultPassword logs a warning if admin still uses default password (Finding #2).
func warnDefaultPassword() {
	var passwordHash string
	err := db.QueryRow(`SELECT password_hash FROM users WHERE username = 'admin'`).Scan(&passwordHash)
	if err != nil {
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte("bloxos")) == nil {
		log.Println("==========================================================")
		log.Println("WARNING: Using default admin password. Change it via the API.")
		log.Println("  POST /api/auth/change-password")
		log.Println("==========================================================")
	}
}

// handleChangePassword allows authenticated users to change their password (Finding #2).
func handleChangePassword(c echo.Context) error {
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}
	if body.NewPassword == "" || len(body.NewPassword) < 6 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "new password must be at least 6 characters"})
	}

	// Get user from JWT.
	auth := c.Request().Header.Get("Authorization")
	tokenStr := ""
	if strings.HasPrefix(auth, "Bearer ") {
		tokenStr = auth[7:]
	}
	if tokenStr == "" {
		tokenStr = c.QueryParam("token")
	}

	tkn, _ := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})
	claims, ok := tkn.Claims.(jwt.MapClaims)
	if !ok {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid token"})
	}
	username, _ := claims["username"].(string)
	if username == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid token"})
	}

	var passwordHash string
	err := db.QueryRow(`SELECT password_hash FROM users WHERE username = ?`, username).Scan(&passwordHash)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "user not found"})
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(body.CurrentPassword)); err != nil {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "current password is incorrect"})
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
	}
	_, err = db.Exec(`UPDATE users SET password_hash = ?, password_changed = TRUE WHERE username = ?`, string(newHash), username)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update password"})
	}

	log.Printf("password changed for user: %s", username)
	return c.JSON(http.StatusOK, map[string]string{"status": "password changed"})
}

// verifyTerminalPIN checks a PIN against the stored hash (Finding #3).
func verifyTerminalPIN(userID, pin string) error {
	if userID == "" {
		return fmt.Errorf("missing user context")
	}
	var pinHash sql.NullString
	err := db.QueryRow(`SELECT terminal_pin_hash FROM users WHERE id = ?`, userID).Scan(&pinHash)
	if err != nil || !pinHash.Valid || pinHash.String == "" {
		return fmt.Errorf("invalid PIN")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(pinHash.String), []byte(pin)); err != nil {
		return fmt.Errorf("invalid PIN")
	}
	return nil
}

// handleChangePIN allows authenticated users to change the terminal PIN (Finding #3).
func handleChangePIN(c echo.Context) error {
	var body struct {
		CurrentPIN string `json:"current_pin"`
		NewPIN     string `json:"new_pin"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}
	if body.NewPIN == "" || len(body.NewPIN) < 4 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "new PIN must be at least 4 characters"})
	}

	userID := extractUserIDFromRequest(c)
	if userID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing user context"})
	}
	if err := verifyTerminalPIN(userID, body.CurrentPIN); err != nil {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "current PIN is incorrect"})
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPIN), bcrypt.DefaultCost)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash PIN"})
	}
	_, err = db.Exec(`UPDATE users SET terminal_pin_hash = ?, pin_changed = TRUE WHERE id = ?`, string(newHash), userID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to update PIN"})
	}

	log.Println("terminal PIN changed")
	return c.JSON(http.StatusOK, map[string]string{"status": "PIN changed"})
}

// handleSSEToken generates a short-lived SSE-scoped JWT (Finding #8).
func handleSSEToken(c echo.Context) error {
	claims, ok := authClaimsFromContext(c)
	if !ok || claims.UserID == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid token"})
	}

	// Generate SSE-scoped token with 5-minute expiry.
	sseClaims := jwt.MapClaims{
		"user_id":  claims.UserID,
		"username": claims.Username,
		"type":     "sse",
		"exp":      time.Now().Add(5 * time.Minute).Unix(),
	}
	sseToken := jwt.NewWithClaims(jwt.SigningMethodHS256, sseClaims)
	sseTokenStr, err := sseToken.SignedString(jwtSecret)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"token":      sseTokenStr,
		"type":       "sse",
		"expires_in": 300,
	})
}
