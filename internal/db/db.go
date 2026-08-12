package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a queried record does not exist.
var ErrNotFound = errors.New("record not found")

// DB wraps a *sql.DB connection to the saferm SQLite database.
type DB struct {
	conn   *sql.DB
	notify RetryNotifier
}

// DeletionRecord represents a single archived deletion in the database.
type DeletionRecord struct {
	ID           int64
	UUID         string
	OriginalPath string
	OriginalName string
	Size         int64
	Hash         string
	IsDirectory  bool
	DeletedAt    time.Time
	Command      string     // may be empty
	Description  string
	Metadata     string     // JSON blob
	RestoredAt    *time.Time // nil if not restored
	RestoredTo    *string    // nil if not restored
	SymlinkTarget *string   // nil if not a symlink
	PurgedAt      *time.Time // nil if not purged
}

// busyTimeoutMS is how long SQLite itself waits for a held lock before handing
// back SQLITE_BUSY. saferm's own bounded retry (see contention.go) sits on top
// of it: SQLite absorbs the brief overlaps, the retry absorbs the long ones,
// and only a lock that outlives both reaches the caller.
const busyTimeoutMS = 5000

// Open opens (or creates) the SQLite database at dbPath with WAL mode and
// busy_timeout, then runs the schema DDL.
//
// notify, when non-nil, is called before each contention retry -- for every
// operation on the returned DB as well as for the schema work below, which is
// why it is supplied here rather than set afterwards. Pass nil for no
// reporting.
func Open(dbPath string, notify RetryNotifier) (*DB, error) {
	return open(dbPath, busyTimeoutMS, notify)
}

// open is Open with the busy timeout exposed, so tests can produce real
// contention without waiting seconds for it.
func open(dbPath string, busyTimeout int, notify RetryNotifier) (*DB, error) {
	// Pass pragmas via DSN so they take effect on every connection in the pool.
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout%%3d%d&_pragma=journal_mode%%3dWAL", dbPath, busyTimeout)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// Create tables and indexes, then run schema migrations. Both are write
	// paths and both are idempotent, so both are retried under contention.
	if err := retryBusy(notify, func() error {
		if _, err := conn.Exec(SchemaSQL); err != nil {
			return err
		}
		return migrate(conn)
	}); err != nil {
		conn.Close()
		return nil, err
	}

	return &DB{conn: conn, notify: notify}, nil
}

// migrate applies schema migrations based on PRAGMA user_version.
func migrate(conn *sql.DB) error {
	var version int
	if err := conn.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("reading user_version: %w", err)
	}

	if version < 1 {
		// Migration 1: add symlink_target column.
		// The column exists in fresh databases (from CREATE TABLE) but not
		// in databases created before this migration was added.
		present, err := hasColumn(conn, "deletions", "symlink_target")
		if err != nil {
			return fmt.Errorf("migration 1 (add symlink_target): %w", err)
		}
		if !present {
			if _, err := conn.Exec("ALTER TABLE deletions ADD COLUMN symlink_target TEXT"); err != nil {
				return fmt.Errorf("migration 1 (add symlink_target): %w", err)
			}
		}
		if _, err := conn.Exec("PRAGMA user_version = 1"); err != nil {
			return fmt.Errorf("setting user_version to 1: %w", err)
		}
	}

	if version < 2 {
		// Migration 2: add purged_at column.
		// The column exists in fresh databases (from CREATE TABLE) but not
		// in databases created before this migration was added.
		present, err := hasColumn(conn, "deletions", "purged_at")
		if err != nil {
			return fmt.Errorf("migration 2 (add purged_at): %w", err)
		}
		if !present {
			if _, err := conn.Exec("ALTER TABLE deletions ADD COLUMN purged_at TEXT"); err != nil {
				return fmt.Errorf("migration 2 (add purged_at): %w", err)
			}
		}
		if _, err := conn.Exec("PRAGMA user_version = 2"); err != nil {
			return fmt.Errorf("setting user_version to 2: %w", err)
		}
	}

	return nil
}

// hasColumn reports whether a table has a column with the given name.
//
// A failed query is an error, never a "no". It used to be a "no": the query
// error was discarded and the caller read the false as "the column is missing",
// then ran an ALTER TABLE that failed with its own unrelated message. Under the
// contention retry that wraps migrate, the swallowed error is worse than
// misleading -- SQLITE_BUSY read as "column missing" produces a follow-up
// failure that the classifier cannot recognize as contention, so the operation
// that would have succeeded on the next attempt fails permanently instead.
func hasColumn(conn *sql.DB, table, column string) (bool, error) {
	rows, err := conn.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, fmt.Errorf("reading columns of %s: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var ctype sql.NullString
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return false, fmt.Errorf("reading columns of %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("reading columns of %s: %w", table, err)
	}
	return false, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error {
	return d.conn.Close()
}

// Insert inserts a DeletionRecord and returns the auto-increment ID.
func (d *DB) Insert(rec *DeletionRecord) (int64, error) {
	var id int64
	err := d.retry(func() error {
		result, err := d.conn.Exec(
			`INSERT INTO deletions (uuid, original_path, original_name, size, hash, is_directory, deleted_at, command, description, metadata, restored_at, restored_to, symlink_target)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			rec.UUID,
			rec.OriginalPath,
			rec.OriginalName,
			rec.Size,
			rec.Hash,
			boolToInt(rec.IsDirectory),
			rec.DeletedAt.Format(time.RFC3339),
			nullableString(rec.Command),
			rec.Description,
			nullableString(rec.Metadata),
			nullableTime(rec.RestoredAt),
			rec.RestoredTo,
			rec.SymlinkTarget,
		)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		return err
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// QueryByID retrieves a single record by ID. Returns ErrNotFound if it does
// not exist.
func (d *DB) QueryByID(id int64) (*DeletionRecord, error) {
	var rec *DeletionRecord
	err := d.retry(func() error {
		row := d.conn.QueryRow(
			`SELECT id, uuid, original_path, original_name, size, hash, is_directory, deleted_at, command, description, metadata, restored_at, restored_to, symlink_target, purged_at
			 FROM deletions WHERE id = ?`, id)
		var err error
		rec, err = scanRecord(row)
		return err
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return rec, nil
}

// QueryByUUID retrieves a single record by its archive uuid. Returns
// ErrNotFound if it does not exist.
//
// The uuid is the identifier a record keeps: the numeric id is this database's
// autoincrement counter, while the uuid names the archived entry on disk and is
// what `delete` hands back to its caller.
func (d *DB) QueryByUUID(uuid string) (*DeletionRecord, error) {
	var rec *DeletionRecord
	err := d.retry(func() error {
		row := d.conn.QueryRow(
			`SELECT id, uuid, original_path, original_name, size, hash, is_directory, deleted_at, command, description, metadata, restored_at, restored_to, symlink_target, purged_at
			 FROM deletions WHERE uuid = ?`, uuid)
		var err error
		rec, err = scanRecord(row)
		return err
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return rec, nil
}

// QueryByPath returns all non-restored records matching the given original_path,
// ordered by deleted_at DESC (newest first).
func (d *DB) QueryByPath(path string) ([]*DeletionRecord, error) {
	var records []*DeletionRecord
	err := d.retry(func() error {
		rows, err := d.conn.Query(
			`SELECT id, uuid, original_path, original_name, size, hash, is_directory, deleted_at, command, description, metadata, restored_at, restored_to, symlink_target, purged_at
			 FROM deletions WHERE original_path = ? AND restored_at IS NULL AND purged_at IS NULL ORDER BY deleted_at DESC`, path)
		if err != nil {
			return err
		}
		defer rows.Close()
		records, err = scanRecords(rows)
		return err
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

// QueryAll returns all records ordered by deleted_at DESC. If includeAll
// is false, restored and purged records are excluded.
func (d *DB) QueryAll(includeAll bool) ([]*DeletionRecord, error) {
	query := `SELECT id, uuid, original_path, original_name, size, hash, is_directory, deleted_at, command, description, metadata, restored_at, restored_to, symlink_target, purged_at
		 FROM deletions`
	if !includeAll {
		query += " WHERE restored_at IS NULL AND purged_at IS NULL"
	}
	query += " ORDER BY deleted_at DESC"

	var records []*DeletionRecord
	err := d.retry(func() error {
		rows, err := d.conn.Query(query)
		if err != nil {
			return err
		}
		defer rows.Close()
		records, err = scanRecords(rows)
		return err
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

// MarkRestored sets restored_at to now and restored_to to the given path.
// Returns ErrNotFound if the record does not exist.
func (d *DB) MarkRestored(id int64, restoredTo string) error {
	now := time.Now().Format(time.RFC3339)
	return d.retry(func() error {
		result, err := d.conn.Exec(
			`UPDATE deletions SET restored_at = ?, restored_to = ? WHERE id = ?`,
			now, restoredTo, id)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// MarkPurged sets purged_at to now, preserving the metadata record.
// Returns ErrNotFound if the record does not exist.
func (d *DB) MarkPurged(id int64) error {
	now := time.Now().Format(time.RFC3339)
	return d.retry(func() error {
		result, err := d.conn.Exec(
			`UPDATE deletions SET purged_at = ? WHERE id = ?`,
			now, id)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// QueryOlderThan returns all non-restored, non-purged records deleted before the given time.
func (d *DB) QueryOlderThan(before time.Time) ([]*DeletionRecord, error) {
	var records []*DeletionRecord
	err := d.retry(func() error {
		rows, err := d.conn.Query(
			`SELECT id, uuid, original_path, original_name, size, hash, is_directory, deleted_at, command, description, metadata, restored_at, restored_to, symlink_target, purged_at
			 FROM deletions WHERE deleted_at < ? AND restored_at IS NULL AND purged_at IS NULL ORDER BY deleted_at DESC`,
			before.Format(time.RFC3339))
		if err != nil {
			return err
		}
		defer rows.Close()
		records, err = scanRecords(rows)
		return err
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

// scanner is the common interface between *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanOne scans a single row from any scanner into a DeletionRecord.
func scanOne(s scanner) (*DeletionRecord, error) {
	var rec DeletionRecord
	var isDir int
	var deletedAtStr string
	var command sql.NullString
	var metadata sql.NullString
	var restoredAtStr sql.NullString
	var restoredTo sql.NullString
	var purgedAtStr sql.NullString

	err := s.Scan(
		&rec.ID, &rec.UUID, &rec.OriginalPath, &rec.OriginalName,
		&rec.Size, &rec.Hash, &isDir, &deletedAtStr,
		&command, &rec.Description, &metadata,
		&restoredAtStr, &restoredTo, &rec.SymlinkTarget,
		&purgedAtStr,
	)
	if err != nil {
		return nil, err
	}

	rec.IsDirectory = isDir != 0
	rec.DeletedAt, err = time.Parse(time.RFC3339, deletedAtStr)
	if err != nil {
		return nil, err
	}
	if command.Valid {
		rec.Command = command.String
	}
	if metadata.Valid {
		rec.Metadata = metadata.String
	}
	if restoredAtStr.Valid {
		t, err := time.Parse(time.RFC3339, restoredAtStr.String)
		if err != nil {
			return nil, err
		}
		rec.RestoredAt = &t
	}
	if restoredTo.Valid {
		rec.RestoredTo = &restoredTo.String
	}
	if purgedAtStr.Valid {
		t, err := time.Parse(time.RFC3339, purgedAtStr.String)
		if err != nil {
			return nil, err
		}
		rec.PurgedAt = &t
	}

	return &rec, nil
}

// scanRecord scans a single *sql.Row into a DeletionRecord.
func scanRecord(row *sql.Row) (*DeletionRecord, error) {
	return scanOne(row)
}

// scanRecords scans multiple *sql.Rows into a slice of DeletionRecords.
func scanRecords(rows *sql.Rows) ([]*DeletionRecord, error) {
	var records []*DeletionRecord
	for rows.Next() {
		rec, err := scanOne(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

// boolToInt converts a bool to an int for SQLite storage.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullableString returns a sql.NullString for optional string fields.
func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// nullableTime formats a *time.Time as a sql.NullString for SQLite storage.
func nullableTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.Format(time.RFC3339), Valid: true}
}
