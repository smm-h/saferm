package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// SetupTestEnv creates a temporary directory structure mimicking ~/.saferm/
// with archive/ and db/ subdirectories. Returns the home directory (parent of
// .saferm/) suitable for passing to runSaferm which appends .saferm itself.
func SetupTestEnv(t *testing.T) string {
	t.Helper()

	homeDir := t.TempDir()
	safermDir := filepath.Join(homeDir, ".saferm")

	archiveDir := filepath.Join(safermDir, "archive")
	dbDir := filepath.Join(safermDir, "db")

	if err := os.MkdirAll(archiveDir, 0700); err != nil {
		t.Fatalf("creating archive dir: %v", err)
	}
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		t.Fatalf("creating db dir: %v", err)
	}

	return homeDir
}

// CreateTempFile creates a file with the given name and content inside dir.
// Returns the absolute path to the created file.
func CreateTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("creating parent dirs for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing file %s: %v", path, err)
	}
	return path
}

// CreateTempDir creates a directory with the given name inside parent,
// populates it with a few sample files, and returns the absolute path.
func CreateTempDir(t *testing.T, parent, name string) string {
	t.Helper()

	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("creating dir %s: %v", dir, err)
	}

	// Create some sample files
	CreateTempFile(t, dir, "file1.txt", "content of file1")
	CreateTempFile(t, dir, "file2.txt", "content of file2")

	// Create a subdirectory with a file
	subDir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("creating subdir: %v", err)
	}
	CreateTempFile(t, subDir, "nested.txt", "nested content")

	return dir
}
