package storage

import (
	"io"
)

// BlobInfo contains metadata about a stored blob.
type BlobInfo struct {
	Hash         string
	Size         int64
	StoragePath  string
	IsNew        bool // True if the blob was newly created, false if it already existed
}

// Storage defines the interface for content-addressable storage.
type Storage interface {
	// PutFromPath stores the content from the file at sourcePath and returns the blob info.
	// It calculates the SHA-256 hash and moves the file to its final destination.
	PutFromPath(sourcePath string) (*BlobInfo, error)

	// Open returns a reader for the given hash.
	Open(hash string) (io.ReadCloser, error)

	// Exists checks if a blob with the given hash exists.
	Exists(hash string) (bool, error)

	// Delete removes the blob with the given hash.
	Delete(hash string) error

	// ListHashes returns all blob hashes currently in storage.
	ListHashes() ([]string, error)

	// GetModTime returns the modification time of a blob.
	// This is used by the integrity scan to implement a grace period.
	GetModTime(hash string) (int64, error)

	// GetTmpPath returns the path to the temporary directory managed by the storage.
	GetTmpPath() string
}
