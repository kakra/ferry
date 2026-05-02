package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kakra/ferry/ent/blob"
	"github.com/kakra/ferry/ent/enttest"
	"github.com/kakra/ferry/internal/config"
	"github.com/kakra/ferry/internal/storage"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
)

func TestWorker_Trigger(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Cleanup.Interval = "24h"

	w := NewWorker(cfg, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan bool)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.trigger:
				done <- true
				return
			}
		}
	}()

	w.Trigger()

	select {
	case <-done:
		// Success
	case <-ctx.Done():
		t.Error("Worker was not triggered within timeout")
	}
}

func TestWorker_MarkAndSweep(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	st, _ := storage.NewFileStorage(t.TempDir())
	cfg := config.DefaultConfig()
	w := NewWorker(cfg, client, st)

	ctx := context.Background()

	// 1. Create a blob that is referenced by an active share
	sh1, _ := client.Share.Create().SetTitle("S1").SetTokenHash("t1").SetExpiresAt(time.Now().Add(1 * time.Hour)).Save(ctx)
	b1, _ := client.Blob.Create().SetID("hash1").SetSize(10).SetStoragePath("p1").Save(ctx)
	_, _ = client.File.Create().SetOriginalName("f1").SetBlob(b1).SetShare(sh1).Save(ctx)

	// 2. Create a blob that is referenced by an EXPIRED share (old enough for GC)
	sh2, _ := client.Share.Create().SetTitle("S2").SetTokenHash("t2").SetExpiresAt(time.Now().Add(-1 * time.Hour)).Save(ctx)
	b2, _ := client.Blob.Create().
		SetID("hash2").
		SetSize(10).
		SetStoragePath("p2").
		SetCreatedAt(time.Now().Add(-1 * time.Hour)).
		Save(ctx)
	_, _ = client.File.Create().SetOriginalName("f2").SetBlob(b2).SetShare(sh2).Save(ctx)

	// 3. Create an unreferenced blob (already older than grace period)
	b3, _ := client.Blob.Create().
		SetID("hash3").
		SetSize(10).
		SetStoragePath("p3").
		SetCreatedAt(time.Now().Add(-1 * time.Hour)).
		Save(ctx)

	// Create an unreferenced blob within the 30m grace period.
	b4, _ := client.Blob.Create().
		SetID("hash4").
		SetSize(10).
		SetStoragePath("p4").
		SetCreatedAt(time.Now().Add(-5 * time.Minute)).
		Save(ctx)

	t.Run("Mark Pass", func(t *testing.T) {
		w.Perform(ctx)

		// b1 should NOT be marked (referenced by sh1)
		db_b1, _ := client.Blob.Get(ctx, b1.ID)
		assert.Nil(t, db_b1.UnreachableSince)

		// b2 should be marked (referenced only by expired sh2)
		db_b2, _ := client.Blob.Get(ctx, b2.ID)
		assert.NotNil(t, db_b2.UnreachableSince)

		// b3 should be marked (unreferenced)
		db_b3, _ := client.Blob.Get(ctx, b3.ID)
		assert.NotNil(t, db_b3.UnreachableSince)

		// b4 should NOT be marked (within 30m grace period)
		db_b4, _ := client.Blob.Get(ctx, b4.ID)
		assert.Nil(t, db_b4.UnreachableSince)
	})

	t.Run("Unmark Pass", func(t *testing.T) {
		// Setup: b3 is marked. Let's make it reachable again by creating a file/share for it.
		sh3, _ := client.Share.Create().SetTitle("S3").SetTokenHash("t3").SetExpiresAt(time.Now().Add(1 * time.Hour)).Save(ctx)
		_, _ = client.File.Create().SetOriginalName("f3").SetBlob(b3).SetShare(sh3).Save(ctx)

		w.Perform(ctx)

		// b3 should now be UNMARKED
		db_b3, _ := client.Blob.Get(ctx, b3.ID)
		assert.Nil(t, db_b3.UnreachableSince)
	})

	t.Run("Sweep Pass", func(t *testing.T) {
		// Manually move b2's unreachable date to more than 24h ago
		client.Blob.UpdateOne(b2).SetUnreachableSince(time.Now().Add(-25 * time.Hour)).ExecX(ctx)

		w.Perform(ctx)

		// b2 should be gone from DB
		exists, _ := client.Blob.Query().Where(blob.IDEQ(b2.ID)).Exist(ctx)
		assert.False(t, exists)

		// b3 should still be there (now reachable again)
		exists, _ = client.Blob.Query().Where(blob.IDEQ(b3.ID)).Exist(ctx)
		assert.True(t, exists)
	})
}

func TestWorker_Hardening(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent_hard?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	storageDir := t.TempDir()
	st, _ := storage.NewFileStorage(storageDir)
	cfg := config.DefaultConfig()
	cfg.Storage.Path = storageDir
	cfg.Cleanup.DeleteIncompleteUploadsAfter = "1h"

	w := NewWorker(cfg, client, st)
	ctx := context.Background()

	t.Run("CAS Integrity Scan - Grace Period", func(t *testing.T) {
		// Create a physical orphan (no DB record)
		hash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // empty file hash
		fullPath := filepath.Join(storageDir, hash[0:2], hash[2:4], hash)
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		os.WriteFile(fullPath, []byte(""), 0644)

		// Case 1: Fresh file (within 30m) -> should NOT be deleted
		w.Perform(ctx)
		_, err := os.Stat(fullPath)
		assert.NoError(t, err, "Fresh orphan should be preserved")

		// Case 2: Old file (backdate mtime) -> should be deleted
		oldTime := time.Now().Add(-1 * time.Hour)
		os.Chtimes(fullPath, oldTime, oldTime)

		w.Perform(ctx)
		_, err = os.Stat(fullPath)
		assert.True(t, os.IsNotExist(err), "Old orphan should be deleted")
	})

	t.Run("TUS Resume Safety", func(t *testing.T) {
		tusFile := filepath.Join(st.GetTmpPath(), "active-upload")
		tusInfo := tusFile + ".info"

		os.WriteFile(tusFile, []byte("partial"), 0644)
		os.WriteFile(tusInfo, []byte("{}"), 0644)

		// Case 1: Active upload (within 1h) -> should NOT be deleted
		w.Perform(ctx)
		_, err := os.Stat(tusFile)
		assert.NoError(t, err, "Active TUS file should be preserved")
		_, err = os.Stat(tusInfo)
		assert.NoError(t, err, "Active TUS info should be preserved")

		// Case 2: Expired upload (backdate) -> should be deleted
		oldTime := time.Now().Add(-2 * time.Hour)
		os.Chtimes(tusFile, oldTime, oldTime)
		os.Chtimes(tusInfo, oldTime, oldTime)

		w.Perform(ctx)
		_, err = os.Stat(tusFile)
		assert.True(t, os.IsNotExist(err), "Expired TUS file should be deleted")
		_, err = os.Stat(tusInfo)
		assert.True(t, os.IsNotExist(err), "Expired TUS info should be deleted")
	})
}
