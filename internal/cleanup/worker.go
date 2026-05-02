package cleanup

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kakra/ferry/ent"
	"github.com/kakra/ferry/ent/blob"
	"github.com/kakra/ferry/ent/file"
	"github.com/kakra/ferry/ent/share"
	"github.com/kakra/ferry/internal/config"
	"github.com/kakra/ferry/internal/storage"
)

// WorkerStats tracks the performance and results of the cleanup worker.
type WorkerStats struct {
	LastRun           time.Time
	LastMarkedCount   int
	LastSweptCount    int
	LastTmpCleanCount int
	LastOrphanCount   int
	IsRunning         bool
}

// Worker handles background maintenance tasks.
type Worker struct {
	db      *ent.Client
	config  *config.Config
	storage storage.Storage
	trigger chan struct{}

	mu    sync.RWMutex
	stats WorkerStats
}

// NewWorker creates a cleanup worker for temporary uploads, expired shares, and orphaned blobs.
func NewWorker(cfg *config.Config, db *ent.Client, st storage.Storage) *Worker {
	return &Worker{
		db:      db,
		config:  cfg,
		storage: st,
		trigger: make(chan struct{}, 1),
	}
}

// Start runs the worker loop in the background.
func (w *Worker) Start(ctx context.Context) {
	interval, err := time.ParseDuration(w.config.Cleanup.Interval)
	if err != nil {
		log.Printf("Invalid cleanup interval '%s', using 15m: %v", w.config.Cleanup.Interval, err)
		interval = 15 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("Cleanup worker started with interval %s", interval)

	for {
		select {
		case <-ctx.Done():
			log.Println("Cleanup worker stopping...")
			return
		case <-ticker.C:
			w.Perform(ctx)
		case <-w.trigger:
			log.Println("Cleanup triggered manually")
			w.Perform(ctx)
		}
	}
}

// Trigger requests an immediate execution of the worker.
func (w *Worker) Trigger() {
	select {
	case w.trigger <- struct{}{}:
	default:
		// Trigger already pending
	}
}

// GetStats returns a copy of the current worker statistics.
func (w *Worker) GetStats() WorkerStats {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.stats
}

// Perform executes the maintenance tasks.
func (w *Worker) Perform(ctx context.Context) {
	w.mu.Lock()
	if w.stats.IsRunning {
		w.mu.Unlock()
		return
	}
	w.stats.IsRunning = true
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		w.stats.IsRunning = false
		w.stats.LastRun = time.Now()
		w.mu.Unlock()
	}()

	log.Println("Performing garbage collection (Mark-and-Sweep)...")

	if w.db == nil || w.storage == nil {
		log.Println("Cleanup prerequisites not met, skipping")
		return
	}

	// Remove old TUS metadata and partial uploads.
	w.cleanupTmpDir()

	// Identify unreferenced blob records.
	w.performMarkPass(ctx)

	// Delete blob records marked during previous cleanup runs.
	w.performSweepPass(ctx)

	// Find physical files without database records.
	w.performIntegrityScan(ctx)

	// Remove expired shares and their file records.
	w.cleanupExpiredShares(ctx)
}

func (w *Worker) cleanupTmpDir() {
	grace, err := time.ParseDuration(w.config.Cleanup.DeleteIncompleteUploadsAfter)
	if err != nil {
		grace = 24 * time.Hour
	}
	threshold := time.Now().Add(-grace)
	count := 0

	err = filepath.Walk(w.storage.GetTmpPath(), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.ModTime().After(threshold) {
			return nil
		}
		log.Printf("GC Tmp: Removing old artifact %s", info.Name())
		if err := os.Remove(path); err == nil {
			count++
		}
		return nil
	})

	if err != nil {
		log.Printf("GC Tmp Error: %v", err)
	}
	w.mu.Lock()
	w.stats.LastTmpCleanCount = count
	w.mu.Unlock()
}

func (w *Worker) performMarkPass(ctx context.Context) {
	reachableHashes, err := w.db.Blob.Query().
		Where(blob.HasFilesWith(
			file.HasShareWith(share.ExpiresAtGT(time.Now())),
		)).
		IDs(ctx)
	if err != nil {
		log.Printf("GC Mark Error: failed to query reachable blobs: %v", err)
		return
	}

	reachableMap := make(map[string]bool)
	for _, h := range reachableHashes {
		reachableMap[h] = true
	}

	gracePeriod := time.Now().Add(-30 * time.Minute)
	allBlobs, err := w.db.Blob.Query().All(ctx)
	if err != nil {
		log.Printf("GC Mark Error: failed to query all blobs: %v", err)
		return
	}

	markedCount := 0
	unmarkedCount := 0

	for _, b := range allBlobs {
		if reachableMap[b.ID] {
			if b.UnreachableSince != nil {
				w.db.Blob.UpdateOne(b).ClearUnreachableSince().ExecX(ctx)
				unmarkedCount++
			}
			continue
		}

		if b.CreatedAt.After(gracePeriod) {
			continue
		}

		if b.UnreachableSince == nil {
			w.db.Blob.UpdateOne(b).SetUnreachableSince(time.Now()).ExecX(ctx)
			markedCount++
		}
	}

	if markedCount > 0 || unmarkedCount > 0 {
		log.Printf("GC Mark Pass: marked %d unreachable, unmarked %d reachable blobs", markedCount, unmarkedCount)
	}
	w.mu.Lock()
	w.stats.LastMarkedCount = markedCount
	w.mu.Unlock()
}

func (w *Worker) performSweepPass(ctx context.Context) {
	sweepThreshold := time.Now().Add(-24 * time.Hour)
	toSweep, err := w.db.Blob.Query().
		Where(blob.UnreachableSinceLT(sweepThreshold)).
		All(ctx)

	if err != nil {
		log.Printf("GC Sweep Error: failed to query candidates: %v", err)
		return
	}

	sweptCount := 0
	for _, b := range toSweep {
		log.Printf("GC Sweep: Physically deleting blob %s", b.ID)
		if err := w.storage.Delete(b.ID); err != nil {
			log.Printf("GC Sweep Warning: failed to delete physical file for %s: %v", b.ID, err)
		}
		if err := w.db.Blob.DeleteOne(b).Exec(ctx); err != nil {
			log.Printf("GC Sweep Error: failed to delete DB record for %s: %v", b.ID, err)
		} else {
			sweptCount++
		}
	}

	if sweptCount > 0 {
		log.Printf("GC Sweep Pass complete: physically removed %d blobs", sweptCount)
	}
	w.mu.Lock()
	w.stats.LastSweptCount = sweptCount
	w.mu.Unlock()
}

func (w *Worker) performIntegrityScan(ctx context.Context) {
	// Find physical hashes not present in DB
	hashes, err := w.storage.ListHashes()
	if err != nil {
		log.Printf("GC Integrity Error: failed to list storage: %v", err)
		return
	}

	// GRACE PERIOD: Ignore very young physical files (less than 30m old)
	// This prevents race conditions with active finalizations.
	graceThreshold := time.Now().Add(-30 * time.Minute).Unix()

	orphanCount := 0
	for _, hash := range hashes {
		mtime, err := w.storage.GetModTime(hash)
		if err != nil || mtime > graceThreshold {
			continue // Too young or error
		}

		exists, _ := w.db.Blob.Query().Where(blob.IDEQ(hash)).Exist(ctx)
		if !exists {
			log.Printf("GC Integrity: Found orphan physical file %s, deleting", hash)
			if err := w.storage.Delete(hash); err == nil {
				orphanCount++
			}
		}
	}
	w.mu.Lock()
	w.stats.LastOrphanCount = orphanCount
	w.mu.Unlock()
}

func (w *Worker) cleanupExpiredShares(ctx context.Context) {
	expiredShares, _ := w.db.Share.Query().
		Where(share.ExpiresAtLT(time.Now())).
		All(ctx)

	if len(expiredShares) > 0 {
		log.Printf("Removing %d expired shares from database", len(expiredShares))
		for _, s := range expiredShares {
			_, _ = w.db.File.Delete().Where(file.HasShareWith(share.ID(s.ID))).Exec(ctx)
			_ = w.db.Share.DeleteOne(s).Exec(ctx)
		}
	}
}
