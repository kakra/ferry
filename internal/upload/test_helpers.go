// +build !production

package upload

import (
	"time"

	"github.com/google/uuid"
)

// SetStatusForTest is a test-only helper to inject statuses into the manager.
func (m *Manager) SetStatusForTest(uploadID string, status UploadStatus, fileID *uuid.UUID, tokenHash string) {
	m.statusManager.mu.Lock()
	defer m.statusManager.mu.Unlock()
	m.statusManager.statuses[uploadID] = statusEntry{
		Status:         status,
		Timestamp:      time.Now(),
		FileID:         fileID,
		ShareTokenHash: tokenHash,
	}
}
