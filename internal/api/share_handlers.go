package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kakra/ferry/ent"
	"github.com/kakra/ferry/ent/file"
	"github.com/kakra/ferry/ent/share"
	"github.com/kakra/ferry/internal/auth"
	internalShare "github.com/kakra/ferry/internal/share"
	"github.com/labstack/echo-contrib/session"
	"github.com/labstack/echo/v4"
)

func (s *Server) handlePublicShare(c echo.Context) error {
	if s.breakGlass {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Public share access is disabled in break-glass mode")
	}

	token := c.Param("token")

	sh, err := s.db.Share.Query().
		Where(share.TokenHashEQ(internalShare.HashToken(token, s.config.Security.TokenSecret))).
		Where(share.ExpiresAtGT(time.Now())).
		WithFiles(func(q *ent.FileQuery) {
			q.WithBlob()
			q.Order(ent.Asc(file.FieldOriginalName), ent.Asc(file.FieldCreatedAt))
		}).
		Only(c.Request().Context())

	if err != nil {
		if ent.IsNotFound(err) {
			return echo.NewHTTPError(http.StatusNotFound, "Share not found or expired")
		}
		return err
	}

	canManageShare := s.canManageShare(c, sh)
	if !canManageShare && !s.isShareUnlocked(c, token) {
		return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/s/%s/unlock", token))
	}

	return s.renderPublicShare(c, sh, token, canManageShare)
}

func (s *Server) getActiveShareByToken(ctx context.Context, token string) (*ent.Share, error) {
	hashed := internalShare.HashToken(token, s.config.Security.TokenSecret)
	return s.db.Share.Query().
		Where(share.TokenHashEQ(hashed), share.ExpiresAtGT(time.Now())).
		Only(ctx)
}

func (s *Server) isShareUnlocked(c echo.Context, token string) bool {
	if s.breakGlass {
		return false
	}

	if strings.HasPrefix(token, "id:") {
		id, err := uuid.Parse(strings.TrimPrefix(token, "id:"))
		if err != nil {
			return false
		}
		sh, err := s.db.Share.Query().
			Where(share.IDEQ(id)).
			Where(share.ExpiresAtGT(time.Now())).
			Only(c.Request().Context())
		if err != nil {
			return false
		}
		return s.canManageShare(c, sh)
	}

	sh, err := s.getActiveShareByToken(c.Request().Context(), token)
	if err != nil {
		return false
	}

	if s.canManageShare(c, sh) {
		return true
	}

	sess, _ := session.Get(sessionName, c)
	if sess == nil {
		return false
	}

	unlocked, ok := sess.Values[guestSessionPrefix+token].(bool)
	if !ok || !unlocked {
		return false
	}

	version, ok := sess.Values[guestUnlockVersionKey+token].(int)
	if !ok {
		return false
	}

	// Password rotations increment UnlockVersion, invalidating older guest cookies.
	return version == sh.UnlockVersion
}

func (s *Server) getUploadSessionID(c echo.Context, token string) string {
	sess, _ := session.Get(sessionName, c)
	key := uploadSessionIDPrefix + token
	sid, ok := sess.Values[key].(string)
	if !ok || sid == "" {
		sid = uuid.New().String()
		sess.Values[key] = sid
		sess.Save(c.Request(), c.Response())
	}
	return sid
}

func uploadSuccessIDsFromQuery(c echo.Context, sh *ent.Share) ([]string, map[string]bool) {
	raw := c.QueryParam("success_ids")
	if raw == "" {
		return nil, map[string]bool{}
	}

	// Create a map of valid file IDs for this share for validation
	validIDs := make(map[string]bool)
	if sh != nil {
		for _, f := range sh.Edges.Files {
			validIDs[f.ID.String()] = true
		}
	}

	ids := make([]string, 0, maxUploadSuccessIDs)
	seen := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" || seen[id] || !validIDs[id] {
			continue
		}
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		if len(ids) == maxUploadSuccessIDs {
			break
		}
	}
	return ids, seen
}

func (s *Server) renderPublicShare(c echo.Context, sh *ent.Share, token string, canManageShare bool) error {
	uploadSID := s.getUploadSessionID(c, token)
	publicShareURL := ""
	if canManageShare && sh.PublicTokenEncrypted != nil {
		publicToken, err := internalShare.DecryptToken(*sh.PublicTokenEncrypted, s.config.Security.TokenSecret)
		if err == nil && publicToken != "" {
			publicShareURL = fmt.Sprintf("%s/s/%s", s.config.Server.PublicURL, publicToken)
		}
	}
	return c.Render(http.StatusOK, "public_share.html", map[string]interface{}{
		"UI":                  s.config.UI,
		"Share":               sh,
		"ShareToken":          token,
		"Files":               sh.Edges.Files,
		"UploadSessionID":     uploadSID,
		"UploadSuccessIDs":    map[string]bool{},
		"UploadSuccessIDList": "",
		"MaxUploadSuccessIDs": maxUploadSuccessIDs,
		"CanManageShare":      canManageShare,
		"PublicShareURL":      publicShareURL,
	})
}

func (s *Server) handleGetFilesFragment(c echo.Context) error {
	token := c.Param("token")
	var sh *ent.Share
	var err error

	if strings.HasPrefix(token, "id:") {
		id, err := uuid.Parse(strings.TrimPrefix(token, "id:"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid share ID")
		}
		sh, err = s.db.Share.Query().Where(share.IDEQ(id)).WithFiles(func(q *ent.FileQuery) {
			q.WithBlob()
			q.Order(ent.Asc(file.FieldOriginalName), ent.Asc(file.FieldCreatedAt))
		}).Only(c.Request().Context())
		if err == nil && !s.canManageShare(c, sh) {
			return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
		}
	} else {
		sh, err = s.db.Share.Query().
			Where(share.TokenHashEQ(internalShare.HashToken(token, s.config.Security.TokenSecret))).
			Where(share.ExpiresAtGT(time.Now())).
			WithFiles(func(q *ent.FileQuery) {
				q.WithBlob()
				q.Order(ent.Asc(file.FieldOriginalName), ent.Asc(file.FieldCreatedAt))
			}).
			Only(c.Request().Context())
		if err == nil && !s.isShareUnlocked(c, token) {
			return echo.NewHTTPError(http.StatusUnauthorized, "Share not unlocked")
		}
	}

	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Share not found")
	}

	uploadSID := s.getUploadSessionID(c, token)
	uploadSuccessIDList, uploadSuccessIDs := uploadSuccessIDsFromQuery(c, sh)
	canManageShare := s.canManageShare(c, sh)

	data := map[string]interface{}{
		"Share":               sh,
		"Files":               sh.Edges.Files,
		"ShareToken":          token,
		"UploadSessionID":     uploadSID,
		"UploadSuccessIDs":    uploadSuccessIDs,
		"UploadSuccessIDList": strings.Join(uploadSuccessIDList, ","),
		"MaxUploadSuccessIDs": maxUploadSuccessIDs,
		"CanManageShare":      canManageShare,
	}

	c.Response().Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.echo.Renderer.(*TemplateRenderer).RenderFragment(c.Response().Writer, "_file_list_area.html", data, c); err != nil {
		log.Printf("Error rendering files fragment: %v", err)
		return err
	}
	return nil
}

func (s *Server) handleAdminSharePrepare(c echo.Context) error {
	if s.breakGlass {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Share preparation is disabled in break-glass mode")
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid share ID")
	}

	sh, err := s.db.Share.Query().
		Where(share.IDEQ(id)).
		Where(share.ExpiresAtGT(time.Now())).
		WithFiles(func(q *ent.FileQuery) {
			q.WithBlob()
			q.Order(ent.Asc(file.FieldOriginalName), ent.Asc(file.FieldCreatedAt))
		}).
		Only(c.Request().Context())

	if err != nil {
		if ent.IsNotFound(err) {
			return echo.NewHTTPError(http.StatusNotFound, "Share not found or expired")
		}
		return err
	}

	if !s.canManageShare(c, sh) {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	// Prep mode reuses the share renderer without exposing the public token to the URL.
	prepToken := "id:" + sh.ID.String()
	return s.renderPublicShare(c, sh, prepToken, true)
}

func (s *Server) handleUpdateShareDetails(c echo.Context) error {
	if s.breakGlass {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Share editing is disabled in break-glass mode")
	}

	shareID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid share ID")
	}

	title := strings.TrimSpace(c.FormValue("title"))
	if title == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Title is required")
	}

	note := strings.TrimSpace(c.FormValue("note"))

	sh, err := s.db.Share.Get(c.Request().Context(), shareID)
	if err != nil {
		if ent.IsNotFound(err) {
			return echo.NewHTTPError(http.StatusNotFound, "Share not found")
		}
		return err
	}
	if !s.canManageShare(c, sh) {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	updater := sh.Update().
		SetTitle(title)
	if note == "" {
		updater.ClearNote()
	} else {
		updater.SetNote(note)
	}

	updatedShare, err := updater.Save(c.Request().Context())
	if err != nil {
		return err
	}

	updatedShare, err = s.db.Share.Query().
		Where(share.IDEQ(updatedShare.ID)).
		WithFiles(func(q *ent.FileQuery) {
			q.WithBlob()
			q.Order(ent.Asc(file.FieldOriginalName), ent.Asc(file.FieldCreatedAt))
		}).
		Only(c.Request().Context())
	if err != nil {
		return err
	}

	prepToken := "id:" + updatedShare.ID.String()
	setNoStoreHeaders(c)
	return s.renderPublicShare(c, updatedShare, prepToken, true)
}

func (s *Server) handlePublicShareUnlock(c echo.Context) error {
	if s.breakGlass {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Public share access is disabled in break-glass mode")
	}

	token := c.Param("token")
	if s.isShareUnlocked(c, token) {
		return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/s/%s", token))
	}

	sh, err := s.getActiveShareByToken(c.Request().Context(), token)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Share not found or expired")
	}

	return c.Render(http.StatusOK, "unlock.html", map[string]interface{}{
		"UI":    s.config.UI,
		"Share": sh,
	})
}

func (s *Server) handlePublicShareUnlockPost(c echo.Context) error {
	if s.breakGlass {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Public share access is disabled in break-glass mode")
	}

	token := c.Param("token")
	password := c.FormValue("password")

	sh, err := s.getActiveShareByToken(c.Request().Context(), token)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Share not found or expired")
	}

	if sh.PasswordHash == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Share has no password hash")
	}

	match, err := auth.VerifyPassword(password, *sh.PasswordHash)
	if err != nil {
		return err
	}

	if match {
		sess, _ := session.Get(sessionName, c)
		sess.Values[guestSessionPrefix+token] = true
		sess.Values[guestUnlockVersionKey+token] = sh.UnlockVersion
		sess.Options = s.sessionOptions(3600 * 24) // 24 hours
		sess.Save(c.Request(), c.Response())
		return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/s/%s", token))
	}

	log.Printf("Security: Failed unlock attempt for share %s from %s", token, c.RealIP())
	return c.Render(http.StatusUnauthorized, "unlock.html", map[string]interface{}{
		"UI":    s.config.UI,
		"Share": sh,
		"Error": "Invalid password",
	})
}

func (s *Server) handleRotateSharePassword(c echo.Context) error {
	if s.breakGlass {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Share password rotation is disabled in break-glass mode")
	}

	shareID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid share ID")
	}

	newPassword, err := auth.GenerateHumanFriendlyPassword()
	if err != nil {
		return err
	}
	passwordHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}

	sh, err := s.db.Share.Get(c.Request().Context(), shareID)
	if err != nil {
		if ent.IsNotFound(err) {
			return echo.NewHTTPError(http.StatusNotFound, "Share not found")
		}
		return err
	}

	if !s.canManageShare(c, sh) {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	updatedShare, err := sh.Update().
		SetPasswordHash(passwordHash).
		SetUnlockVersion(sh.UnlockVersion + 1).
		Save(c.Request().Context())
	if err != nil {
		return err
	}

	publicToken := ""
	if updatedShare.PublicTokenEncrypted != nil {
		publicToken, err = internalShare.DecryptToken(*updatedShare.PublicTokenEncrypted, s.config.Security.TokenSecret)
		if err != nil {
			return err
		}
	}

	setNoStoreHeaders(c)
	return c.Render(http.StatusOK, "share_created.html", map[string]interface{}{
		"UI":                s.config.UI,
		"Share":             updatedShare,
		"PublicToken":       publicToken,
		"SharePassword":     newPassword,
		"PublicURL":         s.config.Server.PublicURL,
		"ResultTitleKey":    "share_created.title",
		"ResultSubtitleKey": "share_created.subtitle",
	})
}
