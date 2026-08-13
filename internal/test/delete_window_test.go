package test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/smm-h/saferm/internal/testutil"
)

// What `delete` does when the world changes under it mid-archival.
//
// The archival is two calls with the database insert between them, and the
// insert is not instantaneous: SQLite's busy_timeout is 5s and saferm retries
// five times on top of it, so a contended archive holds that window open for up
// to ~25 real seconds with the source path live the whole time. These tests
// open the window on purpose -- a held write lock stretches the insert, and the
// archive entry appearing is the signal that the window is open -- and change
// the path while `delete` is inside it.
//
// The archive-package tests cover the same two races at the unit level and in
// microseconds. These exist because the decision about the RECORD is the
// command's, not the archive's: which failures discard the entry, which leave
// it, and what the caller is told about the row that is already committed.

// inFlightDelete is a `saferm delete` stopped inside its window.
type inFlightDelete struct {
	entry   string // the archive entry Execute wrote, by name
	release func()
	done    chan deleteOutcome
	t       *testing.T
}

type deleteOutcome struct {
	stdout string
	stderr string
	code   int
}

// startDeleteInWindow runs `saferm delete` against a held write lock and
// returns once the archive entry exists, which is the proof that the archival
// wrote its entry and is now retrying the insert. Everything the caller does
// next happens inside the window.
func startDeleteInWindow(t *testing.T, home string, args ...string) *inFlightDelete {
	t.Helper()

	before := make(map[string]bool)
	for _, name := range archiveEntries(t, home) {
		before[name] = true
	}

	release := holdArchiveWriteLock(t, home)

	done := make(chan deleteOutcome, 1)
	go func() {
		stdout, stderr, code := runSaferm(t, home, args...)
		done <- deleteOutcome{stdout, stderr, code}
	}()

	var entry string
	deadline := time.Now().Add(20 * time.Second)
	for entry == "" {
		for _, name := range archiveEntries(t, home) {
			if !before[name] {
				entry = name
				break
			}
		}
		if entry != "" {
			break
		}
		if time.Now().After(deadline) {
			release()
			<-done
			t.Fatal("the archival never wrote an entry, so the window never opened")
		}
		time.Sleep(10 * time.Millisecond)
	}

	return &inFlightDelete{entry: entry, release: release, done: done, t: t}
}

// finish closes the window and waits for the command to end.
func (d *inFlightDelete) finish() deleteOutcome {
	d.t.Helper()
	d.release()
	select {
	case out := <-d.done:
		return out
	case <-time.After(90 * time.Second):
		d.t.Fatal("the delete never finished after the write lock was released")
		return deleteOutcome{}
	}
}

// A write through the original path while the insert is in flight rewrites the
// archived bytes with it, because the entry is a hard link to that same inode.
// The row that is already committed records the hash the file had BEFORE the
// write, and nothing can make it true again. Removing the source would leave
// that lie in the archive permanently; instead the entry is discarded, the file
// stays exactly where it is with the content it now has, and the caller is told
// the row names nothing.
func TestDelete_AFileWrittenThroughDuringTheInsertIsNotRemoved(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	seedArchive(t, home, work, "seed.txt")

	target := testutil.CreateTempFile(t, work, "edited.txt", "the bytes that were hashed\n")
	inFlight := startDeleteInWindow(t, home, "delete", "--on-error", "abort", "--description", "written through mid-archival", target)

	const rewritten = "rewritten while the insert was still retrying\n"
	if err := os.WriteFile(target, []byte(rewritten), 0644); err != nil {
		t.Fatalf("rewriting the source inside the window: %v", err)
	}
	out := inFlight.finish()

	if out.code == 0 {
		t.Fatalf("a delete whose archived bytes changed under it must not exit 0; stderr=%s", out.stderr)
	}
	if !strings.Contains(out.stderr, "was written to while it was being archived") {
		t.Errorf("the failure must say what happened, got: %s", out.stderr)
	}
	if !strings.Contains(out.stderr, "names nothing") {
		t.Errorf("the failure must say the committed row now names no archived copy, got: %s", out.stderr)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the source was removed although the record does not describe it: %v", err)
	}
	if string(content) != rewritten {
		t.Errorf("the source's content changed: %q", content)
	}

	// The entry is gone: keeping it would leave the archive holding a blob
	// whose row records a different hash, and a second name for a file the
	// caller is still writing to.
	if _, err := os.Stat(filepath.Join(home, ".saferm", "archive", inFlight.entry)); !os.IsNotExist(err) {
		t.Errorf("the archive entry that no longer matches its record was kept (err=%v)", err)
	}
}

// The path can be replaced outright while the insert is in flight, and a
// removal by name would then destroy a file nothing archived. Both the original
// (in the archive, under the committed row) and the replacement (at the path)
// survive.
func TestDelete_AFileReplacedDuringTheInsertIsNotRemoved(t *testing.T) {
	home := testutil.SetupTestEnv(t)
	work := t.TempDir()
	seedArchive(t, home, work, "seed.txt")

	target := testutil.CreateTempFile(t, work, "swapped.txt", "the archived original\n")
	inFlight := startDeleteInWindow(t, home, "delete", "--on-error", "abort", "--description", "replaced mid-archival", target)

	const replacement = "a completely different file at the same path\n"
	if err := os.Remove(target); err != nil {
		t.Fatalf("removing the source inside the window: %v", err)
	}
	if err := os.WriteFile(target, []byte(replacement), 0644); err != nil {
		t.Fatalf("recreating the path inside the window: %v", err)
	}
	out := inFlight.finish()

	if out.code == 0 {
		t.Fatalf("a delete whose path was replaced under it must not exit 0; stderr=%s", out.stderr)
	}
	if !strings.Contains(out.stderr, "no longer names the file that was archived") {
		t.Errorf("the failure must name the replacement, got: %s", out.stderr)
	}
	if !strings.Contains(out.stderr, "now holds something else") {
		t.Errorf("the failure must state what the path holds now, got: %s", out.stderr)
	}
	if !recordIdentifiers.MatchString(out.stderr) {
		t.Errorf("the failure must name the record that holds the original, got: %s", out.stderr)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the replacement was destroyed by a removal that went by name: %v", err)
	}
	if string(content) != replacement {
		t.Errorf("the replacement's content changed: %q", content)
	}

	// The record is truthful -- the entry holds exactly the bytes it was
	// written for -- so it is kept.
	archived, err := os.ReadFile(filepath.Join(home, ".saferm", "archive", inFlight.entry))
	if err != nil {
		t.Fatalf("the archive entry for the original is gone: %v", err)
	}
	if string(archived) != "the archived original\n" {
		t.Errorf("the archive entry holds %q, not the original", archived)
	}
}

// recordIdentifiers matches the `record [<id>] <uuid>` phrase every
// source-removal failure carries. Both identifiers are in it because both are
// handles the caller can act on, and after this failure the archived copy is
// the only thing that can be named -- the path is not what was archived.
var recordIdentifiers = regexp.MustCompile(`record \[[0-9]+\] [0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
