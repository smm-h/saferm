---
title: Machine surface
description: "How a program drives saferm: the --json envelope, the payload each consumer verb answers with, and the capabilities probe that replaces version checks."
---

# Machine surface

saferm is built to be driven by programs -- agents, launchers, release tools -- and not only by people reading tables. This page is the specification of that surface: what a machine sends, what it gets back, and what it may rely on.

The surface has three parts. Machine mode (`--json`) turns stdout into one JSON document. Four verbs answer with a structured payload inside that document. The `capabilities` verb says which features this particular saferm ships, so a caller negotiates by name rather than by version number.

## Machine mode

`--json` is owned by the CLI framework, not by saferm. It is recognized anywhere on the command line, has no short form, and combines with `--dry-run`.

```bash
saferm --json list
saferm --json --dry-run delete --on-error abort --description "why" ./build/
```

In machine mode **stdout carries exactly one document**: the envelope, serialized as JSON and terminated by a single newline. Everything saferm would have printed -- the archived-identifier lines, the tables, the counted summaries -- rides inside it as diagnostics instead of being printed beside it. Outside machine mode nothing changes: the tables are exactly what they always were.

`--quiet` cannot reach the envelope. It suppresses the human stream, and the envelope is not written through that stream, so `--json --quiet` emits the complete document.

## The envelope

| Key | Type | Meaning |
|-----|------|---------|
| `interface_version` | integer | The envelope contract's own version. `1` today. |
| `app` | string | `saferm`. |
| `app_version` | string | The version of the binary that ran. |
| `command` | string \| null | The command that ran (`list`, `config.show`). `null` when the run ended before a command resolved -- a parse error, an unknown command. |
| `exit_code` | integer | The process's exit status -- the same codes saferm returns outside machine mode. |
| `payload` | any \| null | The verb's own answer, described below. `null` for a verb that declares no payload, and for a run that failed before producing one. |
| `dry_run` | boolean | Whether the run was a preview. |
| `preview` | array | The structured effects the run recorded: every path it wrote, moved or removed. Populated in both modes. |
| `preview_error` | object \| null | Why a preview stopped early, when it did. |
| `diagnostics` | array | Every line the run would have printed, in order, as `{"level", "message"}`. |

The **exit code is still the verdict**. A payload is what a successful run produced; a failure is reported by the exit code and by the diagnostics, and saferm's exit codes mean exactly what they mean outside machine mode:

:-: table-exit-codes

## The four consumer verbs

`delete`, `undelete`, `list` and `info` each declare a payload schema and supply their value on every run, in both modes. `purge` deliberately declares none -- it is the one irreversible operation and the one that asks for consent, and nothing should be driving it from a parsed document.

### delete

```json
{
  "group_id": "0f2b...",
  "archived": [
    {"id": 3, "uuid": "6f1c0e2a-6c9e-4a24-9d1f-2b0f3f5b7c11", "path": "/home/user/project/old-config.yaml", "size": 612}
  ]
}
```

One entry per record the invocation wrote, in the order it wrote them -- the same content as the `archived: [id] uuid path (size)` lines, without the parsing. `group_id` is the identifier stamped on every record this one invocation wrote, which nothing on the human stream names.

Two properties a caller can rely on:

- An **aborted batch still answers**. `--on-error abort` stops at the first failing path, and the payload names everything archived above the failure. The exit code is the failure's.
- A **preview claims nothing**. `--dry-run` writes no records, so `archived` is empty; what the delete would do is the envelope's `preview`.

### undelete

```json
{
  "id": 3,
  "uuid": "6f1c0e2a-6c9e-4a24-9d1f-2b0f3f5b7c11",
  "original_path": "/home/user/project/old-config.yaml",
  "restored_to": "/tmp/inspect/old-config.yaml",
  "kind": "file",
  "overwrote": false
}
```

`restored_to` is where the content actually went, which differs from `original_path` whenever `--destination` was used. `overwrote` says whether something was standing there and was replaced -- the one fact nothing on the filesystem records afterwards. `kind` is one of `file`, `directory`, `symlink`.

Under `--dry-run` the payload names where the content *would* go; the envelope's `dry_run` flag is what tells the two apart.

### list

```json
[
  {
    "id": 3,
    "uuid": "6f1c0e2a-6c9e-4a24-9d1f-2b0f3f5b7c11",
    "path": "/home/user/project/db/migrations",
    "size": 14382,
    "kind": "directory",
    "deleted_at": "2026-08-13T14:32:01Z",
    "status": "archived"
  }
]
```

The rows, after `--path` filtering and subject to `--all`. Two members the table cannot carry are here: the `uuid` (the table has room only for the numeric id) and `deleted_at` as an absolute RFC3339 timestamp, where the Age column is relative prose. `status` is one of `archived`, `restored`, `purged`.

An empty archive answers `[]`, never `null`.

### info

```json
{
  "id": 3,
  "uuid": "6f1c0e2a-6c9e-4a24-9d1f-2b0f3f5b7c11",
  "original_path": "/home/user/project/old-config.yaml",
  "original_name": "old-config.yaml",
  "size": 612,
  "hash": "9f86d081...",
  "kind": "file",
  "symlink_target": null,
  "deleted_at": "2026-08-13T14:32:01Z",
  "status": "restorable",
  "description": "removing stale config",
  "command": "rm old-config.yaml",
  "restored_at": null,
  "restored_to": null,
  "purged_at": null,
  "origin_name": "claudewheel",
  "origin_version": "0.42.0",
  "group_id": "0f2b..."
}
```

Every nullable member is always present with `null` as its value: the difference between "no tool claimed this deletion" and "a tool named the empty string" is the whole content of the origin columns.

`status` is a closed set of words rather than the printed page's prose:

| Value | Meaning |
|-------|---------|
| `restorable` | The archived copy is there. |
| `restored` | The content was restored; `restored_to` names where it went. |
| `purged` | The archived content was destroyed; the metadata survives. |
| `restored-then-purged` | Both happened, in that order. |
| `entry-missing` | Nothing restored or purged it and the archived copy is not there. An archival that meets a changed source inside its window produces this state deliberately: the row names nothing, and `purge` is how it is cleared. |

The captured metadata blob -- the environment, the git context, the resolved ancestry chain -- is deliberately not on the payload. It is an open-ended document, and declaring it in a closed schema would mean either freezing it or describing it dishonestly. `saferm info` prints it.

## capabilities

```bash
saferm --json capabilities
```

```json
{"features": ["git-index-switches", "group-id", "machine-payloads", "on-conflict-modes", "on-error-modes", "restore-destination", "trace-origin", "uuid-handles"]}
```

The verb reads nothing -- no database, no archive directory, no configuration -- so it answers on a machine where saferm has never run, and it does not create saferm's state directory in order to answer.

| Feature | What shipping it means |
|---------|------------------------|
| `git-index-switches` | Both halves of the round trip can leave the git index alone: `delete --no-update-git-index` and `undelete --no-update-git-index`. |
| `group-id` | Every delete invocation stamps one group identifier on every record it writes; it is on `delete`'s and `info`'s payloads. |
| `machine-payloads` | `delete`, `undelete`, `list` and `info` answer with the payloads specified above. |
| `on-conflict-modes` | `undelete --on-conflict overwrite\|abort`, required exactly when the destination is occupied. |
| `on-error-modes` | `delete --on-error abort\|continue`, mandatory with no default. |
| `restore-destination` | `undelete --destination <path>` restores elsewhere and records where the content went. |
| `trace-origin` | Which tool ran a deletion is derived from the process trace store and recorded on the row. |
| `uuid-handles` | Every record has a uuid that `delete` hands back and `info`, `undelete` and `purge` accept. |

### The contract

**A missing verb and a missing feature mean the same thing: treat this saferm as absent.** A consumer probes `capabilities`, reads the feature it needs, and if the verb is unknown or the name is not in the list it takes the same path it takes when saferm is not installed at all -- one code path, not two.

**Feature names, never version numbers.** A locally built saferm reports a Go pseudo-version no semver parser accepts, so a version comparison rejects a perfectly good install; and a version says nothing about what a build actually carries. Nothing in the negotiation surface is a number.

Adding a name is how a new capability becomes negotiable. Removing one is a breaking change to every caller that asks for it.

## Where the declaration lives

The payload schemas are not documented here and implemented somewhere else -- they are the same artifact. Each verb declares its schema as an inline JSON Schema literal over the framework's closed subset, and the framework validates the emitted value against that declaration at the point it writes the envelope. A payload that deviates from its declaration fails the run rather than shipping a wrong shape.

That declaration is published on exactly one machine-readable channel, framework-owned: **`saferm --dump-schema`** writes `.strictcli/schema.json`, which carries every command with its flags, its args, its effect classification and its `payload_schema` verbatim. There is no second convention -- a consumer that wants the machine-readable contract reads that file, and this page is the prose that explains what the fields mean.

**The MCP server** (`saferm --mcp`) is not a second copy of it. It publishes the same commands as tools, and a tool descriptor carries the command's name, its help text, its `effect` (plus `consequential` where it applies) and the `inputSchema` for its arguments -- what saferm accepts and what calling it does to the world. No payload schema appears there: an MCP client learns nothing about the shape of the answer from the descriptor, and reads `--dump-schema` for that.

## Effect classification

Every command declares what it does to the world, and the machine surface documents the same classification the framework enforces.

| Command | Effect | Prompts for consent |
|---------|--------|---------------------|
| `delete` | mutating | no |
| `undelete` | mutating | no |
| `list` | read_only | no |
| `info` | read_only | no |
| `capabilities` | read_only | no |
| `purge` | mutating | yes -- it is the one irreversible operation |

`read_only` is enforced, not merely asserted: the framework refuses a mutation attempted from a read_only command, which is why a `capabilities` probe cannot create state on the machine it is probing.

A non-interactive purge therefore needs `saferm --approve-consequential purge --all`; every other verb runs bare, with no approval flag, which is what makes them safe to call from a script.
