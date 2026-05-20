package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/smm-h/saferm/internal/archive"
	"github.com/smm-h/saferm/internal/config"
	"github.com/smm-h/saferm/internal/db"
	gitutil "github.com/smm-h/saferm/internal/git"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerUndeleteCmd(app *strictcli.App) {
	app.Command("undelete", "Restore a file from archive", handleUndelete,
		strictcli.WithFlags(
			strictcli.BoolFlag("force", "Overwrite existing file at destination"),
		),
		strictcli.WithArgs(
			strictcli.NewArg("target", "Numeric ID or original file path to restore"),
		),
	)
}

func handleUndelete(kwargs map[string]interface{}) int {
	force := kwargs["force"].(bool)
	target := kwargs["target"].(string)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading config: %s\n", err)
		return ExitGeneral
	}

	if err := config.EnsureDirectories(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: creating directories: %s\n", err)
		return ExitGeneral
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening database: %s\n", err)
		return ExitDatabase
	}
	defer database.Close()

	var rec *db.DeletionRecord

	// Try parsing as numeric ID first
	if id, parseErr := strconv.ParseInt(target, 10, 64); parseErr == nil {
		rec, err = database.QueryByID(id)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				fmt.Fprintf(os.Stderr, "error: no record with ID %d\n", id)
				return ExitFileNotFound
			}
			fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
			return ExitDatabase
		}
	} else {
		// Treat as a file path
		records, err := database.QueryByPath(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
			return ExitDatabase
		}
		if len(records) == 0 {
			fmt.Fprintf(os.Stderr, "error: no archived record found for path %q\n", target)
			return ExitFileNotFound
		}
		if len(records) > 1 {
			fmt.Fprintf(os.Stderr, "Multiple matches found:\n")
			fmt.Fprintf(os.Stderr, "  %-6s %-40s %-10s %s\n", "ID", "Path", "Size", "Deleted")
			for _, r := range records {
				fmt.Fprintf(os.Stderr, "  %-6d %-40s %-10s %s\n",
					r.ID, r.OriginalPath, humanSize(r.Size), humanAge(r.DeletedAt))
			}
			fmt.Fprintf(os.Stderr, "\nUse saferm undelete <id> to specify.\n")
			return ExitUsage
		}
		rec = records[0]
	}

	dest := rec.OriginalPath

	err = archive.Restore(rec.UUID, cfg.ArchiveDir, dest, rec.IsDirectory, force)
	if err != nil {
		if errors.Is(err, archive.ErrConflict) {
			fmt.Fprintf(os.Stderr, "error: %s already exists (use --force to overwrite)\n", dest)
			return ExitConflict
		}
		fmt.Fprintf(os.Stderr, "error: restoring: %s\n", err)
		return ExitArchive
	}

	if err := database.MarkRestored(rec.ID, dest); err != nil {
		fmt.Fprintf(os.Stderr, "error: updating database: %s\n", err)
		return ExitDatabase
	}

	// Stage the restored file in git if inside a git repo.
	destDir := filepath.Dir(dest)
	if gitutil.IsInGitRepo(destDir) {
		if err := gitutil.GitAdd(dest); err != nil {
			fmt.Fprintf(os.Stderr, "warning: git add failed for %s: %s\n", dest, err)
		}
	}

	fmt.Printf("Restored %s\n", dest)
	return ExitSuccess
}
