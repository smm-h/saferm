// Package db manages the SQLite database tracking all saferm deletions.
// It uses WAL mode and busy_timeout for concurrency safety across
// multiple simultaneous sessions.
package db
