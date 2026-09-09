package main

// Unified updater: hub API and maintenance gate for the root host worker's
// filesystem mailbox (docs/update-architecture.md). The hub only reads a bounded,
// validated outbox and writes one exclusive, allowlisted request. It never
// executes commands, never sees the docker socket or a root shell, and never
// exposes private paths or raw logs.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const (
	updaterDirEnv        = "BLOXOS_UPDATER_DIR"
	updaterMaxFileBytes  = 64 * 1024
	updaterMaxMessageLen = 300
	updaterSetupCommand  = "sudo bloxos-update init"
)

// updaterStates is the closed set the worker may publish. Anything else is
// treated as a malformed status and never passed through.
var updaterStates = map[string]bool{
	"idle": true, "checking": true, "staging": true, "backing_up": true,
	"installing": true, "verifying": true, "succeeded": true,
	"rolling_back": true, "rolled_back": true, "failed": true,
}

// updaterActiveStates are non-terminal: a new request must be rejected while
// any of these is current.
var updaterActiveStates = map[string]bool{
	"checking": true, "staging": true, "backing_up": true,
	"installing": true, "verifying": true, "rolling_back": true,
}

type updaterCapabilities struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
}

type updaterStatus struct {
	RequestID string `json:"request_id"`
	State     string `json:"state"`
	Version   string `json:"version"`
	Message   string `json:"message"`
	UpdatedAt string `json:"updated_at"`
}

// updaterDir returns the configured mailbox dir, requiring an absolute path
// so the hub can never be redirected into an unexpected location by a
// relative path. Unset or non-absolute means "not configured".
func updaterDir() (string, bool) {
	dir := strings.TrimSpace(os.Getenv(updaterDirEnv))
	if dir == "" || !filepath.IsAbs(dir) {
		return "", false
	}
	return filepath.Clean(dir), true
}

// readBoundedJSONFile reads at most updaterMaxFileBytes from a file that must
// be a regular file (never a symlink) and decodes it into v.
func readBoundedJSONFile(path string, v any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	if info.Size() > updaterMaxFileBytes {
		return fmt.Errorf("file too large")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return fmt.Errorf("file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, updaterMaxFileBytes+1))
	if err != nil || len(data) > updaterMaxFileBytes {
		return fmt.Errorf("cannot read bounded file")
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.Decode(new(any)) != io.EOF {
		return fmt.Errorf("unexpected trailing data")
	}
	return nil
}

func sanitizeUpdaterText(s string, maxLen int) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) && r != 0x7f {
			b.WriteRune(r)
		}
		if b.Len() >= maxLen {
			break
		}
	}
	return b.String()
}

// readCapabilities reads and validates outbox/capabilities.json. A missing or
// invalid file means the updater is not configured/usable, reported plainly.
func readCapabilities(dir string) (*updaterCapabilities, string, error) {
	var caps updaterCapabilities
	err := readBoundedJSONFile(filepath.Join(dir, "outbox", "capabilities.json"), &caps)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "updater not configured", nil
		}
		return nil, "updater capabilities unreadable", err
	}
	if !caps.Enabled || (caps.Mode != "native" && caps.Mode != "compose") {
		return nil, "updater capabilities invalid", fmt.Errorf("invalid capabilities")
	}
	return &caps, "", nil
}

// readStatus reads and sanitizes outbox/status.json. Absent means the worker
// has not run yet (nil status, not an error).
func readStatus(dir string) (*updaterStatus, string, error) {
	var st updaterStatus
	err := readBoundedJSONFile(filepath.Join(dir, "outbox", "status.json"), &st)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "updater status unreadable", err
	}
	if !updaterStates[st.State] {
		return nil, "updater status invalid", fmt.Errorf("unknown state")
	}
	if len(st.RequestID) > 64 || len(st.Version) > 64 || len(st.UpdatedAt) > 64 {
		return nil, "updater status invalid", fmt.Errorf("oversized field")
	}
	st.Version = sanitizeUpdaterText(st.Version, 64)
	st.Message = sanitizeUpdaterText(st.Message, updaterMaxMessageLen)
	return &st, "", nil
}

func (s *Server) handleGetSystemUpdate(c echo.Context) error {
	resp := map[string]any{
		"available":       false,
		"reason":          "updater not configured",
		"cli_command":     updaterSetupCommand,
		"current_version": buildVersion,
		"status":          nil,
	}
	dir, ok := updaterDir()
	if !ok {
		return c.JSON(http.StatusOK, resp)
	}
	caps, reason, err := readCapabilities(dir)
	if err != nil {
		log.Printf("system update: capabilities: %v", err)
	}
	if caps == nil {
		resp["reason"] = reason
		return c.JSON(http.StatusOK, resp)
	}
	st, sreason, serr := readStatus(dir)
	if serr != nil {
		log.Printf("system update: status: %v", serr)
	}
	resp["available"] = true
	resp["cli_command"] = "sudo bloxos-update update"
	resp["mode"] = caps.Mode
	if sreason != "" {
		resp["reason"] = sreason
	} else {
		resp["reason"] = ""
	}
	if st != nil {
		resp["status"] = st
	}
	return c.JSON(http.StatusOK, resp)
}

// handlePostSystemUpdate accepts exactly {"target_version":"latest"} and
// writes ONE exclusive request to the inbox. It cannot overwrite an existing
// request, follow a symlink, or proceed while an update is active.
func (s *Server) handlePostSystemUpdate(c echo.Context) error {
	var body struct {
		TargetVersion string `json:"target_version"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, 1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}
	if dec.Decode(new(any)) != io.EOF {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}
	if body.TargetVersion != "latest" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "only target_version \"latest\" is accepted"})
	}

	dir, ok := updaterDir()
	if !ok {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "updater not configured; run " + updaterSetupCommand + " on the hub host first"})
	}
	if _, reason, err := readCapabilities(dir); err != nil || reason != "" {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "updater unavailable: " + reason})
	}

	// Reject while an update is active (durable status) or a request already
	// exists — one exclusive transaction at a time.
	if st, _, serr := readStatus(dir); serr != nil {
		return c.JSON(http.StatusConflict, map[string]string{"error": "cannot determine updater state; not writing a new request"})
	} else if st != nil && updaterActiveStates[st.State] {
		return c.JSON(http.StatusConflict, map[string]string{"error": "an update is already in progress"})
	}

	inbox := filepath.Join(dir, "inbox")
	if info, err := os.Lstat(inbox); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "updater inbox unavailable"})
	}
	requestID := uuid.New().String()
	payload, _ := json.Marshal(map[string]string{"request_id": requestID, "target_version": body.TargetVersion})
	// Publish atomically: the worker's systemd.path fires on the existence of
	// inbox/request.json, so the complete, fsync'd payload must appear in one
	// link() — never an open handle the worker could read mid-write. The
	// hardlink also gives exclusivity for free: EEXIST means a request is
	// already pending, and a planted symlink is never followed.
	tmpPath := filepath.Join(inbox, "request.tmp."+requestID)
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not stage update request"})
	}
	_, err = f.Write(payload)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmpPath)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not stage update request"})
	}
	reqPath := filepath.Join(inbox, "request.json")
	if err := os.Link(tmpPath, reqPath); err != nil {
		os.Remove(tmpPath)
		if errors.Is(err, os.ErrExist) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "an update request is already pending"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not publish update request"})
	}
	os.Remove(tmpPath)
	log.Printf("system update: request %s accepted for %s by %s", requestID, body.TargetVersion, c.RealIP())
	return c.JSON(http.StatusAccepted, map[string]any{
		"request_id": requestID,
		"state":      "accepted",
		"message":    "update requested; the host worker will stage a backup before the short downtime",
	})
}

// maintenanceMiddleware blocks all hub application traffic while the worker's
// outbox/maintenance marker exists, except GET /health, GET /api/build-info
// and (authenticated) GET /api/system/update. Marker stat errors fail closed
// (traffic blocked) without exposing any path.
func (s *Server) maintenanceMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		allowed := c.Request().Method == http.MethodGet &&
			(c.Path() == "/health" || c.Path() == "/api/build-info" || c.Path() == "/api/system/update")
		if allowed {
			return next(c)
		}
		dir, ok := updaterDir()
		if !ok {
			return next(c)
		}
		_, err := os.Stat(filepath.Join(dir, "outbox", "maintenance"))
		switch {
		case err == nil:
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "maintenance in progress"})
		case errors.Is(err, os.ErrNotExist):
			return next(c)
		default:
			// Cannot determine marker state: fail closed, reveal no path.
			log.Printf("maintenance marker check failed: %v", err)
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "maintenance in progress"})
		}
	}
}
