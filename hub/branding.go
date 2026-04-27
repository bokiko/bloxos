package main

// Phase 10 — instance branding (title, subtitle, logo, favicon).
//
// Branding is a singleton record stored in the `branding_config` table
// (id pinned to 1). Reads are public so the dashboard can fetch the
// current branding before authentication. Writes require the
// `branding.admin` RBAC scope, which is granted to admins only.

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

const (
	maxLogoBytes    = 500 * 1024
	maxFaviconBytes = 100 * 1024
	pngMagic        = "\x89PNG\r\n\x1a\n"
)

// BrandingConfig is the public view of the branding row.
// Image bytes themselves are served via dedicated endpoints; the JSON
// payload only carries metadata so a hot-cached client can decide whether
// to refetch the image.
type BrandingConfig struct {
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	HasLogo     bool   `json:"has_logo"`
	LogoSHA     string `json:"logo_sha,omitempty"`
	HasFavicon  bool   `json:"has_favicon"`
	FaviconSHA  string `json:"favicon_sha,omitempty"`
}

// validatePNG rejects empty, oversize, or non-PNG byte slices and returns
// a 16-character SHA-256 prefix for cache-busting on the dashboard side.
func validatePNG(data []byte, maxBytes int, label string) (sha string, err error) {
	if len(data) == 0 {
		return "", fmt.Errorf("%s is empty", label)
	}
	if len(data) > maxBytes {
		return "", fmt.Errorf("%s exceeds %d bytes (got %d)", label, maxBytes, len(data))
	}
	if !bytes.HasPrefix(data, []byte(pngMagic)) {
		return "", fmt.Errorf("%s is not a PNG (magic bytes mismatch)", label)
	}
	if _, decErr := png.Decode(bytes.NewReader(data)); decErr != nil {
		return "", fmt.Errorf("%s failed PNG decode: %w", label, decErr)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16], nil
}

// handleGetBranding returns the public branding metadata. No auth.
func handleGetBranding(c echo.Context) error {
	var (
		title, subtitle           string
		logoSHA, faviconSHA       sql.NullString
		logoMime, faviconMime     sql.NullString
	)
	err := db.QueryRow(`
		SELECT title, subtitle, logo_sha, logo_mime, favicon_sha, favicon_mime
		FROM branding_config WHERE id = 1
	`).Scan(&title, &subtitle, &logoSHA, &logoMime, &faviconSHA, &faviconMime)
	if err == sql.ErrNoRows {
		// First run before migration ran — return empty branding.
		c.Response().Header().Set("Cache-Control", "public, max-age=60")
		return c.JSON(http.StatusOK, BrandingConfig{})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	cfg := BrandingConfig{
		Title:      title,
		Subtitle:   subtitle,
		HasLogo:    logoSHA.Valid && logoSHA.String != "" && logoMime.Valid,
		HasFavicon: faviconSHA.Valid && faviconSHA.String != "" && faviconMime.Valid,
	}
	if cfg.HasLogo {
		cfg.LogoSHA = logoSHA.String
	}
	if cfg.HasFavicon {
		cfg.FaviconSHA = faviconSHA.String
	}

	c.Response().Header().Set("Cache-Control", "public, max-age=60")
	return c.JSON(http.StatusOK, cfg)
}

// brandingImage represents one of the two branding image columns.
type brandingImage struct {
	dataCol string
	mimeCol string
	shaCol  string
}

var (
	brandingLogoImage    = brandingImage{dataCol: "logo_data", mimeCol: "logo_mime", shaCol: "logo_sha"}
	brandingFaviconImage = brandingImage{dataCol: "favicon_data", mimeCol: "favicon_mime", shaCol: "favicon_sha"}
)

func serveBrandingImage(c echo.Context, img brandingImage) error {
	query := fmt.Sprintf(`SELECT %s, %s, %s FROM branding_config WHERE id = 1`,
		img.dataCol, img.mimeCol, img.shaCol)
	var (
		data     []byte
		mime     sql.NullString
		shaStore sql.NullString
	)
	err := db.QueryRow(query).Scan(&data, &mime, &shaStore)
	if err == sql.ErrNoRows || len(data) == 0 || !mime.Valid || mime.String == "" {
		return c.NoContent(http.StatusNotFound)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	requested := c.QueryParam("v")
	if shaStore.Valid && requested != "" && requested == shaStore.String {
		c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		c.Response().Header().Set("Cache-Control", "public, max-age=60")
	}
	return c.Blob(http.StatusOK, mime.String, data)
}

func handleGetLogo(c echo.Context) error {
	return serveBrandingImage(c, brandingLogoImage)
}

func handleGetFavicon(c echo.Context) error {
	return serveBrandingImage(c, brandingFaviconImage)
}

// handleUpdateBrandingText accepts PATCH {title?, subtitle?}.
func handleUpdateBrandingText(c echo.Context) error {
	var body struct {
		Title    *string `json:"title,omitempty"`
		Subtitle *string `json:"subtitle,omitempty"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}

	var sets []string
	var args []any
	if body.Title != nil {
		t := strings.TrimSpace(*body.Title)
		if len(t) > 100 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "title must be 100 characters or fewer"})
		}
		sets = append(sets, "title = ?")
		args = append(args, t)
	}
	if body.Subtitle != nil {
		s := strings.TrimSpace(*body.Subtitle)
		if len(s) > 200 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "subtitle must be 200 characters or fewer"})
		}
		sets = append(sets, "subtitle = ?")
		args = append(args, s)
	}
	if len(sets) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	query := fmt.Sprintf(`UPDATE branding_config SET %s WHERE id = 1`, strings.Join(sets, ", "))
	if _, err := db.Exec(query, args...); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return handleGetBranding(c)
}

func handleUploadBrandingImage(c echo.Context, img brandingImage, maxBytes int, label string) error {
	fh, err := c.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing file field"})
	}
	if fh.Size > int64(maxBytes) {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%s exceeds %d bytes", label, maxBytes),
		})
	}
	src, err := fh.Open()
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "could not open uploaded file"})
	}
	defer src.Close()
	data, err := io.ReadAll(io.LimitReader(src, int64(maxBytes)+1))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "could not read uploaded file"})
	}
	if len(data) > maxBytes {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%s exceeds %d bytes", label, maxBytes),
		})
	}
	sha, err := validatePNG(data, maxBytes, label)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	query := fmt.Sprintf(`UPDATE branding_config SET %s = ?, %s = ?, %s = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1`,
		img.dataCol, img.mimeCol, img.shaCol)
	if _, err := db.Exec(query, data, "image/png", sha); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return handleGetBranding(c)
}

func handleUploadLogo(c echo.Context) error {
	return handleUploadBrandingImage(c, brandingLogoImage, maxLogoBytes, "logo")
}

func handleUploadFavicon(c echo.Context) error {
	return handleUploadBrandingImage(c, brandingFaviconImage, maxFaviconBytes, "favicon")
}

// handleClearBrandingImage removes a previously uploaded logo or favicon.
func handleClearBrandingImage(c echo.Context) error {
	kind := c.Param("kind")
	var img brandingImage
	switch kind {
	case "logo":
		img = brandingLogoImage
	case "favicon":
		img = brandingFaviconImage
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "kind must be logo or favicon"})
	}
	query := fmt.Sprintf(`UPDATE branding_config SET %s = NULL, %s = NULL, %s = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = 1`,
		img.dataCol, img.mimeCol, img.shaCol)
	if _, err := db.Exec(query); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return handleGetBranding(c)
}
