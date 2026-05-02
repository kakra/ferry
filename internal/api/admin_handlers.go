package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/invopop/ctxi18n/i18n"
	"github.com/kakra/ferry/ent"
	"github.com/kakra/ferry/ent/file"
	"github.com/kakra/ferry/ent/share"
	"github.com/kakra/ferry/ent/user"
	"github.com/kakra/ferry/internal/auth"
	internalShare "github.com/kakra/ferry/internal/share"
	"github.com/labstack/echo/v4"
)

func (s *Server) handleCreateShare(c echo.Context) error {
	if s.breakGlass {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "Share creation is disabled in break-glass mode")
	}
	if !s.canManageShares(c) {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	token, err := internalShare.GenerateToken()
	if err != nil {
		return err
	}

	duration, err := parseShareExpiration(c.FormValue("expiration"), s.config)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid share expiration")
	}

	shareType := share.TypeDownload
	if c.FormValue("type") == "upload" {
		shareType = share.TypeUpload
	}

	title := c.FormValue("title")
	if title == "" {
		format := "02.01.2006 15:04"
		loc := i18n.GetLocale(c.Request().Context())
		if loc != nil {
			format = string(loc.T("common.date_time_format"))
		}
		title = "Share " + time.Now().Format(format)
	}
	note := c.FormValue("note")

	u, _ := s.currentUser(c)

	password, err := auth.GenerateHumanFriendlyPassword()
	if err != nil {
		return err
	}
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	publicTokenEncrypted, err := internalShare.EncryptToken(token, s.config.Security.TokenSecret)
	if err != nil {
		return err
	}

	sc := s.db.Share.Create().
		SetPublicTokenEncrypted(publicTokenEncrypted).
		SetTokenHash(internalShare.HashToken(token, s.config.Security.TokenSecret)).
		SetType(shareType).
		SetExpiresAt(time.Now().Add(duration)).
		SetPasswordHash(passwordHash).
		SetTitle(title)

	if u != nil {
		sc.SetOwner(u)
	}

	if note != "" {
		sc.SetNote(note)
	}

	newShare, err := sc.Save(c.Request().Context())
	if err != nil {
		return err
	}

	return c.Render(http.StatusOK, "share_created.html", map[string]interface{}{
		"UI":                s.config.UI,
		"Share":             newShare,
		"PublicToken":       token,
		"SharePassword":     password,
		"PublicURL":         s.config.Server.PublicURL,
		"ResultTitleKey":    "share_created.title",
		"ResultSubtitleKey": "share_created.subtitle",
	})
}

func (s *Server) handleCreateUser(c echo.Context) error {
	if !s.canManageUsers(c) {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	username := strings.TrimSpace(c.FormValue("username"))
	displayName := strings.TrimSpace(c.FormValue("display_name"))
	password := c.FormValue("password")

	if username == "" || displayName == "" || password == "" || strings.Contains(username, "@") {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid local user data")
	}

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	_, err = s.db.User.Create().
		SetUsername(username).
		SetAuthSource(user.AuthSourceLocal).
		SetDisplayName(displayName).
		SetPasswordHash(passwordHash).
		SetCanManageAllShares(false).
		SetCanManageUsers(false).
		Save(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Failed to create user")
	}

	// HTMX: Reload dashboard
	return s.handleIndex(c)
}

func (s *Server) handleToggleUserDisabled(c echo.Context) error {
	if !s.canManageUsers(c) {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	currentUser, err := s.currentUser(c)
	if err != nil || currentUser == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "No active admin session")
	}

	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID")
	}
	if targetID == currentUser.ID {
		return echo.NewHTTPError(http.StatusBadRequest, "You cannot disable your own account")
	}

	targetUser, err := s.db.User.Get(c.Request().Context(), targetID)
	if err != nil {
		if ent.IsNotFound(err) {
			return echo.NewHTTPError(http.StatusNotFound, "User not found")
		}
		return err
	}

	if _, err := targetUser.Update().
		SetDisabled(!targetUser.Disabled).
		Save(c.Request().Context()); err != nil {
		return err
	}

	// HTMX: Reload dashboard
	return s.handleIndex(c)
}

func (s *Server) handleResetUserPassword(c echo.Context) error {
	if !s.canManageUsers(c) {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID")
	}

	newPassword := c.FormValue("password")
	if newPassword == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Password is required")
	}

	targetUser, err := s.db.User.Get(c.Request().Context(), targetID)
	if err != nil {
		if ent.IsNotFound(err) {
			return echo.NewHTTPError(http.StatusNotFound, "User not found")
		}
		return err
	}
	if targetUser.AuthSource != user.AuthSourceLocal {
		return echo.NewHTTPError(http.StatusBadRequest, "Only local user passwords can be reset")
	}

	passwordHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}

	if _, err := targetUser.Update().SetPasswordHash(passwordHash).Save(c.Request().Context()); err != nil {
		return err
	}

	// HTMX: Reload dashboard
	return s.handleIndex(c)
}

func (s *Server) handleDeleteUser(c echo.Context) error {
	if !s.canManageUsers(c) {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	currentUser, err := s.currentUser(c)
	if err != nil {
		return err
	}

	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID")
	}

	if currentUser != nil && targetID == currentUser.ID {
		return echo.NewHTTPError(http.StatusBadRequest, "You cannot delete your own account")
	}

	tx, err := s.db.Tx(c.Request().Context())
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	targetUser, err := tx.User.Get(c.Request().Context(), targetID)
	if err != nil {
		if ent.IsNotFound(err) {
			return echo.NewHTTPError(http.StatusNotFound, "User not found")
		}
		return err
	}

	if targetUser.AuthSource != user.AuthSourceLocal {
		return echo.NewHTTPError(http.StatusBadRequest, "Only local users can be deleted")
	}
	if !targetUser.Disabled {
		return echo.NewHTTPError(http.StatusBadRequest, "Only disabled users can be deleted")
	}

	ownedShares, err := tx.Share.Query().
		Where(share.OwnerIDEQ(targetID)).
		Select(share.FieldID).
		All(c.Request().Context())
	if err != nil {
		return err
	}
	for _, sh := range ownedShares {
		if err := deleteShareWithDependencies(c.Request().Context(), tx, sh.ID); err != nil {
			return err
		}
	}

	if err := tx.User.DeleteOneID(targetID).Exec(c.Request().Context()); err != nil {
		if ent.IsNotFound(err) {
			return echo.NewHTTPError(http.StatusNotFound, "User not found")
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return s.handleIndex(c)
}

func (s *Server) handleUpdateUserPermission(c echo.Context) error {
	if !s.canManageUsers(c) {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	currentUser, err := s.currentUser(c)
	if err != nil || currentUser == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "No active admin session")
	}

	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid user ID")
	}

	if targetID == currentUser.ID {
		return echo.NewHTTPError(http.StatusBadRequest, "You cannot modify your own permissions")
	}

	perm := c.Param("perm")
	// HTMX submits checkbox values only when checked.
	val := c.FormValue(perm) != ""

	u, err := s.db.User.Get(c.Request().Context(), targetID)
	if err != nil {
		return err
	}

	updater := u.Update()
	switch perm {
	case "can-manage-all-shares":
		updater.SetCanManageAllShares(val)
	case "can-manage-users":
		updater.SetCanManageUsers(val)
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid permission name")
	}

	if _, err := updater.Save(c.Request().Context()); err != nil {
		return err
	}

	return s.handleIndex(c)
}

func (s *Server) handleDeleteShare(c echo.Context) error {
	if !s.canManageShares(c) {
		return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
	}

	shareID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid share ID")
	}

	tx, err := s.db.Tx(c.Request().Context())
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.Share.Get(c.Request().Context(), shareID); err != nil {
		if ent.IsNotFound(err) {
			return echo.NewHTTPError(http.StatusNotFound, "Share not found")
		}
		return err
	}

	if err := deleteShareWithDependencies(c.Request().Context(), tx, shareID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return s.handleIndex(c)
}

func deleteShareWithDependencies(ctx context.Context, tx *ent.Tx, shareID uuid.UUID) error {
	if _, err := tx.File.Delete().Where(file.HasShareWith(share.IDEQ(shareID))).Exec(ctx); err != nil {
		return err
	}
	if err := tx.Share.DeleteOneID(shareID).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return echo.NewHTTPError(http.StatusNotFound, "Share not found")
		}
		return err
	}
	return nil
}
