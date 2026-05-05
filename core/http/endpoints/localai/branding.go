package localai

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
)

// brandingDirName is the subdirectory under DynamicConfigsDir that holds
// uploaded branding assets (logo, horizontal logo, favicon).
const brandingDirName = "branding"

// maxBrandingAssetBytes caps a single uploaded branding asset. Logos and
// favicons are tiny in practice; this just keeps a misclick from filling
// the disk.
const maxBrandingAssetBytes = 5 * 1024 * 1024 // 5 MiB

// brandingAssetKinds enumerates the asset slots an admin may override.
// The :kind path parameter must match one of these.
var brandingAssetKinds = map[string]struct{}{
	"logo":            {},
	"logo_horizontal": {},
	"favicon":         {},
}

// brandingAssetMimeTypes is the allow-list of Content-Type values the upload
// handler accepts. Anything else is rejected with 400 — admins are trusted
// but this keeps an HTML/JS payload from being served back as a "logo".
var brandingAssetMimeTypes = map[string]string{
	"image/png":            ".png",
	"image/jpeg":           ".jpg",
	"image/svg+xml":        ".svg",
	"image/webp":           ".webp",
	"image/x-icon":         ".ico",
	"image/vnd.microsoft.icon": ".ico",
}

// brandingDefaultURLs maps each asset kind to the bundled fallback URL the
// React UI should use when the admin has not uploaded an override.
var brandingDefaultURLs = map[string]string{
	"logo":            "/static/logo.png",
	"logo_horizontal": "/static/logo_horizontal.png",
	"favicon":         "/favicon.svg",
}

// BrandingResponse is the JSON shape returned by GET /api/branding. It is
// intentionally narrow — no other settings leak through this public endpoint.
type BrandingResponse struct {
	InstanceName      string `json:"instance_name"`
	InstanceTagline   string `json:"instance_tagline"`
	LogoURL           string `json:"logo_url"`
	LogoHorizontalURL string `json:"logo_horizontal_url"`
	FaviconURL        string `json:"favicon_url"`
}

// brandingAssetURL returns the URL the UI should use for a given asset kind:
// the dynamic /branding/asset/:kind route when an upload is set, or the
// bundled default otherwise.
func brandingAssetURL(kind, file string) string {
	if file != "" {
		return "/branding/asset/" + kind
	}
	return brandingDefaultURLs[kind]
}

// GetBrandingEndpoint exposes the public branding configuration. It is
// intentionally unauthenticated so the React login page (rendered before
// auth completes) can fetch the instance name and logo. Only the five
// branding fields are returned — never API keys or other settings.
//
// @Summary Get instance branding
// @Description Returns the configured instance name, tagline, and asset URLs. Public — no authentication required.
// @Tags branding
// @Produce json
// @Success 200 {object} BrandingResponse
// @Router /api/branding [get]
func GetBrandingEndpoint(appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		b := appConfig.Branding
		return c.JSON(http.StatusOK, BrandingResponse{
			InstanceName:      b.InstanceName,
			InstanceTagline:   b.InstanceTagline,
			LogoURL:           brandingAssetURL("logo", b.LogoFile),
			LogoHorizontalURL: brandingAssetURL("logo_horizontal", b.LogoHorizontalFile),
			FaviconURL:        brandingAssetURL("favicon", b.FaviconFile),
		})
	}
}

// brandingDir resolves the directory that holds uploaded branding files,
// creating it on demand. Returns an error if DynamicConfigsDir is unset.
func brandingDir(appConfig *config.ApplicationConfig) (string, error) {
	if appConfig.DynamicConfigsDir == "" {
		return "", errors.New("DynamicConfigsDir is not set")
	}
	dir := filepath.Join(appConfig.DynamicConfigsDir, brandingDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

// removeExistingBrandingFiles deletes any prior asset files for the given
// kind so a new upload doesn't leave a stale companion (e.g. logo.png and
// logo.svg sitting side-by-side). Errors other than "not exist" are
// returned so callers know a stale file is left behind.
func removeExistingBrandingFiles(dir, kind string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	prefix := kind + "."
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), prefix) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// setBrandingFile updates appConfig.Branding for the given kind and writes
// the change to runtime_settings.json. A nil-string-pointer would erase the
// override; callers pass the new basename or "" to reset to default.
func setBrandingFile(appConfig *config.ApplicationConfig, kind, basename string) error {
	switch kind {
	case "logo":
		appConfig.Branding.LogoFile = basename
	case "logo_horizontal":
		appConfig.Branding.LogoHorizontalFile = basename
	case "favicon":
		appConfig.Branding.FaviconFile = basename
	default:
		return errors.New("unknown branding asset kind: " + kind)
	}

	settings, err := appConfig.ReadPersistedSettings()
	if err != nil {
		return err
	}
	switch kind {
	case "logo":
		settings.LogoFile = &basename
	case "logo_horizontal":
		settings.LogoHorizontalFile = &basename
	case "favicon":
		settings.FaviconFile = &basename
	}
	return appConfig.WritePersistedSettings(settings)
}

// UploadBrandingAssetEndpoint accepts a multipart "file" field and stores it
// as the override for the given asset kind. Admin-only.
//
// @Summary Upload a branding asset
// @Description Upload a custom logo, horizontal logo, or favicon. The file replaces any previous override for that kind.
// @Tags branding
// @Accept multipart/form-data
// @Produce json
// @Param kind path string true "Asset kind: logo, logo_horizontal, or favicon"
// @Param file formData file true "Image file (png, jpeg, svg, webp, ico — up to 5MiB)"
// @Success 200 {object} BrandingResponse
// @Failure 400 {object} map[string]string
// @Router /api/branding/asset/{kind} [post]
func UploadBrandingAssetEndpoint(appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		kind := c.Param("kind")
		if _, ok := brandingAssetKinds[kind]; !ok {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "invalid asset kind; expected one of logo, logo_horizontal, favicon",
			})
		}

		fileHeader, err := c.FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "file is required"})
		}
		if fileHeader.Size > maxBrandingAssetBytes {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{
				"error": "file too large; max 5 MiB",
			})
		}

		ct := fileHeader.Header.Get("Content-Type")
		ext, ok := brandingAssetMimeTypes[ct]
		if !ok {
			// Fall back to filename extension when the browser sent a generic
			// content-type (e.g. application/octet-stream for an .svg).
			ext = strings.ToLower(filepath.Ext(fileHeader.Filename))
			if !isAllowedBrandingExt(ext) {
				return c.JSON(http.StatusBadRequest, map[string]string{
					"error": "unsupported file type; expected png, jpeg, svg, webp, or ico",
				})
			}
		}

		src, err := fileHeader.Open()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to open uploaded file"})
		}
		defer func() { _ = src.Close() }()

		data, err := io.ReadAll(io.LimitReader(src, maxBrandingAssetBytes+1))
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read uploaded file"})
		}
		if len(data) > maxBrandingAssetBytes {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{
				"error": "file too large; max 5 MiB",
			})
		}

		dir, err := brandingDir(appConfig)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		if err := removeExistingBrandingFiles(dir, kind); err != nil {
			xlog.Warn("failed to clear previous branding asset", "kind", kind, "error", err)
		}

		basename := kind + ext
		dest := filepath.Join(dir, basename)
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to save asset: " + err.Error(),
			})
		}

		if err := setBrandingFile(appConfig, kind, basename); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "asset saved but failed to persist setting: " + err.Error(),
			})
		}

		return c.JSON(http.StatusOK, BrandingResponse{
			InstanceName:      appConfig.Branding.InstanceName,
			InstanceTagline:   appConfig.Branding.InstanceTagline,
			LogoURL:           brandingAssetURL("logo", appConfig.Branding.LogoFile),
			LogoHorizontalURL: brandingAssetURL("logo_horizontal", appConfig.Branding.LogoHorizontalFile),
			FaviconURL:        brandingAssetURL("favicon", appConfig.Branding.FaviconFile),
		})
	}
}

// DeleteBrandingAssetEndpoint removes the override for the given asset kind
// and falls back to the bundled default. Admin-only.
//
// @Summary Reset a branding asset to default
// @Description Remove a custom branding asset; the UI falls back to the bundled LocalAI default.
// @Tags branding
// @Produce json
// @Param kind path string true "Asset kind: logo, logo_horizontal, or favicon"
// @Success 200 {object} BrandingResponse
// @Router /api/branding/asset/{kind} [delete]
func DeleteBrandingAssetEndpoint(appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		kind := c.Param("kind")
		if _, ok := brandingAssetKinds[kind]; !ok {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "invalid asset kind; expected one of logo, logo_horizontal, favicon",
			})
		}

		dir, err := brandingDir(appConfig)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		if err := removeExistingBrandingFiles(dir, kind); err != nil {
			xlog.Warn("failed to remove branding asset file(s)", "kind", kind, "error", err)
		}
		if err := setBrandingFile(appConfig, kind, ""); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to clear branding setting: " + err.Error(),
			})
		}

		return c.JSON(http.StatusOK, BrandingResponse{
			InstanceName:      appConfig.Branding.InstanceName,
			InstanceTagline:   appConfig.Branding.InstanceTagline,
			LogoURL:           brandingAssetURL("logo", appConfig.Branding.LogoFile),
			LogoHorizontalURL: brandingAssetURL("logo_horizontal", appConfig.Branding.LogoHorizontalFile),
			FaviconURL:        brandingAssetURL("favicon", appConfig.Branding.FaviconFile),
		})
	}
}

// ServeBrandingAssetEndpoint streams the uploaded asset for the given kind,
// or 404 when no override is configured. Public — same accessibility as the
// bundled /static/* assets it replaces.
//
// @Summary Serve a custom branding asset
// @Description Serves the admin-uploaded logo, horizontal logo, or favicon. 404 when no override is set.
// @Tags branding
// @Produce image/*
// @Param kind path string true "Asset kind: logo, logo_horizontal, or favicon"
// @Success 200
// @Failure 404
// @Router /branding/asset/{kind} [get]
func ServeBrandingAssetEndpoint(appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		kind := c.Param("kind")
		if _, ok := brandingAssetKinds[kind]; !ok {
			return c.NoContent(http.StatusNotFound)
		}

		var file string
		switch kind {
		case "logo":
			file = appConfig.Branding.LogoFile
		case "logo_horizontal":
			file = appConfig.Branding.LogoHorizontalFile
		case "favicon":
			file = appConfig.Branding.FaviconFile
		}
		if file == "" || appConfig.DynamicConfigsDir == "" {
			return c.NoContent(http.StatusNotFound)
		}

		// Prevent path traversal — the basename must be exactly what we
		// previously stored. Anything containing a separator is rejected.
		if strings.ContainsAny(file, "/\\") || file == "." || file == ".." {
			return c.NoContent(http.StatusNotFound)
		}

		path := filepath.Join(appConfig.DynamicConfigsDir, brandingDirName, file)
		c.Response().Header().Set("Cache-Control", "public, max-age=300")
		return c.File(path)
	}
}

// isAllowedBrandingExt reports whether ext (lowercase, with dot) is one of
// the file extensions the upload handler will accept.
func isAllowedBrandingExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".svg", ".webp", ".ico":
		return true
	}
	return false
}
