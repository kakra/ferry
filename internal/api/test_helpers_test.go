package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/kakra/ferry/ent/user"
	"github.com/kakra/ferry/internal/auth"
	"github.com/kakra/ferry/internal/i18n"
	"github.com/stretchr/testify/require"
)

func createLocalAdminUser(t *testing.T, srv *Server, username, password string, disabled bool, canManageAllShares bool) {
	t.Helper()
	_ = i18n.Init()

	passwordHash, err := auth.HashPassword(password)
	require.NoError(t, err)

	_, err = srv.db.User.Create().
		SetUsername(username).
		SetDisplayName(username).
		SetPasswordHash(passwordHash).
		SetAuthSource(user.AuthSourceLocal).
		SetDisabled(disabled).
		SetCanManageAllShares(canManageAllShares).
		SetCanManageUsers(true).
		Save(context.Background())
	require.NoError(t, err)
}

func attachAdminSession(t *testing.T, srv *Server, req *http.Request, username string) {
	t.Helper()

	u, err := srv.db.User.Query().Where(user.UsernameEQ(username)).Only(context.Background())
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	store := sessions.NewCookieStore([]byte(srv.config.Security.SessionSecret))
	sess, _ := store.Get(req, sessionName)
	sess.Values[userSessionKey] = u.ID.String()
	require.NoError(t, sess.Save(req, rec))
	req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))
}

func mustTimeNowPlusHour() time.Time {
	return time.Now().Add(1 * time.Hour)
}
