package storage

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileStorage_Put(t *testing.T) {
	base := t.TempDir()

	s, err := NewFileStorage(base)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	baseInfo, err := os.Stat(base)
	if err != nil {
		t.Fatalf("failed to stat base dir: %v", err)
	}
	assert.Equal(t, os.FileMode(0700), baseInfo.Mode().Perm())

	tmpInfo, err := os.Stat(filepath.Join(base, "tmp"))
	if err != nil {
		t.Fatalf("failed to stat tmp dir: %v", err)
	}
	assert.Equal(t, os.FileMode(0700), tmpInfo.Mode().Perm())

	content := []byte("hello ferry")
	expectedHash := "cf82d2d961ff699eda330edfa1bc8ec368d42d901582a9534af98acfd1cde208" // SHA-256 for "hello ferry"

	// Create a source file for PutFromPath
	srcPath := filepath.Join(s.GetTmpPath(), "src-file")
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	info, err := s.PutFromPath(srcPath)
	if err != nil {
		t.Fatalf("PutFromPath failed: %v", err)
	}

	if info.Hash != expectedHash {
		t.Errorf("expected hash %s, got %s", expectedHash, info.Hash)
	}

	if info.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), info.Size)
	}

	// Verify file exists on disk
	fullPath := filepath.Join(base, info.StoragePath)
	if _, err := os.Stat(fullPath); err != nil {
		t.Errorf("file not found at %s", fullPath)
	}
	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		t.Fatalf("failed to stat blob file: %v", err)
	}
	assert.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm())

	// Test Exists
	exists, _ := s.Exists(expectedHash)
	if !exists {
		t.Error("Exists returned false for existing blob")
	}

	// Test Open
	rc, err := s.Open(expectedHash)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer rc.Close()

	readContent, _ := io.ReadAll(rc)
	if !bytes.Equal(content, readContent) {
		t.Errorf("expected content %s, got %s", string(content), string(readContent))
	}

	// Test Delete
	if err := s.Delete(expectedHash); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	exists, _ = s.Exists(expectedHash)
	if exists {
		t.Error("blob still exists after Delete")
	}
}

func Test_moveFile(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	dst := filepath.Join(tmp, "dst")
	content := []byte("move me")

	t.Run("Success Rename", func(t *testing.T) {
		os.WriteFile(src, content, 0644)
		err := moveFile(src, dst)
		assert.NoError(t, err)

		// Verify dst exists and src is gone
		data, _ := os.ReadFile(dst)
		assert.Equal(t, content, data)
		info, err := os.Stat(dst)
		assert.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
		_, err = os.Stat(src)
		assert.True(t, os.IsNotExist(err))
		os.Remove(dst)
	})

	t.Run("Error Source Not Found", func(t *testing.T) {
		err := moveFile(filepath.Join(tmp, "non-existent"), dst)
		assert.Error(t, err)
	})
}

func TestFileStorage_Deduplication(t *testing.T) {
	base := t.TempDir()

	s, err := NewFileStorage(base)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	content := []byte("duplicate")

	// First put
	src1 := filepath.Join(s.GetTmpPath(), "src1")
	os.WriteFile(src1, content, 0644)
	info1, _ := s.PutFromPath(src1)

	// Second put
	src2 := filepath.Join(s.GetTmpPath(), "src2")
	os.WriteFile(src2, content, 0644)
	info2, _ := s.PutFromPath(src2)

	if info1.Hash != info2.Hash {
		t.Error("identical content resulted in different hashes")
	}

	if info1.StoragePath != info2.StoragePath {
		t.Error("identical content resulted in different storage paths")
	}
}

func TestFileStorage_ListHashes_SkipsTmp(t *testing.T) {
	base := t.TempDir()
	s, _ := NewFileStorage(base)

	// Create a real blob
	src := filepath.Join(s.GetTmpPath(), "real")
	os.WriteFile(src, []byte("real"), 0644)
	info, _ := s.PutFromPath(src)

	// Create a file in tmp that shouldn't be listed
	tmpFile := filepath.Join(s.GetTmpPath(), "should-be-ignored")
	os.WriteFile(tmpFile, []byte("ignore me"), 0644)

	hashes, err := s.ListHashes()
	assert.NoError(t, err)
	assert.Len(t, hashes, 1)
	assert.Equal(t, info.Hash, hashes[0])
}
