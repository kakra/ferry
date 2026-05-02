package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/invopop/ctxi18n/i18n"
	internalShare "github.com/kakra/ferry/internal/share"
	"github.com/kakra/ferry/internal/upload"
	"github.com/labstack/echo/v4"
)

func (s *Server) handleGetUploadStatus(c echo.Context) error {
	uploadID := c.Param("uploadId")
	if uploadID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Missing uploadId")
	}
	token := c.QueryParam("token")
	if token == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Missing token")
	}

	if s.upload == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Upload manager not initialized")
	}
	info := s.upload.GetStatus(uploadID)

	// Security: If we found an entry, verify the current visitor has access to that share
	if info.Found {
		var providedHash string
		if strings.HasPrefix(token, "id:") {
			// Admin/ID-based path: look up the share to get its hash
			idStr := strings.TrimPrefix(token, "id:")
			id, err := uuid.Parse(idStr)
			if err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "Invalid share ID format")
			}
			sh, err := s.db.Share.Get(c.Request().Context(), id)
			if err != nil {
				return echo.NewHTTPError(http.StatusForbidden, "Unauthorized access to upload status (Share not found)")
			}
			providedHash = sh.TokenHash
		} else {
			// Guest path: hash the token
			providedHash = internalShare.HashToken(token, s.config.Security.TokenSecret)
		}

		if info.ShareTokenHash != providedHash {
			return echo.NewHTTPError(http.StatusForbidden, "Unauthorized access to upload status")
		}
	}

	if c.Request().Header.Get("HX-Request") == "true" {
		if info.Status == upload.StatusComplete {
			if info.FileID != nil {
				trigger, err := json.Marshal(map[string]map[string]string{
					"upload-complete": {
						"file_id":   info.FileID.String(),
						"upload_id": uploadID,
					},
				})
				if err != nil {
					return err
				}
				c.Response().Header().Set("HX-Trigger", string(trigger))
			}
			return c.HTML(http.StatusOK, `<div></div>`)
		}

		if info.Status == upload.StatusProcessing {
			return c.NoContent(http.StatusNoContent)
		}
		if info.Status == upload.StatusError {
			loc := i18n.GetLocale(c.Request().Context())
			msg := "Processing failed."
			if loc != nil {
				msg = loc.T("guest.processing_error")
			}
			return c.HTML(http.StatusOK, fmt.Sprintf(`<div class="upload-status is-error">%s</div>`, html.EscapeString(msg)))
		}
	}

	return c.JSON(http.StatusOK, map[string]string{"status": string(info.Status)})
}

func (s *Server) handleTUSUpload(c echo.Context) error {
	if s.breakGlass {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Uploads are disabled in break-glass mode")
	}

	// tusd validates chunks after creation; Ferry validates share access before creating an upload.
	if c.Request().Method == http.MethodPost {
		metadata := c.Request().Header.Get("Upload-Metadata")
		pairs := strings.Split(metadata, ",")
		var token string
		for _, pair := range pairs {
			parts := strings.Split(strings.TrimSpace(pair), " ")
			if len(parts) == 2 && parts[0] == "share_token" {
				decoded, err := base64.StdEncoding.DecodeString(parts[1])
				if err == nil {
					token = string(decoded)
				}
				break
			}
		}

		if token == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "Missing share_token in metadata")
		}

		if !s.isShareUnlocked(c, token) {
			return echo.NewHTTPError(http.StatusUnauthorized, "Share not unlocked")
		}

		ctx := c.Request().Context()
		uploadSID := s.getUploadSessionID(c, token)
		ctx = context.WithValue(ctx, upload.SessionIDContextKey, uploadSID)

		isAdmin := s.isAdmin(c)
		if isAdmin {
			ctx = context.WithValue(ctx, upload.IsAdminContextKey, true)
		}

		log.Printf("TUS: Upload attempt for token %s (IsAdmin: %v, SessionID: %s)", token, isAdmin, uploadSID)
		c.SetRequest(c.Request().WithContext(ctx))
	}

	s.upload.ServeHTTP(c.Response().Writer, c.Request())
	return nil
}
