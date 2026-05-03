package api

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kakra/ferry/ent"
	"github.com/kakra/ferry/ent/file"
	"github.com/kakra/ferry/ent/share"
	internalShare "github.com/kakra/ferry/internal/share"
	"github.com/labstack/echo/v4"
)

func (s *Server) handleFileDownload(c echo.Context) error {
	if s.breakGlass {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "File downloads are disabled in break-glass mode")
	}

	token := c.Param("token")
	fileIDStr := c.Param("id")
	fileID, err := uuid.Parse(fileIDStr)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid file ID")
	}

	if !s.isShareUnlocked(c, token) {
		return echo.NewHTTPError(http.StatusUnauthorized, "Access restricted")
	}

	query := s.db.File.Query().
		Where(file.IDEQ(fileID)).
		WithBlob().
		WithShare()

	if strings.HasPrefix(token, "id:") {
		idStr := strings.TrimPrefix(token, "id:")
		id, err := uuid.Parse(idStr)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid share ID in token")
		}
		query.Where(file.HasShareWith(
			share.IDEQ(id),
			share.ExpiresAtGT(time.Now()),
		))
	} else {
		hashed := internalShare.HashToken(token, s.config.Security.TokenSecret)
		query.Where(file.HasShareWith(
			share.TokenHashEQ(hashed),
			share.ExpiresAtGT(time.Now()),
		))
	}

	f, err := query.Only(c.Request().Context())
	if err != nil {
		if ent.IsNotFound(err) {
			return echo.NewHTTPError(http.StatusNotFound, "File not found or share expired")
		}
		return err
	}

	reader, err := s.storage.Open(f.Edges.Blob.ID)
	if err != nil {
		log.Printf("Error opening blob %s: %v", f.Edges.Blob.ID, err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to open file")
	}
	defer reader.Close()

	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", f.OriginalName))
	c.Response().Header().Set("Content-Type", "application/octet-stream")
	c.Response().Header().Set("Content-Length", fmt.Sprintf("%d", f.Edges.Blob.Size))

	_, err = io.Copy(c.Response().Writer, reader)
	return err
}

func (s *Server) handleFileDelete(c echo.Context) error {
	if s.breakGlass {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "File deletion is disabled in break-glass mode")
	}

	token := c.Param("token")
	fileIDStr := c.Param("id")
	fileID, err := uuid.Parse(fileIDStr)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid file ID")
	}

	if !s.isShareUnlocked(c, token) {
		return echo.NewHTTPError(http.StatusUnauthorized, "Access restricted")
	}

	query := s.db.File.Query().Where(file.IDEQ(fileID)).WithShare()
	if strings.HasPrefix(token, "id:") {
		idStr := strings.TrimPrefix(token, "id:")
		id, err := uuid.Parse(idStr)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid share ID in token")
		}
		query.Where(file.HasShareWith(
			share.IDEQ(id),
			share.ExpiresAtGT(time.Now()),
		))
	} else {
		hashed := internalShare.HashToken(token, s.config.Security.TokenSecret)
		query.Where(file.HasShareWith(
			share.TokenHashEQ(hashed),
			share.ExpiresAtGT(time.Now()),
		))
	}

	f, err := query.Only(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "File not found")
	}

	if !s.canManageShare(c, f.Edges.Share) {
		uploadSIDStr := s.getUploadSessionID(c, token)
		uploadSID, _ := uuid.Parse(uploadSIDStr)
		if f.UploadSessionID == nil || *f.UploadSessionID != uploadSID {
			return echo.NewHTTPError(http.StatusForbidden, "You can only delete files uploaded in your current session")
		}
	}

	err = s.db.File.DeleteOne(f).Exec(c.Request().Context())
	if err != nil {
		return err
	}

	return c.NoContent(http.StatusNoContent)
}
