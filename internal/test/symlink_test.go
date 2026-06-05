package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smm-h/saferm/internal/testutil"
)

// parseInfoField extracts the value of a labeled field from saferm info output.
// Fields look like "Size:          123 bytes (123 bytes)" or "Hash:          abc123".
// Returns the trimmed value string after the label.
func parseInfoField(t *testing.T, infoOutput, label string) string {
	t.Helper()
	for _, line := range strings.Split(infoOutput, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), label) {
			// The format is "Label:         value" — split on the first ":"
			_, value, ok := strings.Cut(line, ":")
			if !ok {
				t.Fatalf("malformed info line for label %q: %q", label, line)
			}
			return strings.TrimSpace(value)
		}
	}
	t.Fatalf("label %q not found in info output:\n%s", label, infoOutput)
	return ""
}

// TestDelete_Symlink_Standalone tests that deleting a standalone symlink (absolute target)
// records the correct metadata (size=0, no hash) and restores it as a symlink.
func TestDelete_Symlink_Standalone(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	workDir := t.TempDir()

	// Create a real file with known content.
	targetContent := "hello world\n"
	targetPath := testutil.CreateTempFile(t, workDir, "target.txt", targetContent)

	// Create an absolute symlink to it.
	symlinkPath := filepath.Join(workDir, "link.txt")
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	// Verify the symlink exists and points where we expect.
	linkDest, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("readlink before delete: %v", err)
	}
	if linkDest != targetPath {
		t.Fatalf("symlink target mismatch before delete: got %q, want %q", linkDest, targetPath)
	}

	// Delete the symlink.
	_, stderr, code := runSaferm(t, homeDir, "delete", "--description", "standalone symlink test", symlinkPath)
	if code != 0 {
		t.Fatalf("delete symlink failed (exit %d): stderr=%q", code, stderr)
	}

	// Verify the symlink is gone but target file is untouched.
	if _, err := os.Lstat(symlinkPath); !os.IsNotExist(err) {
		t.Fatal("symlink should be gone after delete")
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("reading target file after symlink delete: %v", err)
	}
	if string(got) != targetContent {
		t.Fatalf("target content changed after symlink delete: got %q, want %q", string(got), targetContent)
	}

	// Get the deletion ID from saferm list.
	stdout, _, _ := runSaferm(t, homeDir, "list")
	id := parseFirstID(t, stdout)

	// Run saferm info and parse the output.
	infoOut, stderr, code := runSaferm(t, homeDir, "info", id)
	if code != 0 {
		t.Fatalf("info failed (exit %d): stderr=%q", code, stderr)
	}

	// Assert: size should be "0 B (0 bytes)" — symlinks themselves have no content.
	// Currently, saferm records the symlink string length from os.Lstat, which will be
	// something like ~40 bytes (the length of the absolute target path).
	sizeField := parseInfoField(t, infoOut, "Size")
	if !strings.Contains(sizeField, "(0 bytes)") {
		t.Errorf("symlink size should be 0 bytes, got: %q", sizeField)
	}

	// Assert: hash should be empty or "-" — symlinks have no content to hash.
	// Currently, saferm hashes the TARGET file content (follows the symlink via os.Open).
	hashField := parseInfoField(t, infoOut, "Hash")
	if hashField != "" && hashField != "-" {
		t.Errorf("symlink hash should be empty or '-', got: %q", hashField)
	}

	// Assert: type should be "symlink" — currently saferm shows "file" for symlinks.
	typeField := parseInfoField(t, infoOut, "Type")
	if typeField != "symlink" {
		t.Errorf("symlink type should be 'symlink', got: %q", typeField)
	}

	// Undelete the symlink.
	_, stderr, code = runSaferm(t, homeDir, "undelete", id)
	if code != 0 {
		t.Fatalf("undelete symlink failed (exit %d): stderr=%q", code, stderr)
	}

	// Verify the restored path IS a symlink (not a regular file).
	info, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("lstat restored symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("restored path should be a symlink, but it is not")
	}

	// Verify the symlink points to the correct target.
	restoredTarget, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("readlink restored symlink: %v", err)
	}
	if restoredTarget != targetPath {
		t.Fatalf("restored symlink target mismatch: got %q, want %q", restoredTarget, targetPath)
	}

	// Verify the target file content is still intact.
	got, err = os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("reading target after undelete: %v", err)
	}
	if string(got) != targetContent {
		t.Fatalf("target content changed after undelete: got %q, want %q", string(got), targetContent)
	}
}

// TestDelete_Symlink_Relative tests that deleting a relative symlink preserves the
// exact relative target path through the delete/undelete cycle.
func TestDelete_Symlink_Relative(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	workDir := t.TempDir()

	// Create a subdirectory structure: dir/subdir/ and a file dir/target.txt
	dirPath := filepath.Join(workDir, "dir")
	subdirPath := filepath.Join(dirPath, "subdir")
	if err := os.MkdirAll(subdirPath, 0755); err != nil {
		t.Fatalf("creating subdirs: %v", err)
	}

	targetContent := "relative symlink target content"
	targetPath := testutil.CreateTempFile(t, dirPath, "target.txt", targetContent)

	// Create a relative symlink at dir/subdir/link.txt -> ../target.txt
	symlinkPath := filepath.Join(subdirPath, "link.txt")
	if err := os.Symlink("../target.txt", symlinkPath); err != nil {
		t.Fatalf("creating relative symlink: %v", err)
	}

	// Verify the symlink works (can read through it).
	got, err := os.ReadFile(symlinkPath)
	if err != nil {
		t.Fatalf("reading through relative symlink: %v", err)
	}
	if string(got) != targetContent {
		t.Fatalf("content through relative symlink: got %q, want %q", string(got), targetContent)
	}

	// Delete the symlink.
	_, stderr, code := runSaferm(t, homeDir, "delete", "--description", "relative symlink test", symlinkPath)
	if code != 0 {
		t.Fatalf("delete relative symlink failed (exit %d): stderr=%q", code, stderr)
	}

	// Verify the symlink is gone.
	if _, err := os.Lstat(symlinkPath); !os.IsNotExist(err) {
		t.Fatal("relative symlink should be gone after delete")
	}

	// Get the deletion ID.
	stdout, _, _ := runSaferm(t, homeDir, "list")
	id := parseFirstID(t, stdout)

	// Run saferm info and check size/hash.
	infoOut, stderr, code := runSaferm(t, homeDir, "info", id)
	if code != 0 {
		t.Fatalf("info failed (exit %d): stderr=%q", code, stderr)
	}

	sizeField := parseInfoField(t, infoOut, "Size")
	if !strings.Contains(sizeField, "(0 bytes)") {
		t.Errorf("relative symlink size should be 0 bytes, got: %q", sizeField)
	}

	hashField := parseInfoField(t, infoOut, "Hash")
	if hashField != "" && hashField != "-" {
		t.Errorf("relative symlink hash should be empty or '-', got: %q", hashField)
	}

	// Undelete the symlink.
	_, stderr, code = runSaferm(t, homeDir, "undelete", id)
	if code != 0 {
		t.Fatalf("undelete relative symlink failed (exit %d): stderr=%q", code, stderr)
	}

	// Verify restored path is a symlink.
	info, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("lstat restored relative symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("restored path should be a symlink, but it is not")
	}

	// Verify os.Readlink returns exactly "../target.txt" (relative path preserved).
	restoredTarget, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("readlink restored relative symlink: %v", err)
	}
	if restoredTarget != "../target.txt" {
		t.Fatalf("restored relative symlink target mismatch: got %q, want %q", restoredTarget, "../target.txt")
	}

	// Verify reading through the symlink returns the correct content.
	got, err = os.ReadFile(symlinkPath)
	if err != nil {
		t.Fatalf("reading through restored relative symlink: %v", err)
	}
	if string(got) != targetContent {
		t.Fatalf("content through restored relative symlink: got %q, want %q", string(got), targetContent)
	}

	// Verify the target file itself is untouched.
	got, err = os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("reading target after undelete: %v", err)
	}
	if string(got) != targetContent {
		t.Fatalf("target content changed after undelete: got %q, want %q", string(got), targetContent)
	}
}

// TestDelete_Symlink_Dangling tests that saferm can delete a dangling symlink
// (one whose target no longer exists) without erroring.
func TestDelete_Symlink_Dangling(t *testing.T) {
	homeDir := testutil.SetupTestEnv(t)
	workDir := t.TempDir()

	// Create a real file and a symlink to it.
	targetContent := "dangling symlink target"
	targetPath := testutil.CreateTempFile(t, workDir, "target.txt", targetContent)
	symlinkPath := filepath.Join(workDir, "dangling-link.txt")
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	// Delete the REAL FILE (using os.Remove, not saferm) to make the symlink dangle.
	if err := os.Remove(targetPath); err != nil {
		t.Fatalf("removing target to create dangling symlink: %v", err)
	}

	// Verify the symlink is now dangling:
	// - os.Stat (follows symlinks) should fail
	// - os.Lstat (does not follow) should succeed
	if _, err := os.Stat(symlinkPath); !os.IsNotExist(err) {
		t.Fatalf("expected os.Stat to fail on dangling symlink, got err: %v", err)
	}
	if _, err := os.Lstat(symlinkPath); err != nil {
		t.Fatalf("expected os.Lstat to succeed on dangling symlink, got err: %v", err)
	}

	// Run saferm delete on the dangling symlink.
	// Currently this will FAIL because hashFile calls os.Open which follows the
	// dangling symlink and gets a "no such file" error.
	_, stderr, code := runSaferm(t, homeDir, "delete", "--description", "dangling symlink test", symlinkPath)
	if code != 0 {
		t.Fatalf("delete dangling symlink should succeed (exit 0), got exit %d: stderr=%q", code, stderr)
	}

	// Verify the dangling symlink is gone.
	if _, err := os.Lstat(symlinkPath); !os.IsNotExist(err) {
		t.Fatal("dangling symlink should be gone after delete")
	}

	// Get the ID and run info.
	stdout, _, _ := runSaferm(t, homeDir, "list")
	id := parseFirstID(t, stdout)

	infoOut, stderr, code := runSaferm(t, homeDir, "info", id)
	if code != 0 {
		t.Fatalf("info failed (exit %d): stderr=%q", code, stderr)
	}

	// Size should be 0 for a symlink.
	sizeField := parseInfoField(t, infoOut, "Size")
	if !strings.Contains(sizeField, "(0 bytes)") {
		t.Errorf("dangling symlink size should be 0 bytes, got: %q", sizeField)
	}

	// Hash should be empty for a dangling symlink (no content to hash).
	hashField := parseInfoField(t, infoOut, "Hash")
	if hashField != "" && hashField != "-" {
		t.Errorf("dangling symlink hash should be empty or '-', got: %q", hashField)
	}

	// Undelete the dangling symlink.
	_, stderr, code = runSaferm(t, homeDir, "undelete", id)
	if code != 0 {
		t.Fatalf("undelete dangling symlink failed (exit %d): stderr=%q", code, stderr)
	}

	// Verify the restored path is a dangling symlink pointing to the original target.
	info, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("lstat restored dangling symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("restored path should be a symlink, but it is not")
	}

	restoredTarget, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("readlink restored dangling symlink: %v", err)
	}
	if restoredTarget != targetPath {
		t.Fatalf("restored dangling symlink target mismatch: got %q, want %q", restoredTarget, targetPath)
	}

	// The symlink should still be dangling (target was not restored).
	if _, err := os.Stat(symlinkPath); !os.IsNotExist(err) {
		t.Fatal("restored symlink should still be dangling (target was not restored)")
	}
}
