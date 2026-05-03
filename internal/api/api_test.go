package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/kakra/ferry/ent"
	"github.com/kakra/ferry/ent/enttest"
	"github.com/kakra/ferry/ent/share"
	"github.com/kakra/ferry/ent/user"
	"github.com/kakra/ferry/internal/auth"
	"github.com/kakra/ferry/internal/cleanup"
	"github.com/kakra/ferry/internal/config"
	"github.com/kakra/ferry/internal/i18n"
	internalShare "github.com/kakra/ferry/internal/share"
	"github.com/kakra/ferry/internal/storage"
	"github.com/kakra/ferry/internal/upload"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupBaseServer(t *testing.T) (*Server, *config.Config) {
	return setupCustomServer(t, config.DefaultConfig())
}

func setupCustomServer(t *testing.T, cfg *config.Config) (*Server, *config.Config) {
	_ = i18n.Init()
	cfg.Storage.Path = t.TempDir()
	st, _ := storage.NewFileStorage(cfg.Storage.Path)
	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", uuid.New().String())
	client := enttest.Open(t, "sqlite3", dbName)
	t.Cleanup(func() { _ = client.Close() })
	um, _ := upload.NewManager(cfg, client, st)
	cw := cleanup.NewWorker(cfg, client, st)
	srv := NewServer(cfg, client, cw, um, st)
	srv.SkipCSRF = true
	return srv, cfg
}

func setupBreakGlassServer(t *testing.T) (*Server, *config.Config) {
	_ = i18n.Init()
	cfg := config.DefaultConfig()
	cfg.Auth.BootstrapPassword = "bootstrap-secret"
	st, _ := storage.NewFileStorage(t.TempDir())
	dbName := fmt.Sprintf("file:bg-%s?mode=memory&cache=shared&_fk=1", uuid.New().String())
	client := enttest.Open(t, "sqlite3", dbName)
	t.Cleanup(func() { _ = client.Close() })
	srv := NewBreakGlassServer(cfg, client, st)
	srv.SkipCSRF = true
	return srv, cfg
}

func attachBreakGlassSession(t *testing.T, srv *Server, req *http.Request) {
	t.Helper()

	rec := httptest.NewRecorder()
	store := sessions.NewCookieStore([]byte(srv.config.Security.SessionSecret))
	sess, _ := store.Get(req, sessionName)
	sess.Values[breakGlassSessionKey] = true
	_ = sess.Save(req, rec)
	req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))
}

func TestHandleSetup_CreatesFirstAdmin(t *testing.T) {
	srv, cfg := setupBaseServer(t)
	cfg.Auth.BootstrapPassword = "bootstrap-secret"

	f := make(url.Values)
	f.Set("bootstrap_password", "bootstrap-secret")
	f.Set("username", "admin")
	f.Set("display_name", "Boss")
	f.Set("password", "secret123")

	req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(f.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/", rec.Header().Get("Location"))
	assert.Contains(t, rec.Header().Get("Set-Cookie"), sessionName)

	// Verify User
	u, err := srv.db.User.Query().Where(user.UsernameEQ("admin")).Only(context.Background())
	assert.NoError(t, err)
	assert.True(t, u.CanManageAllShares)
}

func TestHandleSetup_UnavailableAfterBootstrap(t *testing.T) {
	srv, _ := setupBaseServer(t)
	createLocalAdminUser(t, srv, "admin", "secret", false, true)

	req := httptest.NewRequest(http.MethodGet, "/setup", nil)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleLogin_StaticPasswordDoesNotBootstrapWebSession(t *testing.T) {
	srv, cfg := setupBaseServer(t)
	cfg.Auth.StaticPassword = "static-secret"

	form := url.Values{}
	form.Set("password", "static-secret")

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NotContains(t, rec.Header().Get("Set-Cookie"), sessionName)
}

func TestHandleIndex_Protected(t *testing.T) {
	srv, _ := setupBaseServer(t)
	// Create a user so bootstrap is disabled
	createLocalAdminUser(t, srv, "admin", "secret", false, true)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "/login?next=")
}

func TestHandleAdminCleanup_BearerAuth(t *testing.T) {
	srv, cfg := setupBaseServer(t)
	cfg.Auth.StaticPassword = "api-secret"

	t.Run("No Auth -> 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/cleanup", nil)
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("Valid Bearer -> 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/cleanup", nil)
		req.Header.Set("Authorization", "Bearer api-secret")
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestHandleHealth_Public(t *testing.T) {
	srv, _ := setupBaseServer(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
}

func TestNoAdminUser_HidesAdminLinksAndBlocksStatus(t *testing.T) {
	srv, _ := setupBaseServer(t)
	createLocalAdminUser(t, srv, "noadmin", "secret123", false, false)

	ctx := context.Background()
	u, err := srv.db.User.Query().Where(user.UsernameEQ("noadmin")).Only(ctx)
	require.NoError(t, err)
	_, err = u.Update().
		SetCanManageUsers(false).
		SetCanManageAllShares(false).
		Save(ctx)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	attachUserSession(t, srv, req, u.ID)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), `href="/status"`)
	assert.NotContains(t, rec.Body.String(), `href="/#users"`)

	statusReq := httptest.NewRequest(http.MethodGet, "/status", nil)
	attachUserSession(t, srv, statusReq, u.ID)
	statusRec := httptest.NewRecorder()
	srv.echo.ServeHTTP(statusRec, statusReq)

	assert.Equal(t, http.StatusForbidden, statusRec.Code)
}

func TestAuthRateLimiter(t *testing.T) {
	t.Run("login", func(t *testing.T) {
		srv, _ := setupBaseServer(t)
		createLocalAdminUser(t, srv, "limited-admin", "secret123", false, true)

		form := url.Values{}
		form.Set("username", "limited-admin")
		form.Set("password", "wrong")

		var lastCode int
		for i := 0; i < 6; i++ {
			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			srv.echo.ServeHTTP(rec, req)
			lastCode = rec.Code
		}

		assert.Equal(t, http.StatusTooManyRequests, lastCode)
	})

	t.Run("share unlock", func(t *testing.T) {
		srv, cfg := setupBaseServer(t)
		token := "rate-limited-share"
		_, err := srv.db.Share.Create().
			SetTitle("Rate Limited Share").
			SetType(share.TypeUpload).
			SetTokenHash(internalShare.HashToken(token, cfg.Security.TokenSecret)).
			SetPasswordHash("invalid-hash").
			SetUnlockVersion(1).
			SetExpiresAt(mustTimeNowPlusHour()).
			Save(context.Background())
		require.NoError(t, err)

		form := url.Values{}
		form.Set("password", "wrong")

		var lastCode int
		for i := 0; i < 6; i++ {
			req := httptest.NewRequest(http.MethodPost, "/s/"+token+"/unlock", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			srv.echo.ServeHTTP(rec, req)
			lastCode = rec.Code
		}

		assert.Equal(t, http.StatusTooManyRequests, lastCode)
	})
}

func TestHandleCreateShare_UsesSelectedExpiration(t *testing.T) {
	srv, _ := setupBaseServer(t)
	createLocalAdminUser(t, srv, "admin", "secret123", false, true)

	reqForm := make(url.Values)
	reqForm.Set("type", "download")
	reqForm.Set("title", "Short Share")
	reqForm.Set("expiration", "24h")

	req := httptest.NewRequest(http.MethodPost, "/api/shares", strings.NewReader(reqForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	attachAdminSession(t, srv, req, "admin")

	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	sh, err := srv.db.Share.Query().Order(ent.Desc(share.FieldCreatedAt)).First(context.Background())
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(24*time.Hour), sh.ExpiresAt, 2*time.Minute)
}

func TestHandleCreateShare_RejectsInvalidExpiration(t *testing.T) {
	srv, _ := setupBaseServer(t)
	createLocalAdminUser(t, srv, "admin", "secret123", false, true)

	reqForm := make(url.Values)
	reqForm.Set("type", "download")
	reqForm.Set("title", "Bad Share")
	reqForm.Set("expiration", "999h")

	req := httptest.NewRequest(http.MethodPost, "/api/shares", strings.NewReader(reqForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	attachAdminSession(t, srv, req, "admin")

	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBuildDashboardShareViews_FilterAndSort(t *testing.T) {
	now := time.Now()
	downloadShare := &ent.Share{
		ID:        uuid.New(),
		Title:     "Download Share",
		Type:      share.TypeDownload,
		CreatedAt: now.Add(-4 * time.Hour),
		ExpiresAt: now.Add(24 * time.Hour),
		Edges: ent.ShareEdges{
			Files: []*ent.File{
				{CreatedAt: now.Add(-1 * time.Hour)},
			},
		},
	}
	uploadShare := &ent.Share{
		ID:        uuid.New(),
		Title:     "Upload Share",
		Type:      share.TypeUpload,
		CreatedAt: now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(48 * time.Hour),
		Edges: ent.ShareEdges{
			Files: []*ent.File{
				{CreatedAt: now.Add(-30 * time.Minute)},
			},
		},
	}
	idleShare := &ent.Share{
		ID:        uuid.New(),
		Title:     "Idle Share",
		Type:      share.TypeDownload,
		CreatedAt: now.Add(-10 * time.Minute),
		ExpiresAt: now.Add(12 * time.Hour),
	}

	views := buildDashboardShareViews(context.Background(), []*ent.Share{downloadShare, uploadShare, idleShare}, nil, false, "all", "activity")
	require.Len(t, views, 3)
	assert.Equal(t, "Idle Share", views[0].Title)
	assert.Equal(t, "Upload Share", views[1].Title)
	assert.Equal(t, "Download Share", views[2].Title)

	filtered := buildDashboardShareViews(context.Background(), []*ent.Share{downloadShare, uploadShare, idleShare}, nil, false, "upload", "activity")
	require.Len(t, filtered, 1)
	assert.Equal(t, "Upload Share", filtered[0].Title)

	expiring := buildDashboardShareViews(context.Background(), []*ent.Share{downloadShare, uploadShare, idleShare}, nil, false, "all", "expiry")
	require.Len(t, expiring, 3)
	assert.Equal(t, "Idle Share", expiring[0].Title)
}

func TestBreakGlassLogin_UsesBootstrapPassword(t *testing.T) {
	srv, _ := setupBreakGlassServer(t)
	createLocalAdminUser(t, srv, "admin", "secret123", false, true)

	f := make(url.Values)
	f.Set("username", "ignored")
	f.Set("password", "bootstrap-secret")

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(f.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	// Break glass redirects to '/' by default if no next is given
	assert.Equal(t, "/", rec.Header().Get("Location"))
	assert.Contains(t, rec.Header().Get("Set-Cookie"), sessionName)
}

func TestBreakGlassSetup_Unavailable(t *testing.T) {
	srv, _ := setupBreakGlassServer(t)

	req := httptest.NewRequest(http.MethodGet, "/setup", nil)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBreakGlassPublicShare_Blocked(t *testing.T) {
	srv, cfg := setupBreakGlassServer(t)

	passwordHash, err := auth.HashPassword("secret123")
	require.NoError(t, err)

	_, err = srv.db.Share.Create().
		SetTitle("Blocked Share").
		SetTokenHash(internalShare.HashToken("share-token", cfg.Security.TokenSecret)).
		SetType(share.TypeDownload).
		SetExpiresAt(mustTimeNowPlusHour()).
		SetPasswordHash(passwordHash).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/s/share-token", nil)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestBreakGlassUpload_Blocked(t *testing.T) {
	srv, _ := setupBreakGlassServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/upload", nil)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestBreakGlassCanManageUsers(t *testing.T) {
	srv, _ := setupBreakGlassServer(t)
	createLocalAdminUser(t, srv, "target", "oldpass", false, true)

	targetUser, err := srv.db.User.Query().Where(user.UsernameEQ("target")).Only(context.Background())
	require.NoError(t, err)

	f := make(url.Values)
	f.Set("password", "newpass123")
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetUser.ID.String()+"/password", strings.NewReader(f.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	attachBreakGlassSession(t, srv, req)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	loginForm := make(url.Values)
	loginForm.Set("username", "target")
	loginForm.Set("password", "oldpass")
	loginReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()

	// Create a normal server but use the same DB
	cfg := config.DefaultConfig()
	st, _ := storage.NewFileStorage(t.TempDir())
	normalSrv := NewServer(cfg, srv.db, nil, nil, st)
	normalSrv.SkipCSRF = true
	normalSrv.echo.ServeHTTP(loginRec, loginReq)
	assert.Equal(t, http.StatusUnauthorized, loginRec.Code)

	loginForm.Set("password", "newpass123")
	loginReq = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec = httptest.NewRecorder()
	normalSrv.echo.ServeHTTP(loginRec, loginReq)
	assert.Equal(t, http.StatusSeeOther, loginRec.Code)
}

func TestBreakGlassCreateShare_Blocked(t *testing.T) {
	srv, _ := setupBreakGlassServer(t)

	f := make(url.Values)
	f.Set("title", "Nope")
	req := httptest.NewRequest(http.MethodPost, "/api/shares", strings.NewReader(f.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	attachBreakGlassSession(t, srv, req)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestBreakGlassDeleteShare_Allowed(t *testing.T) {
	srv, _ := setupBreakGlassServer(t)

	sh, err := srv.db.Share.Create().
		SetTitle("Delete Me").
		SetTokenHash("delete-me").
		SetExpiresAt(mustTimeNowPlusHour()).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/admin/shares/"+sh.ID.String()+"/delete", nil)
	attachBreakGlassSession(t, srv, req)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	exists, err := srv.db.Share.Query().Where(share.IDEQ(sh.ID)).Exist(context.Background())
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestLogin_Permissions(t *testing.T) {
	srv, _ := setupBaseServer(t)

	// Create a user who can ONLY manage users
	createLocalAdminUser(t, srv, "usermgr", "pass123", false, false)
	u, _ := srv.db.User.Query().Where(user.UsernameEQ("usermgr")).Only(context.Background())
	_, _ = u.Update().SetCanManageUsers(true).Save(context.Background())

	t.Run("User Manager can login", func(t *testing.T) {
		f := make(url.Values)
		f.Set("username", "usermgr")
		f.Set("password", "pass123")

		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(f.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		srv.echo.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusSeeOther, rec.Code)
		assert.Equal(t, "/", rec.Header().Get("Location"))
	})
}

func TestUserManagement_Permissions(t *testing.T) {
	srv, _ := setupBaseServer(t)
	ctx := context.Background()

	// 1. Create a Superuser (NOT disabled, HAS global shares)
	createLocalAdminUser(t, srv, "super", "secret", false, true)
	uSuper, _ := srv.db.User.Query().Where(user.UsernameEQ("super")).Only(ctx)

	// 2. Create a Target User (NOT disabled, NO global shares)
	createLocalAdminUser(t, srv, "target", "secret", false, false)
	uTarget, _ := srv.db.User.Query().Where(user.UsernameEQ("target")).Only(ctx)
	// Helper sets can_manage_users to true, let's unset it for target
	_, _ = uTarget.Update().SetCanManageUsers(false).Save(ctx)
	uTarget, _ = srv.db.User.Get(ctx, uTarget.ID)

	t.Run("Update target permissions", func(t *testing.T) {
		f := make(url.Values)
		f.Set("can-manage-all-shares", "true")
		// can-manage-users is NOT set, so it should become false

		req := httptest.NewRequest(http.MethodPost, "/admin/users/"+uTarget.ID.String()+"/permissions/can-manage-all-shares", strings.NewReader(f.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		attachUserSession(t, srv, req, uSuper.ID)
		rec := httptest.NewRecorder()

		srv.echo.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		updated, _ := srv.db.User.Get(ctx, uTarget.ID)
		assert.True(t, updated.CanManageAllShares)
		assert.False(t, updated.CanManageUsers)
	})

	t.Run("Self-protection: cannot demote self", func(t *testing.T) {
		f := make(url.Values)
		// Try to remove own manage-users right
		req := httptest.NewRequest(http.MethodPost, "/admin/users/"+uSuper.ID.String()+"/permissions/can-manage-users", strings.NewReader(f.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		attachUserSession(t, srv, req, uSuper.ID)
		rec := httptest.NewRecorder()

		srv.echo.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		stillSuper, _ := srv.db.User.Get(ctx, uSuper.ID)
		assert.True(t, stillSuper.CanManageUsers, "Self-demotion must be blocked")
	})
}

func TestHandleDeleteShare_RemovesFilesAndShare(t *testing.T) {
	srv, _ := setupBaseServer(t)
	createLocalAdminUser(t, srv, "admin", "secret123", false, true)

	sh, err := srv.db.Share.Create().
		SetTitle("Delete Share").
		SetTokenHash("delete-share").
		SetExpiresAt(mustTimeNowPlusHour()).
		Save(context.Background())
	require.NoError(t, err)

	b, err := srv.db.Blob.Create().SetID("blob-delete").SetSize(1).SetStoragePath("blob-delete").Save(context.Background())
	require.NoError(t, err)

	_, err = srv.db.File.Create().
		SetOriginalName("hello.txt").
		SetBlob(b).
		SetShare(sh).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/admin/shares/"+sh.ID.String()+"/delete", nil)
	attachAdminSession(t, srv, req, "admin")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	shareExists, err := srv.db.Share.Query().Where(share.IDEQ(sh.ID)).Exist(context.Background())
	require.NoError(t, err)
	assert.False(t, shareExists)

	fileCount, err := srv.db.File.Query().Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, fileCount)
}

func TestHandleDeleteShare_ForbiddenForUserManager(t *testing.T) {
	srv, _ := setupBaseServer(t)

	createLocalAdminUser(t, srv, "shareadmin", "secret123", false, true)
	createLocalAdminUser(t, srv, "usermgr", "secret123", false, false)
	userMgr, err := srv.db.User.Query().Where(user.UsernameEQ("usermgr")).Only(context.Background())
	require.NoError(t, err)
	_, err = userMgr.Update().SetCanManageUsers(true).Save(context.Background())
	require.NoError(t, err)

	sh, err := srv.db.Share.Create().
		SetTitle("Protected Share").
		SetTokenHash("protected-share").
		SetExpiresAt(mustTimeNowPlusHour()).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/admin/shares/"+sh.ID.String()+"/delete", nil)
	attachAdminSession(t, srv, req, "usermgr")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandleDeleteUser_RemovesDisabledLocalUserAndOwnedShares(t *testing.T) {
	srv, _ := setupBaseServer(t)
	ctx := context.Background()

	createLocalAdminUser(t, srv, "super", "secret123", false, true)
	createLocalAdminUser(t, srv, "disabled-local", "secret123", true, false)

	targetUser, err := srv.db.User.Query().Where(user.UsernameEQ("disabled-local")).Only(ctx)
	require.NoError(t, err)

	ownedShare, err := srv.db.Share.Create().
		SetTitle("Owned by disabled user").
		SetTokenHash("owned-disabled-user").
		SetOwnerID(targetUser.ID).
		SetExpiresAt(mustTimeNowPlusHour()).
		Save(ctx)
	require.NoError(t, err)

	blob, err := srv.db.Blob.Create().SetID("blob-user-delete").SetSize(1).SetStoragePath("blob-user-delete").Save(ctx)
	require.NoError(t, err)

	_, err = srv.db.File.Create().
		SetOriginalName("delete-me.txt").
		SetBlob(blob).
		SetShare(ownedShare).
		Save(ctx)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetUser.ID.String()+"/delete", nil)
	attachAdminSession(t, srv, req, "super")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	userExists, err := srv.db.User.Query().Where(user.IDEQ(targetUser.ID)).Exist(ctx)
	require.NoError(t, err)
	assert.False(t, userExists)

	shareExists, err := srv.db.Share.Query().Where(share.IDEQ(ownedShare.ID)).Exist(ctx)
	require.NoError(t, err)
	assert.False(t, shareExists)

	fileCount, err := srv.db.File.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, fileCount)
}

func TestHandleDeleteUser_RejectsEnabledOrNonLocalUsers(t *testing.T) {
	srv, _ := setupBaseServer(t)
	ctx := context.Background()

	createLocalAdminUser(t, srv, "super", "secret123", false, true)
	createLocalAdminUser(t, srv, "enabled-local", "secret123", false, false)
	enabledLocal, err := srv.db.User.Query().Where(user.UsernameEQ("enabled-local")).Only(ctx)
	require.NoError(t, err)

	ldapUser, err := srv.db.User.Create().
		SetUsername("someone@example.test").
		SetDisplayName("LDAP User").
		SetAuthSource(user.AuthSourceLdap).
		SetDisabled(true).
		Save(ctx)
	require.NoError(t, err)

	t.Run("enabled local user cannot be deleted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/users/"+enabledLocal.ID.String()+"/delete", nil)
		attachAdminSession(t, srv, req, "super")
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("disabled ldap user cannot be deleted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/users/"+ldapUser.ID.String()+"/delete", nil)
		attachAdminSession(t, srv, req, "super")
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestHandleDeleteUser_RejectsSelfDeletion(t *testing.T) {
	srv, _ := setupBaseServer(t)
	ctx := context.Background()

	createLocalAdminUser(t, srv, "self", "secret123", false, true)
	selfUser, err := srv.db.User.Query().Where(user.UsernameEQ("self")).Only(ctx)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+selfUser.ID.String()+"/delete", nil)
	attachUserSession(t, srv, req, selfUser.ID)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleAdminSharePrepare_ShowsPublicShareURL(t *testing.T) {
	srv, cfg := setupBaseServer(t)
	createLocalAdminUser(t, srv, "admin", "secret123", false, true)

	token := "public-share-token"
	encrypted, err := internalShare.EncryptToken(token, cfg.Security.TokenSecret)
	require.NoError(t, err)

	sh, err := srv.db.Share.Create().
		SetTitle("Prepared Share").
		SetPublicTokenEncrypted(encrypted).
		SetTokenHash(internalShare.HashToken(token, cfg.Security.TokenSecret)).
		SetExpiresAt(mustTimeNowPlusHour()).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/admin/shares/"+sh.ID.String()+"/prepare", nil)
	attachAdminSession(t, srv, req, "admin")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), cfg.Server.PublicURL+"/s/"+token)
}

func TestHandleRotateSharePassword_RendersValidPublicLink(t *testing.T) {
	srv, cfg := setupBaseServer(t)
	createLocalAdminUser(t, srv, "admin", "secret123", false, true)

	token := "rotate-public-token"
	encrypted, err := internalShare.EncryptToken(token, cfg.Security.TokenSecret)
	require.NoError(t, err)

	sh, err := srv.db.Share.Create().
		SetTitle("Rotate Share").
		SetPublicTokenEncrypted(encrypted).
		SetTokenHash(internalShare.HashToken(token, cfg.Security.TokenSecret)).
		SetExpiresAt(mustTimeNowPlusHour()).
		Save(context.Background())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/admin/shares/"+sh.ID.String()+"/password", nil)
	attachAdminSession(t, srv, req, "admin")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), cfg.Server.PublicURL+"/s/"+token)
	assert.NotContains(t, rec.Body.String(), cfg.Server.PublicURL+"/s/</code>")
}

func TestHandleGetFilesFragment_ShowsRecentUploadSuccess(t *testing.T) {
	srv, cfg := setupBaseServer(t)
	ctx := context.Background()
	uploadSID := uuid.New()

	blob, err := srv.db.Blob.Create().
		SetID("fragment-success-blob").
		SetSize(12).
		SetStoragePath("fragment-success-blob").
		Save(ctx)
	require.NoError(t, err)

	t.Run("guest upload share", func(t *testing.T) {
		token := "guest-upload-fragment-token"
		sh, err := srv.db.Share.Create().
			SetTitle("Guest Upload").
			SetType(share.TypeUpload).
			SetTokenHash(internalShare.HashToken(token, cfg.Security.TokenSecret)).
			SetUnlockVersion(1).
			SetExpiresAt(mustTimeNowPlusHour()).
			Save(ctx)
		require.NoError(t, err)

		uploadedFile, err := srv.db.File.Create().
			SetOriginalName("guest.txt").
			SetBlob(blob).
			SetShare(sh).
			SetUploadSessionID(uploadSID).
			Save(ctx)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/s/%s/files-fragment?success_ids=%s", token, uploadedFile.ID.String()), nil)
		attachSessionValues(t, srv, req, map[interface{}]interface{}{
			guestSessionPrefix + token:    true,
			guestUnlockVersionKey + token: sh.UnlockVersion,
			uploadSessionIDPrefix + token: uploadSID.String(),
		})
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), uploadedFile.OriginalName)
		assert.Contains(t, rec.Body.String(), "Upload successful!")
	})

	t.Run("admin download share", func(t *testing.T) {
		createLocalAdminUser(t, srv, "fragment-admin", "secret123", false, true)
		adminUser, err := srv.db.User.Query().Where(user.UsernameEQ("fragment-admin")).Only(ctx)
		require.NoError(t, err)

		sh, err := srv.db.Share.Create().
			SetTitle("Admin Download").
			SetType(share.TypeDownload).
			SetTokenHash("admin-fragment-hash").
			SetExpiresAt(mustTimeNowPlusHour()).
			Save(ctx)
		require.NoError(t, err)

		uploadedFile, err := srv.db.File.Create().
			SetOriginalName("admin.txt").
			SetBlob(blob).
			SetShare(sh).
			SetUploadSessionID(uploadSID).
			Save(ctx)
		require.NoError(t, err)

		token := "id:" + sh.ID.String()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/s/%s/files-fragment?success_ids=%s", token, uploadedFile.ID.String()), nil)
		attachSessionValues(t, srv, req, map[interface{}]interface{}{
			userSessionKey:                adminUser.ID.String(),
			uploadSessionIDPrefix + token: uploadSID.String(),
		})
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), uploadedFile.OriginalName)
		assert.Contains(t, rec.Body.String(), "Upload successful!")
	})

	t.Run("many parallel completions stay badgeable", func(t *testing.T) {
		token := "guest-upload-many-fragment-token"
		sh, err := srv.db.Share.Create().
			SetTitle("Many Guest Uploads").
			SetType(share.TypeUpload).
			SetTokenHash(internalShare.HashToken(token, cfg.Security.TokenSecret)).
			SetUnlockVersion(1).
			SetExpiresAt(mustTimeNowPlusHour()).
			Save(ctx)
		require.NoError(t, err)

		successIDs := make([]string, 0, 25)
		for i := 0; i < 25; i++ {
			uploadedFile, err := srv.db.File.Create().
				SetOriginalName(fmt.Sprintf("many-%02d.txt", i)).
				SetBlob(blob).
				SetShare(sh).
				SetUploadSessionID(uploadSID).
				Save(ctx)
			require.NoError(t, err)
			successIDs = append(successIDs, uploadedFile.ID.String())
		}

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/s/%s/files-fragment?success_ids=%s", token, strings.Join(successIDs, ",")), nil)
		attachSessionValues(t, srv, req, map[interface{}]interface{}{
			guestSessionPrefix + token:    true,
			guestUnlockVersionKey + token: sh.UnlockVersion,
			uploadSessionIDPrefix + token: uploadSID.String(),
		})
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, len(successIDs), strings.Count(rec.Body.String(), "Upload successful!"))
	})
}

func TestHandleGetFilesFragment_DoesNotUseUploadSessionForSuccessBadge(t *testing.T) {
	srv, cfg := setupBaseServer(t)
	ctx := context.Background()
	token := "guest-upload-no-success-badge-token"
	uploadSID := uuid.New()

	blob, err := srv.db.Blob.Create().
		SetID("fragment-no-success-blob").
		SetSize(12).
		SetStoragePath("fragment-no-success-blob").
		Save(ctx)
	require.NoError(t, err)

	sh, err := srv.db.Share.Create().
		SetTitle("Guest Upload Without Success").
		SetType(share.TypeUpload).
		SetTokenHash(internalShare.HashToken(token, cfg.Security.TokenSecret)).
		SetUnlockVersion(1).
		SetExpiresAt(mustTimeNowPlusHour()).
		Save(ctx)
	require.NoError(t, err)

	_, err = srv.db.File.Create().
		SetOriginalName("old-session-file.txt").
		SetBlob(blob).
		SetShare(sh).
		SetUploadSessionID(uploadSID).
		Save(ctx)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/s/"+token+"/files-fragment", nil)
	attachSessionValues(t, srv, req, map[interface{}]interface{}{
		guestSessionPrefix + token:    true,
		guestUnlockVersionKey + token: sh.UnlockVersion,
		uploadSessionIDPrefix + token: uploadSID.String(),
	})
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "old-session-file.txt")
	assert.NotContains(t, rec.Body.String(), "Upload successful!")
}

func attachUserSession(t *testing.T, srv *Server, req *http.Request, userID uuid.UUID) {
	t.Helper()
	attachSessionValues(t, srv, req, map[interface{}]interface{}{
		userSessionKey: userID.String(),
	})
}

func attachSessionValues(t *testing.T, srv *Server, req *http.Request, values map[interface{}]interface{}) {
	t.Helper()
	rec := httptest.NewRecorder()
	store := sessions.NewCookieStore([]byte(srv.config.Security.SessionSecret))
	sess, _ := store.Get(req, sessionName)
	for key, value := range values {
		sess.Values[key] = value
	}
	_ = sess.Save(req, rec)
	req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))
}
