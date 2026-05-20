// Package git provides helpers for managing the git index alongside
// saferm's archive/restore operations.
package git

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// IsInGitRepo returns true if dir is inside a git working tree.
func IsInGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// IsGitTracked returns true if the file at path is tracked by git
// (i.e., known to the index). The path must be absolute or relative
// to the current working directory; the command runs from the file's
// parent directory.
func IsGitTracked(path string) bool {
	dir := filepath.Dir(path)
	cmd := exec.Command("git", "ls-files", "--error-unmatch", path)
	cmd.Dir = dir
	err := cmd.Run()
	return err == nil
}

// GitRmCached stages the removal of path in the git index without
// touching the working tree (the file is already archived).
// When recursive is true, -r is added for directory removal.
func GitRmCached(path string, recursive bool) error {
	args := []string{"rm", "--cached"}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, path)

	dir := filepath.Dir(path)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

// GitAdd stages a file in the git index. The command runs from the
// file's parent directory.
func GitAdd(path string) error {
	cmd := exec.Command("git", "add", path)
	cmd.Dir = filepath.Dir(path)
	return cmd.Run()
}
