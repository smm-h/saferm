# Own a real schema-migration mechanism (deferred)

Deferred deliberately. The immediate need is met by a weaker approach; this file records why that
is a stopgap and what the durable answer looks like.

## The immediate case

Two origin columns are being added, with the invariant that a version cannot be recorded without
a name. SQLite cannot add a CHECK constraint to an existing table — `ALTER TABLE ... ADD COLUMN`
supports defaults and types, not constraints. So the invariant can be expressed in three places
and only three:

- the fresh-schema `CREATE TABLE`, which new databases get
- application code at every write path, which all databases get
- a full table rebuild, which no database in this codebase has ever had

**Chosen now:** application-code enforcement only, with no constraint in either schema. Uniform
across fresh and migrated databases, one enforcement path, no rebuild precedent set. Accepted
cost: the invariant lives entirely in code, so a future write path that forgets it, or a
hand-edited database, can violate it silently — and an older binary writing to a newer database
bypasses it entirely.

**Deferred:** the rebuild, so the storage layer enforces the invariant rather than every caller
remembering to.

## The central question this raises

This will not be the last such change. The migration ladder handles exactly one shape of
change — adding a nullable column — because that is the only shape `ALTER TABLE` supports well.
Every other schema evolution needs a rebuild:

- adding or changing a CHECK constraint (the case above)
- adding a NOT NULL column without a usable default
- changing a column's type or its collation
- adding or removing a UNIQUE constraint
- dropping a column on older SQLite versions
- renaming a column on older SQLite versions
- introducing a foreign key
- reordering columns, if anything ever depends on ordinal position

So the real question is not "how do we add this one constraint." It is: **the tool needs a
migration mechanism it owns, because the archive is irreplaceable user data and the current
ladder can only express the easiest kind of change.**

What such a mechanism has to provide, at minimum:

- **The twelve-step rebuild done once, correctly, and reusably** — foreign keys off, new table
  created under a temporary name, data copied with any transformation, old table dropped, new
  one renamed, indexes and triggers recreated, integrity check, foreign keys back on, all inside
  a transaction. Written once as a helper, not open-coded per migration.
- **A backup before any destructive step.** The archive holds files the user asked to be able to
  recover; a failed rebuild that loses the index to them is worse than any bug it was fixing.
- **Resumability or strict atomicity.** A rebuild interrupted midway must leave a database that
  either opens as the old version or the new one, never a half-copied table under a temporary
  name.
- **A downgrade story, or an explicit refusal.** An older binary opening a newer database
  currently proceeds and silently bypasses code-level invariants. Either the version is checked
  and the old binary refuses, or the consequence is documented.
- **Verification after the fact** — row counts, an integrity check, and a spot comparison, so a
  silent truncation during copy is caught rather than shipped.
- **A way to test it.** Fixtures at each historical schema version, migrating forward to current,
  asserting data survives. The ladder has no such fixtures today.

## Why deferring is acceptable

Code-level enforcement is correct as long as every write path goes through it, and today they
all do. The invariant is also low-stakes: a version without a name is untidy metadata, not data
loss. Nothing about the deferral risks the archive.

What makes it a stopgap rather than a solution is the next change, not this one. The first
schema change that genuinely cannot be expressed as a nullable column will need the mechanism
above, and it will need it under time pressure. Building it before then is the difference between
a considered rebuild helper and one written in a hurry against a database holding data the user
cannot get back.

## Effort

The rebuild helper and its fixtures are a medium piece of work, and almost entirely testing.
The rebuild itself is a well-documented sequence; the value is in the backup, the verification
and the version fixtures, which is where the time goes.
