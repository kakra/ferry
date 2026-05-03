package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/kakra/ferry/ent"
	"github.com/kakra/ferry/ent/enttest"
	"github.com/kakra/ferry/ent/file"
	"github.com/kakra/ferry/ent/share"
	"github.com/kakra/ferry/internal/auth"
	"github.com/kakra/ferry/internal/cleanup"
	"github.com/kakra/ferry/internal/config"
	"github.com/kakra/ferry/internal/i18n"
	internalShare "github.com/kakra/ferry/internal/share"
	"github.com/kakra/ferry/internal/storage"
	"github.com/kakra/ferry/internal/upload"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
)

func setupTestServer(t *testing.T) (*Server, *ent.Client, *config.Config) {
	_ = i18n.Init()
	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", uuid.New().String())
	client := enttest.Open(t, "sqlite3", dbName)
	cfg := config.DefaultConfig()
	cfg.Storage.Path = t.TempDir()

	st, _ := storage.NewFileStorage(cfg.Storage.Path)
	um, err := upload.NewManager(cfg, client, st)
	if err != nil {
		t.Fatalf("failed to create upload manager: %v", err)
	}

	cw := cleanup.NewWorker(cfg, client, st)
	srv := NewServer(cfg, client, cw, um, st)
	srv.SkipCSRF = true

	return srv, client, cfg
}

func TestHandleCreateShare_WithPassword(t *testing.T) {
	srv, client, _ := setupTestServer(t)
	createLocalAdminUser(t, srv, "admin", "secret123", false, true)

	f := make(url.Values)
	f.Set("type", "upload")
	f.Set("title", "Test Share")

	t.Run("Full Admin Flow Logic", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/shares", strings.NewReader(f.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		attachAdminSession(t, srv, req, "admin")

		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		// 1. Verify DB record
		sh, err := client.Share.Query().Order(ent.Desc(share.FieldCreatedAt)).First(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, sh.PasswordHash)

		// 2. Extract password from response using regex
		re := regexp.MustCompile(`<code id="share-password".*?>(.*)</code>`)
		matches := re.FindStringSubmatch(rec.Body.String())
		assert.Len(t, matches, 2, "Response should contain a generated password")
		password := strings.TrimSpace(matches[1])

		// 3. Verify the hash matches exactly this password
		match, err := auth.VerifyPassword(password, *sh.PasswordHash)
		assert.NoError(t, err)
		assert.True(t, match, "The stored hash must match the password shown to the admin")
	})
}

func TestAdminManagement_BypassAndPrep(t *testing.T) {
	srv, client, cfg := setupTestServer(t)
	createLocalAdminUser(t, srv, "admin", "secret123", false, true)

	// 1. Create a DOWNLOAD share
	token, _ := internalShare.GenerateToken()
	hashed := internalShare.HashToken(token, cfg.Security.TokenSecret)
	password := "pass"
	pwHash, _ := auth.HashPassword(password)

	sh, _ := client.Share.Create().
		SetTitle("Test Share").
		SetTokenHash(hashed).
		SetType(share.TypeDownload).
		SetExpiresAt(time.Now().Add(1 * time.Hour)).
		SetPasswordHash(pwHash).
		Save(context.Background())

	t.Run("Admin Prep Route -> Direct Access bypassing password", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/admin/shares/%s/prepare", sh.ID), nil)
		attachAdminSession(t, srv, req, "admin")
		rec := httptest.NewRecorder()

		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "id=\"upload-container\"")
		// Verify token format is id:<uuid>
		assert.Contains(t, rec.Body.String(), "id:"+sh.ID.String())
	})

	t.Run("Admin Upload via Prep -> Context Injection Check", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/upload/", nil)
		req.Header.Set("Tus-Resumable", "1.0.0")
		metadata := fmt.Sprintf("share_token %s", base64.StdEncoding.EncodeToString([]byte("id:"+sh.ID.String())))
		req.Header.Set("Upload-Metadata", metadata)

		attachAdminSession(t, srv, req, "admin")

		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		// 401 would mean unlock failed. 400 is fine (missing data for TUS).
		assert.NotEqual(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("Admin Deletes Other Guest File -> Success", func(t *testing.T) {
		b, _ := client.Blob.Create().SetID("h-admin").SetSize(1).SetStoragePath("p").Save(context.Background())
		f, _ := client.File.Create().
			SetOriginalName("guest.txt").
			SetBlob(b).
			SetShare(sh).
			SetUploadSessionID(uuid.New()).
			Save(context.Background())

		// We use id:<uuid> as token to simulate prep mode
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/s/id:%s/files/%s", sh.ID, f.ID), nil)
		attachAdminSession(t, srv, req, "admin")
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code)
	})
}

func TestHandlePublicShare_UnlockFlow(t *testing.T) {
	srv, client, cfg := setupTestServer(t)

	password := "unlock-me-123"
	hash, _ := auth.HashPassword(password)
	token, _ := internalShare.GenerateToken()

	_, _ = client.Share.Create().
		SetTitle("Test Share").
		SetTokenHash(internalShare.HashToken(token, cfg.Security.TokenSecret)).
		SetType(share.TypeUpload).
		SetExpiresAt(time.Now().Add(1 * time.Hour)).
		SetPasswordHash(hash).
		Save(context.Background())

	t.Run("Access Protected Share -> Redirect to Unlock", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/s/"+token, nil)
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusSeeOther, rec.Code)
		assert.Equal(t, "/s/"+token+"/unlock", rec.Header().Get("Location"))
	})

	t.Run("POST Correct Password -> Access Success", func(t *testing.T) {
		f := make(url.Values)
		f.Set("password", password)
		req := httptest.NewRequest(http.MethodPost, "/s/"+token+"/unlock", strings.NewReader(f.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusSeeOther, rec.Code)
		cookie := rec.Header().Get("Set-Cookie")

		// Use Cookie to access share
		accessReq := httptest.NewRequest(http.MethodGet, "/s/"+token, nil)
		accessReq.Header.Set("Cookie", cookie)
		rec = httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, accessReq)

		assert.Equal(t, http.StatusOK, rec.Code)
		// Check for presence of upload container
		assert.Contains(t, rec.Body.String(), "id=\"upload-container\"")
	})
}

func TestHandleTUSUpload_Security(t *testing.T) {
	srv, client, cfg := setupTestServer(t)
	token, _ := internalShare.GenerateToken()
	hashed := internalShare.HashToken(token, cfg.Security.TokenSecret)
	password := "secure-pass"
	pwHash, _ := auth.HashPassword(password)

	_, _ = client.Share.Create().
		SetTitle("Test Share").
		SetTokenHash(hashed).
		SetType(share.TypeUpload).
		SetExpiresAt(time.Now().Add(1 * time.Hour)).
		SetPasswordHash(pwHash).
		Save(context.Background())

	t.Run("POST /api/upload without unlock -> 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/upload/", nil)
		req.Header.Set("Tus-Resumable", "1.0.0")
		metadata := fmt.Sprintf("share_token %s", base64.StdEncoding.EncodeToString([]byte(token)))
		req.Header.Set("Upload-Metadata", metadata)

		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestHandleTUSUpload_PostThenPatch_Succeeds(t *testing.T) {
	srv, client, cfg := setupTestServer(t)
	token, _ := internalShare.GenerateToken()
	password := "secure-pass"
	pwHash, _ := auth.HashPassword(password)

	_, _ = client.Share.Create().
		SetTitle("Patch Share").
		SetTokenHash(internalShare.HashToken(token, cfg.Security.TokenSecret)).
		SetType(share.TypeUpload).
		SetExpiresAt(time.Now().Add(1 * time.Hour)).
		SetPasswordHash(pwHash).
		Save(context.Background())

	unlockForm := make(url.Values)
	unlockForm.Set("password", password)
	unlockReq := httptest.NewRequest(http.MethodPost, "/s/"+token+"/unlock", strings.NewReader(unlockForm.Encode()))
	unlockReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unlockRec := httptest.NewRecorder()
	srv.echo.ServeHTTP(unlockRec, unlockReq)
	cookie := unlockRec.Header().Get("Set-Cookie")

	postReq := httptest.NewRequest(http.MethodPost, "/api/upload/", nil)
	postReq.Header.Set("Cookie", cookie)
	postReq.Header.Set("Tus-Resumable", "1.0.0")
	postReq.Header.Set("Upload-Length", "10")
	postReq.Header.Set("Upload-Metadata", fmt.Sprintf("filename %s,share_token %s",
		base64.StdEncoding.EncodeToString([]byte("hello.txt")),
		base64.StdEncoding.EncodeToString([]byte(token)),
	))
	postRec := httptest.NewRecorder()
	srv.echo.ServeHTTP(postRec, postReq)

	assert.Equal(t, http.StatusCreated, postRec.Code)
	location := postRec.Header().Get("Location")
	assert.NotEmpty(t, location)

	patchReq := httptest.NewRequest(http.MethodPatch, location, bytes.NewBufferString("hello"))
	patchReq.Header.Set("Cookie", cookie)
	patchReq.Header.Set("Tus-Resumable", "1.0.0")
	patchReq.Header.Set("X-Ferry-Share-Token", token)
	patchReq.Header.Set("Upload-Offset", "0")
	patchReq.Header.Set("Content-Type", "application/offset+octet-stream")
	patchRec := httptest.NewRecorder()
	srv.echo.ServeHTTP(patchRec, patchReq)

	assert.Equal(t, http.StatusNoContent, patchRec.Code)
	assert.Equal(t, "5", patchRec.Header().Get("Upload-Offset"))

	missingTokenReq := httptest.NewRequest(http.MethodPatch, location, bytes.NewBufferString("hello"))
	missingTokenReq.Header.Set("Cookie", cookie)
	missingTokenReq.Header.Set("Tus-Resumable", "1.0.0")
	missingTokenReq.Header.Set("Upload-Offset", "0")
	missingTokenReq.Header.Set("Content-Type", "application/offset+octet-stream")
	missingTokenRec := httptest.NewRecorder()
	srv.echo.ServeHTTP(missingTokenRec, missingTokenReq)

	assert.Equal(t, http.StatusUnauthorized, missingTokenRec.Code)
}

func TestHandleFileDelete_Ownership_Full(t *testing.T) {
	srv, client, cfg := setupTestServer(t)
	token, _ := internalShare.GenerateToken()
	password := "pass"
	pwHash, _ := auth.HashPassword(password)

	sh, _ := client.Share.Create().
		SetTitle("Test Share").
		SetTokenHash(internalShare.HashToken(token, cfg.Security.TokenSecret)).
		SetType(share.TypeUpload).
		SetExpiresAt(time.Now().Add(1 * time.Hour)).
		SetPasswordHash(pwHash).
		Save(context.Background())

	b, _ := client.Blob.Create().SetID("h-del").SetSize(10).SetStoragePath("p-del").Save(context.Background())

	t.Run("Full Lifecycle Deletion", func(t *testing.T) {
		// 1. Unlock
		f := make(url.Values)
		f.Set("password", password)
		req := httptest.NewRequest(http.MethodPost, "/s/"+token+"/unlock", strings.NewReader(f.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)
		cookie := rec.Header().Get("Set-Cookie")

		// 2. GET Share to initialize SID
		req = httptest.NewRequest(http.MethodGet, "/s/"+token, nil)
		req.Header.Set("Cookie", cookie)
		rec = httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)
		// Update cookie if refreshed
		if c := rec.Header().Get("Set-Cookie"); c != "" {
			cookie = c
		}

		// 3. Extract SID from session store manually for file creation
		store := sessions.NewCookieStore([]byte(cfg.Security.SessionSecret))
		dummyReq := httptest.NewRequest(http.MethodGet, "/", nil)
		dummyReq.Header.Set("Cookie", cookie)
		sess, _ := store.Get(dummyReq, sessionName)
		sidStr, _ := sess.Values["upload_sid_"+token].(string)
		sid, _ := uuid.Parse(sidStr)

		fDel, _ := client.File.Create().
			SetID(uuid.New()).
			SetOriginalName("delete-me.txt").
			SetBlob(b).
			SetShare(sh).
			SetUploadSessionID(sid).
			Save(context.Background())

		// 4. Delete successfully
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/s/%s/files/%s", token, fDel.ID), nil)
		req.Header.Set("Cookie", cookie)
		rec = httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code)
		exists, _ := client.File.Query().Where(file.IDEQ(fDel.ID)).Exist(context.Background())
		assert.False(t, exists)
	})
}
