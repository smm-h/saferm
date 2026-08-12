// Package db manages the SQLite database tracking all saferm deletions.
//
// Concurrency safety across simultaneous sessions comes in three layers: WAL
// mode, SQLite's own busy_timeout, and a bounded retry on top of both -- every
// operation that meets SQLITE_BUSY or SQLITE_LOCKED is run again, up to five
// attempts with a 50ms linear backoff, reported through a RetryNotifier.
// Contention that outlives the whole budget is returned as a *ContentionError,
// a type distinct from every other database failure, so a caller can tell
// "another process holds the write lock, try again" from "this archive is
// broken".
package db
