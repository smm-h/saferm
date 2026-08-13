package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/smm-h/saferm/internal/db"
	"github.com/smm-h/saferm/internal/meta"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerInfoCmd(app *strictcli.App) {
	app.Command("info", "Display full metadata and context for an archived deletion", handleInfo,
		strictcli.WithEffect(strictcli.EffectReadOnly),
		strictcli.WithArgs(
			strictcli.NewArg("target", "Record UUID or numeric database ID of the archived item to inspect ("+identifierOrderHelp+")"),
		),
	)
}

// recordStatus states, in one line, whether the archived content is still
// there to restore.
//
// The two lifecycle columns answer it wherever they are set: a restore moves
// the blob out and stamps restored_at, a purge destroys it and stamps
// purged_at. Both can be set -- a record restored first and purged afterwards
// -- and then both are reported, because either alone would hide half of what
// happened.
//
// Where neither is set the columns are no longer the whole answer, and the
// archive directory is consulted for the one remaining case. An archival that
// meets a changed source inside its window commits its row and DISCARDS its
// entry on purpose -- a file written through while its archive entry was a
// hard link to it, a tree that grew during the insert -- so a row that names
// nothing is a state saferm produces itself, not a corruption. Answering
// "restorable" for it is the one answer that sends a caller into an undelete
// that cannot work.
func recordStatus(rec *db.DeletionRecord, archiveDir string) string {
	var parts []string
	if rec.RestoredAt != nil {
		parts = append(parts, "restored at "+rec.RestoredAt.Format(time.RFC3339))
	}
	if rec.PurgedAt != nil {
		parts = append(parts, "purged at "+rec.PurgedAt.Format(time.RFC3339))
	}
	if len(parts) == 0 {
		if archiveEntryIsGone(archiveDir, rec) {
			return "the archived copy is gone though nothing restored or purged it -- this row names nothing; purge it to clear it"
		}
		return "restorable"
	}
	return strings.Join(parts, ", ")
}

func handleInfo(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
	target := kwargs["target"].(string)

	// Shape before archive: a path is refused whether or not this machine has
	// ever deleted anything.
	if code := requireIdentifierShape(target); code != ExitSuccess {
		return strictcli.Exit(code)
	}

	archiveDir := kwargs["archive_dir"].(string)
	dbPath := kwargs["db_path"].(string)

	database, err := openArchiveDBIfPresent(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening database: %s\n", err)
		return strictcli.Exit(dbExit(err))
	}
	// No database file means nothing has ever been deleted on this machine, so
	// no identifier resolves -- the same answer as one that was never issued.
	if database == nil {
		reportNoSuchRecord(target)
		return strictcli.Exit(ExitFileNotFound)
	}
	defer database.Close()

	// Identifier only: `info` inspects a record, and a path can name several.
	rec, code := resolveRecord(database, target, false)
	if code != ExitSuccess {
		return strictcli.Exit(code)
	}

	fileType := "file"
	if rec.SymlinkTarget != nil {
		fileType = "symlink"
	} else if rec.IsDirectory {
		fileType = "directory"
	}

	// The whole record is built first and emitted once, so machine mode carries
	// it as a single diagnostic rather than one per field.
	var out strings.Builder
	fmt.Fprintf(&out, "ID:            %d\n", rec.ID)
	fmt.Fprintf(&out, "UUID:          %s\n", rec.UUID)
	fmt.Fprintf(&out, "Original Path: %s\n", rec.OriginalPath)
	fmt.Fprintf(&out, "Original Name: %s\n", rec.OriginalName)
	fmt.Fprintf(&out, "Size:          %s (%d bytes)\n", humanSize(rec.Size), rec.Size)
	fmt.Fprintf(&out, "Hash:          %s\n", rec.Hash)
	fmt.Fprintf(&out, "Type:          %s\n", fileType)
	if rec.SymlinkTarget != nil {
		fmt.Fprintf(&out, "Target:        %s\n", *rec.SymlinkTarget)
	}
	fmt.Fprintf(&out, "Deleted At:    %s\n", rec.DeletedAt.Format(time.RFC3339))
	fmt.Fprintf(&out, "Status:        %s\n", recordStatus(rec, archiveDir))
	fmt.Fprintf(&out, "Description:   %s\n", rec.Description)
	if rec.Command != "" {
		fmt.Fprintf(&out, "Command:       %s\n", rec.Command)
	}

	if rec.RestoredAt != nil {
		fmt.Fprintf(&out, "Restored At:   %s\n", rec.RestoredAt.Format(time.RFC3339))
	}
	if rec.RestoredTo != nil {
		fmt.Fprintf(&out, "Restored To:   %s\n", *rec.RestoredTo)
	}
	if rec.PurgedAt != nil {
		fmt.Fprintf(&out, "Purged At:     %s\n", rec.PurgedAt.Format(time.RFC3339))
	}

	// Parse and display metadata
	if rec.Metadata != "" {
		var m meta.Metadata
		if err := json.Unmarshal([]byte(rec.Metadata), &m); err == nil {
			fmt.Fprintln(&out, "\nMetadata:")
			if m.GitBranch != "" {
				fmt.Fprintf(&out, "  Git Branch:  %s\n", m.GitBranch)
			}
			if m.GitHEAD != "" {
				fmt.Fprintf(&out, "  Git HEAD:    %s\n", m.GitHEAD)
			}
			if m.GitRoot != "" {
				fmt.Fprintf(&out, "  Git Root:    %s\n", m.GitRoot)
			}
			if m.PPID != 0 {
				fmt.Fprintf(&out, "  Parent PID:  %d\n", m.PPID)
			}
			if m.ParentCmd != "" {
				fmt.Fprintf(&out, "  Parent Cmd:  %s\n", m.ParentCmd)
			}
			if len(m.Custom) > 0 {
				fmt.Fprintln(&out, "  Custom:")
				for k, v := range m.Custom {
					fmt.Fprintf(&out, "    %s = %s\n", k, v)
				}
			}
			if len(m.Env) > 0 {
				fmt.Fprintf(&out, "  Environment: %d variables captured\n", len(m.Env))
			}
		}
	}
	emit(ctx, "%s", out.String())

	return strictcli.Exit(ExitSuccess)
}
