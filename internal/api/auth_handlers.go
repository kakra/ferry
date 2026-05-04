package api

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/kakra/ferry/ent"
	"github.com/kakra/ferry/ent/user"
	"github.com/kakra/ferry/internal/auth"
	"github.com/labstack/echo-contrib/session"
	"github.com/labstack/echo/v4"
)

func (s *Server) authMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !s.isAuthenticated(c) {
			nextURL := c.Request().URL.RequestURI()
			return c.Redirect(http.StatusSeeOther, "/login?next="+url.QueryEscape(strings.TrimPrefix(nextURL, "/")))
		}
		return next(c)
	}
}

func (s *Server) adminOnlyMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !s.canManageUsers(c) {
			return echo.NewHTTPError(http.StatusForbidden, "Insufficient permissions")
		}
		return next(c)
	}
}

func (s *Server) apiAuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if s.breakGlass {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Break-glass mode does not expose the admin API")
		}

		if s.isAdmin(c) {
			return next(c)
		}

		// CLI maintenance commands use the static secret until a dedicated API-token model exists.
		authHeader := c.Request().Header.Get("Authorization")
		expected := "Bearer " + s.config.Auth.StaticPassword
		if authHeader != "" && authHeader == expected {
			return next(c)
		}

		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid credentials")
	}
}

func (s *Server) sessionOptions(maxAge int) *sessions.Options {
	return &sessions.Options{
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.config.Security.BehindReverseProxy,
		SameSite: http.SameSiteLaxMode,
	}
}

func (s *Server) currentUser(c echo.Context) (*ent.User, error) {
	sess, _ := session.Get(sessionName, c)
	userIDStr, ok := sess.Values[userSessionKey].(string)
	if !ok || userIDStr == "" {
		return nil, nil
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, err
	}
	return s.db.User.Get(c.Request().Context(), userID)
}

func (s *Server) isAdmin(c echo.Context) bool {
	if s.breakGlass {
		sess, _ := session.Get(sessionName, c)
		auth, ok := sess.Values[breakGlassSessionKey].(bool)
		return ok && auth
	}

	u, err := s.currentUser(c)
	if err != nil || u == nil || u.Disabled {
		return false
	}
	// Platform admins manage users and server internals.
	return u.CanManageUsers
}

func (s *Server) isAuthenticated(c echo.Context) bool {
	if s.breakGlass {
		return s.isAdmin(c)
	}

	u, err := s.currentUser(c)
	return err == nil && u != nil && !u.Disabled
}

func (s *Server) canManageUsers(c echo.Context) bool {
	if s.breakGlass {
		return s.isAdmin(c)
	}

	u, err := s.currentUser(c)
	if err != nil || u == nil {
		return false
	}
	return u.CanManageUsers
}

func (s *Server) canManageAllShares(c echo.Context) bool {
	if s.breakGlass {
		return s.isAdmin(c)
	}

	u, err := s.currentUser(c)
	if err != nil || u == nil || u.Disabled {
		return false
	}
	return u.CanManageAllShares
}

func (s *Server) canManageShare(c echo.Context, sh *ent.Share) bool {
	if sh == nil {
		return false
	}
	if s.canManageAllShares(c) {
		return true
	}

	u, err := s.currentUser(c)
	if err != nil || u == nil || u.Disabled {
		return false
	}
	return sh.OwnerID != nil && *sh.OwnerID == u.ID
}

func (s *Server) noUsersExist(ctx context.Context) bool {
	if s.db == nil {
		return true
	}
	count, _ := s.db.User.Query().Count(ctx)
	return count == 0
}

func (s *Server) handleLogin(c echo.Context) error {
	setNoStoreHeaders(c)
	return c.Render(http.StatusOK, "login.html", map[string]interface{}{
		"UI":             s.config.UI,
		"SetupNeeded":    !s.breakGlass && s.noUsersExist(c.Request().Context()),
		"BreakGlassMode": s.breakGlass,
		"Next":           c.QueryParam("next"),
	})
}

func (s *Server) renderLoginError(c echo.Context, username string, err string) error {
	setNoStoreHeaders(c)
	return c.Render(http.StatusUnauthorized, "login.html", map[string]interface{}{
		"UI":             s.config.UI,
		"Username":       username,
		"Error":          err,
		"SetupNeeded":    s.noUsersExist(c.Request().Context()),
		"BreakGlassMode": s.breakGlass,
		"Next":           c.QueryParam("next"),
	})
}

func sanitizeNextRedirect(next string) string {
	if next == "" {
		return "/"
	}

	// Normalize backslashes (Windows-style) to forward slashes
	next = strings.ReplaceAll(next, "\\", "/")
	target, err := url.Parse(next)
	if err != nil {
		return "/"
	}

	// Ensure the redirect is to a local path (starts with /) and not an absolute URL (has hostname)
	// Also prevent protocol-relative URLs (starting with //)
	if target.Hostname() != "" || target.IsAbs() || !strings.HasPrefix(target.Path, "/") || strings.HasPrefix(target.Path, "//") {
		return "/"
	}

	return target.String()
}

func (s *Server) handleLoginPost(c echo.Context) error {
	username := strings.TrimSpace(c.FormValue("username"))
	password := c.FormValue("password")
	next := sanitizeNextRedirect(c.QueryParam("next"))

	if s.breakGlass {
		if s.config.Auth.BootstrapPassword != "" && password == s.config.Auth.BootstrapPassword {
			sess, _ := session.Get(sessionName, c)
			sess.Values[userSessionKey] = ""
			sess.Values[breakGlassSessionKey] = true
			sess.Options = s.sessionOptions(3600)
			sess.Save(c.Request(), c.Response())
			return c.Redirect(http.StatusSeeOther, next)
		}

		log.Printf("Security: Failed break-glass login attempt from %s", c.RealIP())
		return s.renderLoginError(c, username, "Invalid credentials")
	}

	if s.db != nil && username != "" {
		u, err := s.db.User.Query().
			Where(user.UsernameEQ(username), user.AuthSourceEQ(user.AuthSourceLocal), user.Disabled(false)).
			Only(c.Request().Context())
		if err == nil && u.PasswordHash != nil {
			match, verifyErr := auth.VerifyPassword(password, *u.PasswordHash)
			if verifyErr == nil && match {
				sess, _ := session.Get(sessionName, c)
				sess.Values[userSessionKey] = u.ID.String()
				delete(sess.Values, breakGlassSessionKey)
				sess.Options = s.sessionOptions(86400 * 7) // 1 week
				sess.Save(c.Request(), c.Response())
				return c.Redirect(http.StatusSeeOther, next)
			}
		}
	}

	log.Printf("Security: Failed login attempt for user '%s' from %s", username, c.RealIP())
	return s.renderLoginError(c, username, "Invalid credentials")
}

func (s *Server) handleLogout(c echo.Context) error {
	sess, _ := session.Get(sessionName, c)
	sess.Options.MaxAge = -1
	sess.Save(c.Request(), c.Response())
	return c.Redirect(http.StatusSeeOther, "/login")
}

func (s *Server) handleSetup(c echo.Context) error {
	if s.breakGlass {
		return echo.NewHTTPError(http.StatusNotFound, "Setup unavailable in break-glass mode")
	}
	if !s.noUsersExist(c.Request().Context()) {
		return echo.NewHTTPError(http.StatusNotFound, "Setup no longer available")
	}
	return c.Render(http.StatusOK, "setup.html", map[string]interface{}{
		"UI": s.config.UI,
	})
}

func (s *Server) handleSetupPost(c echo.Context) error {
	if s.breakGlass {
		return echo.NewHTTPError(http.StatusNotFound, "Setup unavailable in break-glass mode")
	}
	if !s.noUsersExist(c.Request().Context()) {
		return echo.NewHTTPError(http.StatusNotFound, "Setup no longer available")
	}

	bootstrapPassword := c.FormValue("bootstrap_password")
	if bootstrapPassword != s.config.Auth.BootstrapPassword {
		return c.Render(http.StatusUnauthorized, "setup.html", map[string]interface{}{
			"UI":    s.config.UI,
			"Error": "Invalid bootstrap password",
		})
	}

	username := strings.TrimSpace(c.FormValue("username"))
	displayName := strings.TrimSpace(c.FormValue("display_name"))
	password := c.FormValue("password")

	if username == "" || displayName == "" || password == "" {
		return c.Render(http.StatusBadRequest, "setup.html", map[string]interface{}{
			"UI":    s.config.UI,
			"Error": "All fields are required",
		})
	}

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	newUser, err := s.db.User.Create().
		SetUsername(username).
		SetDisplayName(displayName).
		SetPasswordHash(passwordHash).
		SetAuthSource(user.AuthSourceLocal).
		SetCanManageAllShares(true).
		SetCanManageUsers(true).
		Save(ctx)

	if err != nil {
		return c.Render(http.StatusInternalServerError, "setup.html", map[string]interface{}{
			"UI":    s.config.UI,
			"Error": "Failed to create admin user",
		})
	}

	sess, _ := session.Get(sessionName, c)
	sess.Values[userSessionKey] = newUser.ID.String()
	delete(sess.Values, breakGlassSessionKey)
	sess.Options = s.sessionOptions(86400 * 7) // 1 week
	sess.Save(c.Request(), c.Response())

	return c.Redirect(http.StatusSeeOther, "/")
}
