package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kakra/ferry/ent/share"
	internalShare "github.com/kakra/ferry/internal/share"
	"github.com/kakra/ferry/internal/upload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleGetUploadStatus_SecurityAndHTMX(t *testing.T) {
	srv, cfg := setupBaseServer(t)
	token := "secret-test-token"
	tokenHash := internalShare.HashToken(token, cfg.Security.TokenSecret)

	// 1. Create a share in DB
	sh, err := srv.db.Share.Create().
		SetTitle("Security Test").
		SetTokenHash(tokenHash).
		SetType(share.TypeUpload).
		SetExpiresAt(time.Now().Add(1 * time.Hour)).
		Save(context.Background())
	require.NoError(t, err)

	// Create a blob and file for "complete" state
	fileID := uuid.New()
	b, _ := srv.db.Blob.Create().SetID("hash123").SetSize(100).SetStoragePath("p").Save(context.Background())
	_, _ = srv.db.File.Create().
		SetID(fileID).
		SetOriginalName("test.txt").
		SetBlob(b).
		SetShare(sh).
		Save(context.Background())

	uploadID := "test-upload-id"
	m := srv.upload

	t.Run("Reject Incorrect Token", func(t *testing.T) {
		m.SetStatusForTest(uploadID, upload.StatusComplete, &fileID, tokenHash)

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/upload/status/%s?token=wrong-token", uploadID), nil)
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("Accept Correct Token - JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/upload/status/%s?token=%s", uploadID, token), nil)
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]string
		json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.Equal(t, "complete", resp["status"])
	})

	t.Run("Accept Admin ID Token", func(t *testing.T) {
		adminToken := fmt.Sprintf("id:%s", sh.ID.String())
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/upload/status/%s?token=%s", uploadID, adminToken), nil)
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]string
		json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.Equal(t, "complete", resp["status"])
	})

	t.Run("HTMX Request - Event Trigger", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/upload/status/%s?token=%s", uploadID, token), nil)
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		var trigger map[string]map[string]string
		require.NoError(t, json.Unmarshal([]byte(rec.Header().Get("HX-Trigger")), &trigger))
		assert.Equal(t, fileID.String(), trigger["upload-complete"]["file_id"])
		assert.Equal(t, uploadID, trigger["upload-complete"]["upload_id"])

		body := rec.Body.String()
		assert.Equal(t, "<div></div>", body)
		assert.NotContains(t, body, "files-fragment")
		assert.NotContains(t, body, "<tr")
	})

	t.Run("HTMX Request - Processing State", func(t *testing.T) {
		procID := "proc-id"
		m.SetStatusForTest(procID, upload.StatusProcessing, nil, tokenHash)

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/upload/status/%s?token=%s", procID, token), nil)
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code)
		// Keep the existing HTMX polling row intact while the client-side scanner is shown.
		assert.Empty(t, rec.Body.String())
	})

	t.Run("HTMX Request - Error State", func(t *testing.T) {
		errID := "error-id"
		m.SetStatusForTest(errID, upload.StatusError, nil, tokenHash)

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/upload/status/%s?token=%s", errID, token), nil)
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		assert.Contains(t, body, `class="upload-status is-error"`)
		assert.Contains(t, body, "Processing failed")
		assert.NotContains(t, body, `style=`)
		assert.NotContains(t, body, "color: red")
	})
}

func TestAuthRateLimiter_ProtectsSetup(t *testing.T) {
	srv, cfg := setupBaseServer(t)
	cfg.Auth.BootstrapPassword = "bootstrap-secret"

	form := url.Values{}
	form.Set("bootstrap_password", "wrong")
	form.Set("username", "admin")
	form.Set("display_name", "Boss")
	form.Set("password", "secret123")

	var lastCode int
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.echo.ServeHTTP(rec, req)
		lastCode = rec.Code
	}

	assert.Equal(t, http.StatusTooManyRequests, lastCode)
}

func TestSecurityHeaders_AllowBrandingAndHTMX(t *testing.T) {
	srv, _ := setupBaseServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "SAMEORIGIN", rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "same-origin", rec.Header().Get("Referrer-Policy"))

	csp := rec.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "default-src 'self'")
	assert.Contains(t, csp, "script-src 'self' https://unpkg.com 'unsafe-inline' 'unsafe-eval'")
	assert.Contains(t, csp, "style-src 'self' https: http: 'unsafe-inline'")
	assert.Contains(t, csp, "img-src 'self' https: http: data:")
}
