package upload

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// UploadStatus describes the server-side finalization state of a tusd upload.
type UploadStatus string

const (
	// StatusProcessing means tusd completed the transfer and Ferry is creating records.
	StatusProcessing UploadStatus = "processing"
	// StatusComplete means the upload has been finalized and is visible in the share.
	StatusComplete UploadStatus = "complete"
	// StatusError means finalization failed after tusd accepted the upload.
	StatusError UploadStatus = "error"
)

type statusEntry struct {
	Status         UploadStatus
	Timestamp      time.Time
	FileID         *uuid.UUID
	ShareTokenHash string
}

// StatusInfo is the public status snapshot returned to upload clients.
type StatusInfo struct {
	Status         UploadStatus
	FileID         *uuid.UUID
	ShareTokenHash string
	Found          bool
}

// StatusManager tracks the finalization status of uploads.
// It is safe for concurrent use.
type StatusManager struct {
	statuses map[string]statusEntry
	mu       sync.RWMutex
}

// NewStatusManager creates a new status manager.
func NewStatusManager() *StatusManager {
	sm := &StatusManager{
		statuses: make(map[string]statusEntry),
	}
	go sm.cleanupLoop()
	return sm
}

// Set sets the status for a given upload ID.
func (sm *StatusManager) Set(uploadID string, status UploadStatus) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	entry := sm.statuses[uploadID]
	entry.Status = status
	entry.Timestamp = time.Now()
	sm.statuses[uploadID] = entry
}

// SetExtended sets the full status for a given upload ID.
func (sm *StatusManager) SetExtended(uploadID string, status UploadStatus, fileID *uuid.UUID, shareTokenHash string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.statuses[uploadID] = statusEntry{
		Status:         status,
		Timestamp:      time.Now(),
		FileID:         fileID,
		ShareTokenHash: shareTokenHash,
	}
}

// Get retrieves the status for a given upload ID.
func (sm *StatusManager) Get(uploadID string) StatusInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	entry, ok := sm.statuses[uploadID]
	if !ok {
		return StatusInfo{Status: StatusComplete, Found: false}
	}
	return StatusInfo{
		Status:         entry.Status,
		FileID:         entry.FileID,
		ShareTokenHash: entry.ShareTokenHash,
		Found:          true,
	}
}

// cleanupLoop periodically cleans up old status entries to prevent memory leaks.
func (sm *StatusManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		sm.mu.Lock()
		for id, entry := range sm.statuses {
			// Clean up entries older than 15 minutes
			if time.Since(entry.Timestamp) > 15*time.Minute {
				delete(sm.statuses, id)
			}
		}
		sm.mu.Unlock()
	}
}
