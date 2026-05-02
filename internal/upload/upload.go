package upload

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kakra/ferry/ent"
	"github.com/kakra/ferry/ent/blob"
	"github.com/kakra/ferry/ent/share"
	"github.com/kakra/ferry/internal/config"
	internalShare "github.com/kakra/ferry/internal/share"
	"github.com/kakra/ferry/internal/storage"
	"github.com/tus/tusd/v2/pkg/filestore"
	"github.com/tus/tusd/v2/pkg/handler"
)

// ContextKey identifies request-scoped values consumed by the upload manager.
type ContextKey string

const (
	// SessionIDContextKey carries the server-assigned upload session ID.
	SessionIDContextKey ContextKey = "upload_session_id"
	// IsAdminContextKey marks uploads initiated from an authenticated management session.
	IsAdminContextKey ContextKey = "is_admin"
)

// Manager wraps the tusd handler and finalizes completed uploads into Ferry storage.
type Manager struct {
	Handler       *handler.UnroutedHandler
	router        http.Handler
	config        *config.Config
	db            *ent.Client
	storage       storage.Storage
	statusManager *StatusManager
}

// NewManager creates a resumable upload manager backed by temporary file storage.
func NewManager(cfg *config.Config, db *ent.Client, st storage.Storage) (*Manager, error) {
	tmpPath := st.GetTmpPath()

	store := filestore.New(tmpPath)
	composer := handler.NewStoreComposer()
	store.UseIn(composer)

	m := &Manager{
		config:        cfg,
		db:            db,
		storage:       st,
		statusManager: NewStatusManager(),
	}

	h, err := handler.NewUnroutedHandler(handler.Config{
		BasePath:                "/api/upload/",
		StoreComposer:           composer,
		NotifyCompleteUploads:   true,
		RespectForwardedHeaders: true,
		PreUploadCreateCallback: m.preUploadCreate,
	})
	if err != nil {
		return nil, err
	}
	m.Handler = h
	m.router = h.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/upload")
		path = strings.Trim(path, "/")

		prepareRequest := func(path string) *http.Request {
			r2 := r.Clone(r.Context())
			r2.URL = cloneURL(r.URL)
			if path == "" {
				r2.URL.Path = "/"
			} else {
				r2.URL.Path = "/" + path
			}
			r2.RequestURI = r2.URL.RequestURI()
			return r2
		}

		switch path {
		case "":
			switch r.Method {
			case http.MethodPost:
				h.PostFile(w, prepareRequest(path))
			default:
				w.Header().Add("Allow", "POST")
				w.WriteHeader(http.StatusMethodNotAllowed)
				_, _ = w.Write([]byte("method not allowed"))
			}
		default:
			switch r.Method {
			case http.MethodHead:
				h.HeadFile(w, prepareRequest(path))
			case http.MethodPatch:
				h.PatchFile(w, prepareRequest(path))
			default:
				w.Header().Add("Allow", "HEAD, PATCH")
				w.WriteHeader(http.StatusMethodNotAllowed)
				_, _ = w.Write([]byte("method not allowed"))
			}
		}
	}))

	return m, nil
}

func cloneURL(u *url.URL) *url.URL {
	if u == nil {
		return &url.URL{}
	}
	u2 := *u
	return &u2
}

func (m *Manager) preUploadCreate(hook handler.HookEvent) (handler.HTTPResponse, handler.FileInfoChanges, error) {
	token, ok := hook.Upload.MetaData["share_token"]
	if !ok {
		return handler.HTTPResponse{}, handler.FileInfoChanges{}, handler.NewError("ERR_MISSING_SHARE", "Missing share_token in metadata", http.StatusBadRequest)
	}

	var targetShare *ent.Share
	var err error
	var secureHash string

	if strings.HasPrefix(token, "id:") {
		// Management uploads use the internal share UUID and require an admin context.
		isAdmin, _ := hook.Context.Value(IsAdminContextKey).(bool)
		if !isAdmin {
			return handler.HTTPResponse{}, handler.FileInfoChanges{}, handler.NewError("ERR_UNAUTHORIZED", "Admin access required for ID-based upload", http.StatusUnauthorized)
		}

		idStr := strings.TrimPrefix(token, "id:")
		id, err := uuid.Parse(idStr)
		if err != nil {
			return handler.HTTPResponse{}, handler.FileInfoChanges{}, handler.NewError("ERR_INVALID_ID", "Invalid share ID format", http.StatusBadRequest)
		}

		targetShare, err = m.db.Share.Query().
			Where(share.IDEQ(id)).
			Where(share.ExpiresAtGT(time.Now())).
			Only(context.Background())

		if err == nil {
			secureHash = targetShare.TokenHash
		}
	} else {
		// Guest uploads use the public token and are limited to upload shares.
		hashed := internalShare.HashToken(token, m.config.Security.TokenSecret)
		secureHash = hashed

		targetShare, err = m.db.Share.Query().
			Where(share.TokenHashEQ(hashed)).
			Where(share.ExpiresAtGT(time.Now())).
			Only(context.Background())

		isAdmin, _ := hook.Context.Value(IsAdminContextKey).(bool)
		if err == nil && !isAdmin && targetShare.Type != share.TypeUpload {
			return handler.HTTPResponse{}, handler.FileInfoChanges{}, handler.NewError("ERR_FORBIDDEN", "This share does not allow guest uploads", http.StatusForbidden)
		}
	}

	if err != nil {
		return handler.HTTPResponse{}, handler.FileInfoChanges{}, handler.NewError("ERR_INVALID_SHARE", "Share not found or expired", http.StatusForbidden)
	}

	// Store only the token hash in tusd metadata so the public token is not persisted.
	md := handler.MetaData{
		"share_token_hash": secureHash,
		"filename":         hook.Upload.MetaData["filename"],
	}

	// The API layer assigns session IDs; clients must not be able to choose them.
	if sid, ok := hook.Context.Value(SessionIDContextKey).(string); ok {
		md["upload_session_id"] = sid
	}

	return handler.HTTPResponse{}, handler.FileInfoChanges{
		MetaData: md,
	}, nil
}

// StartHookListener listens for completed uploads and finalizes them.
func (m *Manager) StartHookListener(ctx context.Context) {
	log.Println("TUS Hook listener started")
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-m.Handler.CompleteUploads:
			m.statusManager.Set(event.Upload.ID, StatusProcessing)
			log.Printf("Upload completed: %s (Size: %d bytes), starting finalization", event.Upload.ID, event.Upload.Size)
			fileID, tokenHash, err := m.finalizeUpload(ctx, event.Upload)
			if err != nil {
				m.statusManager.Set(event.Upload.ID, StatusError)
				log.Printf("Error finalizing upload %s: %v", event.Upload.ID, err)
			} else {
				m.statusManager.SetExtended(event.Upload.ID, StatusComplete, fileID, tokenHash)
			}
		}
	}
}

func (m *Manager) finalizeUpload(ctx context.Context, info handler.FileInfo) (*uuid.UUID, string, error) {
	shareTokenHash, ok := info.MetaData["share_token_hash"]
	if !ok {
		return nil, "", fmt.Errorf("missing share_token_hash in metadata")
	}
	originalName := info.MetaData["filename"]
	if originalName == "" {
		originalName = "unnamed_file"
	}

	var uploadSessionID *uuid.UUID
	if sidStr, ok := info.MetaData["upload_session_id"]; ok {
		sid, err := uuid.Parse(sidStr)
		if err == nil {
			uploadSessionID = &sid
		}
	}

	tmpPath := filepath.Join(m.storage.GetTmpPath(), info.ID)

	if _, err := os.Stat(tmpPath); os.IsNotExist(err) {
		return nil, "", fmt.Errorf("completed upload file not found at path: %s", tmpPath)
	}

	blobInfo, err := m.storage.PutFromPath(tmpPath)
	if err != nil {
		// PutFromPath is responsible for cleaning up the tmpPath on failure
		return nil, "", fmt.Errorf("failed to store blob in CAS: %w", err)
	}

	// Remove a newly created blob if the database transaction cannot reference it.
	compensate := func() {
		if blobInfo.IsNew {
			log.Printf("Deleting orphaned new physical blob %s", blobInfo.Hash)
			_ = m.storage.Delete(blobInfo.Hash)
		}
	}

	tx, err := m.db.Tx(ctx)
	if err != nil {
		compensate()
		return nil, "", err
	}

	sh, err := tx.Share.Query().
		Where(share.TokenHashEQ(shareTokenHash)).
		Only(ctx)
	if err != nil {
		tx.Rollback()
		compensate()
		return nil, "", fmt.Errorf("failed to find share: %w", err)
	}

	b, err := tx.Blob.Query().Where(blob.IDEQ(blobInfo.Hash)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			b, err = tx.Blob.Create().
				SetID(blobInfo.Hash).
				SetSize(blobInfo.Size).
				SetStoragePath(blobInfo.StoragePath).
				Save(ctx)
		}
		if err != nil {
			tx.Rollback()
			compensate()
			return nil, "", fmt.Errorf("failed to handle blob record: %w", err)
		}
	}

	newFileID := uuid.New()
	fc := tx.File.Create().
		SetID(newFileID).
		SetOriginalName(originalName).
		SetBlob(b).
		SetShare(sh)

	if uploadSessionID != nil {
		fc.SetUploadSessionID(*uploadSessionID)
	}

	_, err = fc.Save(ctx)
	if err != nil {
		tx.Rollback()
		compensate()
		return nil, "", fmt.Errorf("failed to create file record: %w", err)
	}

	if err := tx.Commit(); err != nil {
		compensate()
		return nil, "", fmt.Errorf("failed to commit transaction: %w", err)
	}

	// PutFromPath moves or deletes the data file; tusd's metadata sidecar remains.
	os.Remove(tmpPath + ".info")

	log.Printf("Successfully finalized file '%s' (Hash: %s)", originalName, blobInfo.Hash)
	return &newFileID, shareTokenHash, nil
}

// ServeHTTP dispatches tusd upload requests through Ferry's constrained route wrapper.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.router.ServeHTTP(w, r)
}

// GetStatus returns the current processing state for an upload ID.
func (m *Manager) GetStatus(uploadID string) StatusInfo {
	return m.statusManager.Get(uploadID)
}
