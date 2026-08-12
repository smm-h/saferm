package db

import (
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Contention -- two processes reaching for the archive database's single write
// lock at the same moment -- is the one database failure that is not a failure
// at all: the loser only has to ask again. Everything in this file exists so
// that asking again happens inside saferm rather than in every caller.
//
// Before it, the driver's raw SQLITE_BUSY travelled all the way out to the
// caller as one undifferentiated database error, and saferm's own concurrency
// tests hand-rolled a retry loop around the binary to compensate -- the
// documentation promised automatic safety that only the tests were providing.

const (
	// busyMaxAttempts is the total number of tries a contended operation gets,
	// the first one included. It is the larger of the two counts saferm's
	// hand-rolled test loops used (3 and 5): those loops are the measured
	// record of what the suite already needed to pass reliably.
	busyMaxAttempts = 5

	// busyBackoffStep is the linear backoff unit: the pause before retry N is
	// N * busyBackoffStep, matching the 50ms-per-attempt schedule those same
	// loops used. Worst case the four pauses add 500ms on top of SQLite's own
	// busy_timeout waits.
	busyBackoffStep = 50 * time.Millisecond
)

// RetryNotifier is called once before each contention retry, so a caller can
// report the wait under --verbose. It is never called for a failure that is not
// contention, and never for the final attempt (there is no wait after it).
type RetryNotifier func(attempt, maxAttempts int, delay time.Duration, err error)

// ContentionError reports that an operation was still meeting a locked database
// after the whole retry budget was spent. It is deliberately its own type: a
// caller that collapses it into a generic database failure loses the one piece
// of information that distinguishes "another process is busy, try later" from
// "this database is broken".
type ContentionError struct {
	Attempts int
	Elapsed  time.Duration
	Err      error
}

func (e *ContentionError) Error() string {
	return fmt.Sprintf("database contention: still locked after %d attempts over %s: %s",
		e.Attempts, e.Elapsed.Round(time.Millisecond), e.Err)
}

func (e *ContentionError) Unwrap() error { return e.Err }

// IsContention reports whether err is SQLITE_BUSY/SQLITE_LOCKED-class
// contention -- a lock held by another connection, which retrying can clear.
//
// The driver reports a result code alongside the message, so the classification
// reads that code rather than matching on English. SQLite's extended codes
// carry the primary code in their low byte (SQLITE_BUSY_SNAPSHOT is
// SQLITE_BUSY | (2 << 8), and so on), so the low byte is what is compared --
// every extended flavour of BUSY and LOCKED classifies with its primary.
func IsContention(err error) bool {
	var serr *sqlite.Error
	if !errors.As(err, &serr) {
		return false
	}
	switch serr.Code() & 0xFF {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	}
	return false
}

// IsContentionExhausted reports whether err is a ContentionError -- contention
// that outlived the retry budget. It is what maps a database failure onto
// saferm's distinct contention exit code.
func IsContentionExhausted(err error) bool {
	var ce *ContentionError
	return errors.As(err, &ce)
}

// retry runs op, retrying it while it fails with database contention, and
// returns a *ContentionError once the budget is spent.
//
// Every operation it wraps is safe to run again: the reads are reads, and the
// writes are single statements that either took effect (in which case op
// returned nil and there is no retry) or did not (SQLITE_BUSY means the
// statement never got the write lock).
func (d *DB) retry(op func() error) error {
	return retryBusy(d.notify, op)
}

func retryBusy(notify RetryNotifier, op func() error) error {
	start := time.Now()
	var err error
	for attempt := 1; attempt <= busyMaxAttempts; attempt++ {
		err = op()
		if err == nil || !IsContention(err) {
			return err
		}
		if attempt == busyMaxAttempts {
			break
		}
		delay := time.Duration(attempt) * busyBackoffStep
		if notify != nil {
			notify(attempt, busyMaxAttempts, delay, err)
		}
		time.Sleep(delay)
	}
	return &ContentionError{Attempts: busyMaxAttempts, Elapsed: time.Since(start), Err: err}
}
