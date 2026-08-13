package db

const SchemaSQL = `
CREATE TABLE IF NOT EXISTS deletions (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	uuid          TEXT NOT NULL UNIQUE,
	original_path TEXT NOT NULL,
	original_name TEXT NOT NULL,
	size          INTEGER NOT NULL,
	hash          TEXT NOT NULL,
	is_directory  INTEGER NOT NULL DEFAULT 0,
	deleted_at    TEXT NOT NULL,
	command       TEXT,
	description   TEXT NOT NULL,
	metadata      TEXT,
	restored_at   TEXT,
	restored_to   TEXT,
	symlink_target TEXT,
	purged_at     TEXT,
	-- Who ran the deletion, derived at insert time from the process trace store
	-- (STRICTCLI_TRACE_PARENT). Both nullable and never empty; null means no
	-- tool claimed this deletion. A version without a name is refused in code
	-- rather than by a CHECK constraint, so fresh and migrated databases are
	-- governed by one enforcement path -- see Insert.
	origin_name    TEXT,
	origin_version TEXT,
	-- The identifier every record of one delete invocation shares. Minted per
	-- invocation, so a batch is recoverable as a batch; null on rows written
	-- before the column existed.
	group_id       TEXT
);

CREATE INDEX IF NOT EXISTS idx_deletions_original_path ON deletions(original_path);
CREATE INDEX IF NOT EXISTS idx_deletions_deleted_at ON deletions(deleted_at);
`
