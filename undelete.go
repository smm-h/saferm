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
	app.Command("undelete", "Restore a file from archive", handleUndelete,
		strictcli.WithFlags(
			strictcli.BoolFlag("force-overwrite", "Overwrite existing file at destination", strictcli.Default(false)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("target", "Numeric ID or original file path to restore"),
		),
	)
}

func handleUndelete(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
	forceOverwrite := kwargs["force_overwrite"].(bool)
	target := kwargs["target"].(string)

	archiveDir := kwargs["archive_dir"].(string)
	dbPath := kwargs["db_path"].(string)

	if err := ensureDirectories(filepath.Dir(archiveDir), archiveDir, dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: creating directories: %s\n", err)
		return strictcli.Exit(ExitGeneral)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening database: %s\n", err)
		return strictcli.Exit(ExitDatabase)
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
			return strictcli.Exit(ExitDatabase)
		}
	} else {
		// Treat as a file path
		records, err := database.QueryByPath(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
			return strictcli.Exit(ExitDatabase)
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
		return strictcli.Exit(ExitDatabase)
	}

	// Stage the restored file in git if inside a git repo.
	destDir := filepath.Dir(dest)
	if gitutil.IsInGitRepo(destDir) {
		if err := gitutil.GitAdd(dest); err != nil {
			fmt.Fprintf(os.Stderr, "warning: git add failed for %s: %s\n", dest, err)
		}
	}

	fmt.Printf("Restored %s\n", dest)
	return strictcli.Exit(ExitSuccess)
}
