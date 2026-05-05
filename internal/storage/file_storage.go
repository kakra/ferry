package storage

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// FileStorage stores blobs on disk using SHA-256 based content addressing.
type FileStorage struct {
	basePath string
	tmpPath  string
}

// NewFileStorage creates the storage and temporary upload directories if needed.
func NewFileStorage(basePath string) (*FileStorage, error) {
	if err := os.MkdirAll(basePath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create storage path: %w", err)
	}
	if err := os.Chmod(basePath, 0700); err != nil {
		return nil, fmt.Errorf("failed to secure storage path: %w", err)
	}
	tmpPath := filepath.Join(basePath, "tmp")
	if err := os.MkdirAll(tmpPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create tmp path: %w", err)
	}
	if err := os.Chmod(tmpPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to secure tmp path: %w", err)
	}
	return &FileStorage{
		basePath: basePath,
		tmpPath:  tmpPath,
	}, nil
}

// GetTmpPath returns the directory used for temporary tusd upload files.
func (s *FileStorage) GetTmpPath() string {
	return s.tmpPath
}

// PutFromPath moves sourcePath into content-addressable storage and deduplicates by hash.
func (s *FileStorage) PutFromPath(sourcePath string) (*BlobInfo, error) {
	f, err := os.Open(sourcePath)
	if err != nil {
		// If the source file can't be opened, there's nothing to clean up, just error.
		return nil, fmt.Errorf("failed to open source file for hashing: %w", err)
	}

	hash := sha256.New()
	size, err := io.Copy(hash, f)
	f.Close() // Close immediately after reading
	if err != nil {
		// Hashing failed, but the source file still exists. The caller is responsible for it.
		return nil, fmt.Errorf("failed to hash file: %w", err)
	}

	hexHash := fmt.Sprintf("%x", hash.Sum(nil))
	relPath := s.getRelativePath(hexHash)
	fullPath := filepath.Join(s.basePath, relPath)

	if err := os.MkdirAll(filepath.Dir(fullPath), 0700); err != nil {
		// Don't remove the source, as the destination is unavailable. Let caller handle it.
		return nil, fmt.Errorf("failed to create blob directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(fullPath), 0700); err != nil {
		return nil, fmt.Errorf("failed to secure blob directory: %w", err)
	}

	isNew := false
	if _, err := os.Stat(fullPath); err == nil {
		// Blob exists, so we don't need the source file. Delete it.
		os.Remove(sourcePath)
	} else {
		// Blob does not exist, so move the source file to the final destination.
		if err := moveFile(sourcePath, fullPath); err != nil {
			// If move fails, the source file might still exist. The caller is responsible for it.
			return nil, fmt.Errorf("failed to move blob to final path: %w", err)
		}
		isNew = true
	}

	if err := os.Chmod(fullPath, 0600); err != nil {
		return nil, fmt.Errorf("failed to secure blob file: %w", err)
	}

	return &BlobInfo{
		Hash:        hexHash,
		Size:        size,
		StoragePath: relPath,
		IsNew:       isNew,
	}, nil
}

// moveFile attempts to rename a file, falling back to a copy-then-delete
// operation if the rename fails due to a cross-device link error.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		if linkErr, ok := err.(*os.LinkError); ok && linkErr.Err == syscall.EXDEV {
			// Cross-device moves require copy-then-delete semantics.
			srcFile, err := os.Open(src)
			if err != nil {
				return fmt.Errorf("moveFile: failed to open source for copy: %w", err)
			}
			defer srcFile.Close()

			dstFile, err := os.Create(dst)
			if err != nil {
				return fmt.Errorf("moveFile: failed to create destination for copy: %w", err)
			}
			defer dstFile.Close()

			if _, err := io.Copy(dstFile, srcFile); err != nil {
				// Attempt to clean up the partially written destination file on copy error.
				os.Remove(dst)
				return fmt.Errorf("moveFile: failed to copy file contents: %w", err)
			}

			// Ensure file handles are closed before attempting to remove the source.
			dstFile.Close()
			srcFile.Close()

			// Remove the source file after a successful copy.
			if err := os.Chmod(dst, 0600); err != nil {
				return fmt.Errorf("moveFile: failed to secure copied file: %w", err)
			}
			return os.Remove(src)
		}
		// The error was not a cross-device link error.
		return fmt.Errorf("moveFile: failed to rename file: %w", err)
	}
	if err := os.Chmod(dst, 0600); err != nil {
		return fmt.Errorf("moveFile: failed to secure moved file: %w", err)
	}
	return nil
}

// Open returns a reader for a stored blob hash.
func (s *FileStorage) Open(hash string) (io.ReadSeekCloser, error) {
	fullPath := filepath.Join(s.basePath, s.getRelativePath(hash))
	return os.Open(fullPath)
}

// Exists reports whether a blob hash exists in storage.
func (s *FileStorage) Exists(hash string) (bool, error) {
	fullPath := filepath.Join(s.basePath, s.getRelativePath(hash))
	_, err := os.Stat(fullPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// GetModTime returns the Unix modification time for a stored blob.
func (s *FileStorage) GetModTime(hash string) (int64, error) {
	fullPath := filepath.Join(s.basePath, s.getRelativePath(hash))
	info, err := os.Stat(fullPath)
	if err != nil {
		return 0, err
	}
	return info.ModTime().Unix(), nil
}

// Delete removes a stored blob by hash.
func (s *FileStorage) Delete(hash string) error {
	fullPath := filepath.Join(s.basePath, s.getRelativePath(hash))
	return os.Remove(fullPath)
}

// ListHashes returns all SHA-256 blob hashes currently stored on disk.
func (s *FileStorage) ListHashes() ([]string, error) {
	var hashes []string
	err := filepath.Walk(s.basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path == s.tmpPath {
				return filepath.SkipDir
			}
			return nil
		}

		name := filepath.Base(path)
		if name == "CACHEDIR.TAG" {
			return nil
		}

		// The filename is the hash
		hash := name
		if len(hash) == 64 && isHex(hash) {
			hashes = append(hashes, hash)
		}
		return nil
	})
	return hashes, err
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func (s *FileStorage) getRelativePath(hash string) string {
	if len(hash) < 4 {
		return hash
	}
	return filepath.Join(hash[0:2], hash[2:4], hash)
}
