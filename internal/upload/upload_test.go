package upload

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kakra/ferry/ent/enttest"
	"github.com/kakra/ferry/ent/file"
	"github.com/kakra/ferry/ent/share"
	"github.com/kakra/ferry/internal/config"
	internalShare "github.com/kakra/ferry/internal/share"
	"github.com/kakra/ferry/internal/storage"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/tus/tusd/v2/pkg/handler"
)

type mockStorage struct {
	putFromPathFunc func(path string) (*storage.BlobInfo, error)
	delFunc         func(hash string) error
	listFunc        func() ([]string, error)
	mtimeFunc       func(hash string) (int64, error)
	tmpPath         string
}

func (m *mockStorage) PutFromPath(path string) (*storage.BlobInfo, error) {
	return m.putFromPathFunc(path)
}
func (m *mockStorage) Open(hash string) (io.ReadCloser, error) { return nil, nil }
func (m *mockStorage) Exists(hash string) (bool, error)        { return false, nil }
func (m *mockStorage) Delete(hash string) error {
	if m.delFunc != nil {
		return m.delFunc(hash)
	}
	return nil
}
func (m *mockStorage) ListHashes() ([]string, error) {
	if m.listFunc != nil {
		return m.listFunc()
	}
	return nil, nil
}
func (m *mockStorage) GetModTime(hash string) (int64, error) {
	if m.mtimeFunc != nil {
		return m.mtimeFunc(hash)
	}
	return 0, nil
}
func (m *mockStorage) GetTmpPath() string {
	return m.tmpPath
}

func TestManager_FinalizeUpload(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	tmpDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.Path = tmpDir

	// Setup: Create a share
	sh, _ := client.Share.Create().
		SetTitle("Test Share").
		SetTokenHash("target-hash").
		SetType(share.TypeUpload).
		SetExpiresAt(time.Now().Add(1 * time.Hour)).
		Save(context.Background())

	t.Run("Successful Finalization", func(t *testing.T) {
		// Setup: Mock storage
		st := &mockStorage{
			tmpPath: filepath.Join(tmpDir, "tmp"),
			putFromPathFunc: func(path string) (*storage.BlobInfo, error) {
				// PutFromPath is responsible for deleting the source file
				os.Remove(path)
				return &storage.BlobInfo{
					Hash:        "file-hash",
					Size:        10,
					StoragePath: "aa/bb/file-hash",
					IsNew:       true,
				}, nil
			},
		}
		os.MkdirAll(st.tmpPath, 0755)
		m, _ := NewManager(cfg, client, st)

		// Create dummy file in tmp
		uploadID := "upload-1"
		filePath := filepath.Join(st.tmpPath, uploadID)
		os.WriteFile(filePath, []byte("content"), 0644)
		os.WriteFile(filePath+".info", []byte("{}"), 0644)

		info := handler.FileInfo{
			ID:   uploadID,
			Size: 7,
			MetaData: handler.MetaData{
				"share_token_hash": "target-hash",
				"filename":         "hello.txt",
			},
		}

		fileID, tokenHash, err := m.finalizeUpload(context.Background(), info)
		assert.NoError(t, err)
		assert.NotNil(t, fileID)
		assert.Equal(t, "target-hash", tokenHash)

		// Verify DB records
		b, err := client.Blob.Get(context.Background(), "file-hash")
		assert.NoError(t, err)
		assert.Equal(t, int64(10), b.Size)

		f, err := client.File.Query().Where(file.HasShareWith(share.ID(sh.ID))).Only(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "hello.txt", f.OriginalName)

		// Verify cleanup
		_, err = os.Stat(filePath)
		assert.True(t, os.IsNotExist(err), "tmp file should be deleted")
	})

	t.Run("Safe Compensation - Deduplicated Blob", func(t *testing.T) {
		// Scenario: Blob already exists. Uploading same content. DB error occurs.
		// Existing blobs must not be deleted when a later database step fails.

		existingHash := "existing-hash"
		deleteCalled := false

		st := &mockStorage{
			tmpPath: filepath.Join(tmpDir, "tmp-dedup"),
			putFromPathFunc: func(path string) (*storage.BlobInfo, error) {
				return &storage.BlobInfo{
					Hash:        existingHash,
					Size:        20,
					StoragePath: "cc/dd/" + existingHash,
					IsNew:       false,
				}, nil
			},
			delFunc: func(hash string) error {
				if hash == existingHash {
					deleteCalled = true
				}
				return nil
			},
		}

		os.MkdirAll(st.tmpPath, 0755)
		m, _ := NewManager(cfg, client, st)

		// Create dummy file in tmp
		uploadID := "upload-err"
		filePath := filepath.Join(st.tmpPath, uploadID)
		os.WriteFile(filePath, []byte("content"), 0644)
		os.WriteFile(filePath+".info", []byte("{}"), 0644)

		info := handler.FileInfo{
			ID: uploadID,
			MetaData: handler.MetaData{
				"share_token_hash": "non-existent-share", // Forces DB error
			},
		}

		_, _, err := m.finalizeUpload(context.Background(), info)
		assert.Error(t, err)
		assert.False(t, deleteCalled, "Physical storage.Delete should NOT be called for existing blobs")
	})

	t.Run("Safe_Compensation_-_New_Blob", func(t *testing.T) {
		// Scenario: New Blob. DB error occurs.
		// Newly created blobs are removed when a later database step fails.

		newHash := "new-orphan-hash"
		deleteCalled := false

		st := &mockStorage{
			tmpPath: filepath.Join(tmpDir, "tmp-new"),
			putFromPathFunc: func(path string) (*storage.BlobInfo, error) {
				return &storage.BlobInfo{
					Hash:        newHash,
					Size:        30,
					StoragePath: "ee/ff/" + newHash,
					IsNew:       true,
				}, nil
			},
			delFunc: func(hash string) error {
				if hash == newHash {
					deleteCalled = true
				}
				return nil
			},
		}

		os.MkdirAll(st.tmpPath, 0755)
		m, _ := NewManager(cfg, client, st)

		uploadID := "upload-err-new"
		filePath := filepath.Join(st.tmpPath, uploadID)
		os.WriteFile(filePath, []byte("content"), 0644)
		os.WriteFile(filePath+".info", []byte("{}"), 0644)

		info := handler.FileInfo{
			ID: uploadID,
			MetaData: handler.MetaData{
				"share_token_hash": "non-existent-share", // Forces DB error
			},
		}

		_, _, _ = m.finalizeUpload(context.Background(), info)
		assert.True(t, deleteCalled, "Physical storage.Delete MUST be called for orphaned NEW blobs")
	})
}

func TestManager_RespectsForwardedHeadersForLocation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:forwarded?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	tmpDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.Path = tmpDir

	token := "forwarded-token"

	_, _ = client.Share.Create().
		SetTitle("Forwarded Share").
		SetTokenHash(internalShare.HashToken(token, cfg.Security.TokenSecret)).
		SetType(share.TypeUpload).
		SetExpiresAt(time.Now().Add(1 * time.Hour)).
		Save(context.Background())

	st, _ := storage.NewFileStorage(tmpDir)
	m, err := NewManager(cfg, client, st)
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/upload/", nil)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", "10")
	req.Header.Set("X-Forwarded-Host", "ferry.apps.netactive.local")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Upload-Metadata", "filename aGVsbG8udHh0,share_token "+encodeBase64(token))

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "https://ferry.apps.netactive.local/api/upload/")
}

func encodeBase64(v string) string {
	return base64.StdEncoding.EncodeToString([]byte(v))
}
