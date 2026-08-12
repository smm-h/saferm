package test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smm-h/saferm/internal/testutil"
	_ "modernc.org/sqlite"
)

// exitContention mirrors main.ExitContention. The command package is package
// main and cannot be imported, so the number is restated here on purpose: this
// test is the promise that the number saferm returns under exhausted database
// contention never silently changes.
const exitContention = 8

// holdArchiveWriteLock takes SQLite's write lock on the archive database from a
// second connection and holds it until the returned release func runs.
//
// BEGIN IMMEDIATE acquires the write lock at once (rather than on first write),
// so from the moment this returns every other process that tries to write is
// met with SQLITE_BUSY once its own busy_timeout expires. That is the
// deterministic contention the retry exists for, and the concurrency tests'
// hand-rolled retry loops used to paper over.
func holdArchiveWriteLock(t *testing.T, homeDir string) func() {
	t.Helper()

	dbPath := filepath.Join(homeDir, ".saferm", "db", "saferm.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("archive database %s does not exist yet: %v", dbPath, err)
	}

	sqlDB, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout%3d5000&_pragma=journal_mode%3dWAL")
	if err != nil {
		t.Fatalf("opening lock-holder connection: %v", err)
	}
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatalf("reserving lock-holder connection: %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("acquiring write lock: %v", err)
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
			conn.Close()
			sqlDB.Close()
		})
	}
	t.Cleanup(release)
	return release
}

func TestDeleteExitsWithContentionCodeWhenLockNeverReleased(t *testing.T) {
	if testing.Short() {
		t.Skip("exhausting the retry budget takes ~25s (five attempts against a 5s busy_timeout)")
	}
	t.Parallel()

	homeDir := t.TempDir()
	workDir := t.TempDir()
	// One successful delete first, so the database file and its schema exist
	// and a lock can be taken on them.
	seedArchive(t, homeDir, workDir, "seed.txt")

	holdArchiveWriteLock(t, homeDir)

	path := testutil.CreateTempFile(t, workDir, "blocked.txt", "blocked")
	_, stderr, code := runSaferm(t, homeDir, "delete", "--description", "blocked by a held write lock", path)

	if code != exitContention {
		t.Fatalf("expected exit %d (contention exhausted), got %d: stderr=%q", exitContention, code, stderr)
	}
	if !strings.Contains(stderr, "database contention") {
		t.Errorf("stderr does not name the contention: %q", stderr)
	}
}

func TestDeleteSucceedsWhenLockIsReleasedWithinRetryBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("waiting past one busy_timeout to reach the second attempt takes ~7s")
	}
	t.Parallel()

	homeDir := t.TempDir()
	workDir := t.TempDir()
	// One successful delete first, so the database file and its schema exist
	// and a lock can be taken on them.
	seedArchive(t, homeDir, workDir, "seed.txt")

	release := holdArchiveWriteLock(t, homeDir)
	// Held past one full busy_timeout (5s), so the first attempt really fails
	// and the delete can only succeed by retrying -- but well inside the retry
	// budget, so it does.
	go func() {
		time.Sleep(7 * time.Second)
		release()
	}()

	path := testutil.CreateTempFile(t, workDir, "delayed.txt", "delayed")
	start := time.Now()
	_, stderr, code := runSaferm(t, homeDir, "delete", "--verbose", "--description", "released within the retry budget", path)
	elapsed := time.Since(start)

	if code != 0 {
		t.Fatalf("expected success after the lock was released (exit %d): stderr=%q", code, stderr)
	}
	if elapsed < 5*time.Second {
		t.Errorf("delete returned in %s, which is before the first attempt could have hit busy_timeout; the lock was not contended", elapsed)
	}
	if !strings.Contains(stderr, "retrying") {
		t.Errorf("--verbose did not report the retry: stderr=%q", stderr)
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s still exists after a successful delete", path)
	}
	t.Logf("contention retry succeeded after %s", elapsed)
}
