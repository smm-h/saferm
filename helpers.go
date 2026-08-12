package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/smm-h/saferm/internal/db"
	"github.com/smm-h/strictcli/go/strictcli"
)

// say prints progress or summary chatter to stdout unless --quiet is in force.
//
// It draws the line the framework's --quiet flag means in saferm: it is for
// what a caller can lose without losing information -- the counted summaries,
// the per-item --verbose progress, "Nothing to purge.", the restore
// confirmation. It is NOT for the outputs that ARE the command (`list` and
// `info`, and the dry-run previews), and it is not for stderr, which --quiet
// never touches. Routing every chatter line through one helper is what keeps
// that boundary from drifting one print at a time.
//
// --quiet dominates --verbose, matching the framework's own Context.Debug.
func say(ctx *strictcli.Context, format string, a ...interface{}) {
	if ctx.Quiet() {
		return
	}
	fmt.Printf(format, a...)
}

// ensureDirectories creates the base dir, archive dir, and db dir (parent of
// dbPath) if they don't exist.
//
// The creation is minted on the effects handle rather than performed directly,
// so a dry run declares it in the would-do log and makes nothing. It used to be
// a raw MkdirAll: the first-ever `saferm --dry-run delete` created saferm's
// whole state directory on its way to promising it would touch nothing.
//
// Only the three mutating commands call this. `list` and `info` are read_only,
// and the effects handle refuses a mutation from a read_only command outright
// -- they open the database where it already is and error where it is not.
func ensureDirectories(fx *strictcli.Effects, baseDir, archiveDir, dbPath string) error {
	dirs := []string{
		baseDir,
		archiveDir,
		filepath.Dir(dbPath),
	}
	for _, dir := range dirs {
		if _, err := fx.Mkdir(dir, strictcli.Resource("saferm-state:"+dir)); err != nil {
			return err
		}
	}
	return nil
}

// openArchiveDB opens the archive database, or reports that no archive exists.
//
// Under --dry-run ensureDirectories only records the directories it would
// create, so on a machine with no archive yet there is nothing for SQLite to
// open -- and creating the file to answer a preview would be exactly the write
// the preview promises not to make. The caller gets a nil *db.DB meaning "no
// archive yet" and must say so in its own vocabulary. Outside dry mode the
// directories have just been created, so the open always proceeds and the
// result is never nil.
func openArchiveDB(dryRun bool, dbPath string) (*db.DB, error) {
	if dryRun {
		return openArchiveDBIfPresent(dbPath)
	}
	return db.Open(dbPath)
}

// openArchiveDBIfPresent opens the archive database, or returns nil when there
// is no database file.
//
// It is what the read_only commands use. `list` and `info` never create
// saferm's state directory -- they cannot, the effects handle refuses a
// mutation from a read_only command -- so on a machine that has never deleted
// anything they meet a file that is not there. SQLite's "unable to open
// database file" is the wrong answer to "what have I deleted?"; the caller
// turns the nil into "nothing", which is the true one.
func openArchiveDBIfPresent(dbPath string) (*db.DB, error) {
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return db.Open(dbPath)
}

// pathSeparatorPlaceholder stands in for "/" while a pattern and a path are
// handed to filepath.Match. NUL is the one byte a POSIX path can never contain
// (the kernel interface terminates paths with it), so substituting it can never
// collide with real path content.
const pathSeparatorPlaceholder = "\x00"

// matchArchivePath reports whether an archived original path matches the
// user's --path glob.
//
// The syntax is filepath.Match's, with one deliberate difference: `*`, `?` and
// character classes match the path separator too, so a pattern names a subtree
// rather than exactly one directory level. saferm stores absolute original
// paths, which are always several levels deep, so the stock semantics made
// `--path "/home/*"` mean "the immediate children of /home and nothing else" --
// a filter that could not reach a single realistically archived path. Spanning
// separators is what the flag's own documented example always implied.
//
// Malformed patterns still come back as filepath.ErrBadPattern, so the caller
// keeps reporting them as usage errors rather than as an empty result.
func matchArchivePath(pattern, path string) (bool, error) {
	return filepath.Match(
		strings.ReplaceAll(pattern, "/", pathSeparatorPlaceholder),
		strings.ReplaceAll(path, "/", pathSeparatorPlaceholder),
	)
}

// humanSize formats a byte count as a human-readable string.
func humanSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	size := float64(bytes)
	for _, unit := range units {
		size /= 1024
		if size < 1024 {
			if size < 10 {
				return fmt.Sprintf("%.1f %s", size, unit)
			}
			return fmt.Sprintf("%.0f %s", size, unit)
		}
	}
	return fmt.Sprintf("%.0f TB", size)
}

// humanAge formats a time as a relative duration from now.
func humanAge(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	if days < 30 {
		return fmt.Sprintf("%d days ago", days)
	}
	months := days / 30
	if months == 1 {
		return "1 month ago"
	}
	if months < 12 {
		return fmt.Sprintf("%d months ago", months)
	}
	years := months / 12
	if years == 1 {
		return "1 year ago"
	}
	return fmt.Sprintf("%d years ago", years)
}

// parseSize parses a human-readable size string into bytes.
// Supported suffixes (case-insensitive): B, KB (1024), MB (1024^2), GB (1024^3), TB (1024^4).
// Suffix is mandatory — bare numbers are rejected.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size string")
	}

	upper := strings.ToUpper(s)

	// Find where the numeric part ends and the suffix begins.
	suffixStart := -1
	for i, ch := range upper {
		if ch < '0' || ch > '9' {
			suffixStart = i
			break
		}
	}

	if suffixStart <= 0 {
		// Either no digits at all, or no suffix (bare number).
		if suffixStart == 0 {
			return 0, fmt.Errorf("invalid size %q: no numeric part", s)
		}
		return 0, fmt.Errorf("invalid size %q: suffix is mandatory (use B, KB, MB, GB, or TB)", s)
	}

	numStr := s[:suffixStart]
	suffix := upper[suffixStart:]

	n, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: number part %q is not a valid integer", s, numStr)
	}
	if n <= 0 {
		return 0, fmt.Errorf("invalid size %q: must be positive", s)
	}

	var multiplier int64
	switch suffix {
	case "B":
		multiplier = 1
	case "KB":
		multiplier = 1024
	case "MB":
		multiplier = 1024 * 1024
	case "GB":
		multiplier = 1024 * 1024 * 1024
	case "TB":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("invalid size %q: unknown suffix %q (use B, KB, MB, GB, or TB)", s, suffix)
	}

	result := n * multiplier
	// Check for overflow: if dividing back doesn't give the original number, it overflowed.
	if result/multiplier != n {
		return 0, fmt.Errorf("invalid size %q: value overflows int64", s)
	}

	return result, nil
}

// parseDuration parses a human-friendly duration string.
// Supported suffixes: h (hours), d (days), w (weeks), m (months of 30 days).
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}

	suffix := s[len(s)-1:]
	numStr := s[:len(s)-1]

	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: number part %q is not an integer", s, numStr)
	}
	if n <= 0 {
		return 0, fmt.Errorf("invalid duration %q: must be positive", s)
	}

	switch suffix {
	case "h":
		return time.Duration(n) * time.Hour, nil
	case "d":
		return time.Duration(n) * 24 * time.Hour, nil
	case "w":
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	case "m":
		return time.Duration(n) * 30 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid duration %q: unknown suffix %q (use h, d, w, or m)", s, suffix)
	}
}
