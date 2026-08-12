package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/smm-h/saferm/internal/archive"
	"github.com/smm-h/saferm/internal/db"
	gitutil "github.com/smm-h/saferm/internal/git"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerUndeleteCmd(app *strictcli.App) {
	app.Command("undelete", "Restore a previously archived file back to its original path", handleUndelete,
		strictcli.WithEffect(strictcli.EffectMutating),
		strictcli.WithGrants(strictcli.Grant{
			Name:   "git-index",
			Reason: "a restored file is staged so the working tree and the index agree again",
			Kind:   strictcli.ProcMutate,
		}),
		strictcli.WithFlags(
			strictcli.BoolFlag("force-overwrite", "Overwrite any existing file at the restoration destination", strictcli.Default(false)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("target", "Numeric database ID or original file path of the item to restore"),
		),
	)
}

func handleUndelete(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
	forceOverwrite := kwargs["force_overwrite"].(bool)
	target := kwargs["target"].(string)

	archiveDir := kwargs["archive_dir"].(string)
	dbPath := kwargs["db_path"].(string)

	if err := ensureDirectories(ctx.Effects(), filepath.Dir(archiveDir), archiveDir, dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: creating directories: %s\n", err)
		return strictcli.Exit(ExitGeneral)
	}

	database, err := openArchiveDB(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening database: %s\n", err)
		return strictcli.Exit(dbExit(err))
	}
	// nil means the dry run found no archive at all, so there is nothing to
	// restore from and no record to name.
	if database == nil {
		fmt.Fprintf(os.Stderr, "error: no archived record found for %q\n", target)
		return strictcli.Exit(ExitFileNotFound)
	}
	defer database.Close()

	var rec *db.DeletionRecord

	// Try parsing as numeric ID first
	if id, parseErr := strconv.ParseInt(target, 10, 64); parseErr == nil {
		rec, err = database.QueryByID(id)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				fmt.Fprintf(os.Stderr, "error: no record with ID %d\n", id)
				return strictcli.Exit(ExitFileNotFound)
			}
			fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
			return strictcli.Exit(dbExit(err))
		}
	} else {
		// Treat as a file path
		records, err := database.QueryByPath(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
			return strictcli.Exit(dbExit(err))
		}
		if len(records) == 0 {
			fmt.Fprintf(os.Stderr, "error: no archived record found for path %q\n", target)
			return strictcli.Exit(ExitFileNotFound)
		}
		if len(records) > 1 {
			fmt.Fprintf(os.Stderr, "Multiple matches found:\n")
			fmt.Fprintf(os.Stderr, "  %-6s %-40s %-10s %s\n", "ID", "Path", "Size", "Deleted")
			for _, r := range records {
				fmt.Fprintf(os.Stderr, "  %-6d %-40s %-10s %s\n",
					r.ID, r.OriginalPath, humanSize(r.Size), humanAge(r.DeletedAt))
			}
			fmt.Fprintf(os.Stderr, "\nUse saferm undelete <id> to specify.\n")
			return strictcli.Exit(ExitUsage)
		}
		rec = records[0]
	}

	// Guard: cannot restore an already-restored item. A restore consumes the
	// archived entry -- it is moved out of the archive, not copied -- so a
	// second restore of the same record has nothing to read. Reported here, in
	// the record's own vocabulary, because both routes below answer badly: with
	// the destination gone the archive layer reports a raw failed stat of a
	// UUID, and with the destination still there the conflict path advertises
	// --force-overwrite, which could not have helped.
	if rec.RestoredAt != nil {
		restoredTo := rec.OriginalPath
		if rec.RestoredTo != nil {
			restoredTo = *rec.RestoredTo
		}
		fmt.Fprintf(os.Stderr, "error: record %d was already restored at %s to %s; the archived copy was consumed by that restore\n",
			rec.ID, rec.RestoredAt.Format(time.RFC3339), restoredTo)
		return strictcli.Exit(ExitArchive)
	}

	// Guard: cannot restore a purged item (archive content is gone).
	if rec.PurgedAt != nil {
		fmt.Fprintf(os.Stderr, "error: content for %d was purged on %s; metadata is preserved but the file cannot be restored\n",
			rec.ID, rec.PurgedAt.Format(time.RFC3339))
		return strictcli.Exit(ExitArchive)
	}

	dest := rec.OriginalPath

	symlinkTarget := ""
	if rec.SymlinkTarget != nil {
		symlinkTarget = *rec.SymlinkTarget
	}
	// A restore is the mirror of an archival and just as compound (extract a
	// tar+zstd tree, or move the entry back with a cross-device fallback), so
	// the same split applies: under --dry-run the move is recorded on the
	// handle and nothing is touched. See recordArchival in delete.go.
	if ctx.DryRun() {
		src := filepath.Join(archiveDir, rec.UUID)
		switch {
		case symlinkTarget != "":
			src += ".symlink"
		case rec.IsDirectory:
			src += ".tar.zst"
		}
		if _, err := ctx.Effects().Rename(src, dest, strictcli.Resource("path:"+dest)); err != nil {
			fmt.Fprintf(os.Stderr, "error: recording restore: %s\n", err)
			return strictcli.Exit(ExitArchive)
		}
		say(ctx, "Would restore %s\n", dest)
		return strictcli.Exit(ExitSuccess)
	}

	err = archive.Restore(rec.UUID, archiveDir, dest, rec.IsDirectory, forceOverwrite, symlinkTarget)
	if err != nil {
		if errors.Is(err, archive.ErrConflict) {
			fmt.Fprintf(os.Stderr, "error: %s already exists (use --force-overwrite to overwrite)\n", dest)
			return strictcli.Exit(ExitConflict)
		}
		fmt.Fprintf(os.Stderr, "error: restoring: %s\n", err)
		return strictcli.Exit(ExitArchive)
	}

	if err := database.MarkRestored(rec.ID, dest); err != nil {
		fmt.Fprintf(os.Stderr, "error: updating database: %s\n", err)
		return strictcli.Exit(dbExit(err))
	}

	// Stage the restored file in git if inside a git repo.
	destDir := filepath.Dir(dest)
	if gitutil.IsInGitRepo(destDir) {
		if err := gitutil.GitAdd(dest); err != nil {
			fmt.Fprintf(os.Stderr, "warning: git add failed for %s: %s\n", dest, err)
		}
	}

	say(ctx, "Restored %s\n", dest)
	return strictcli.Exit(ExitSuccess)
}
