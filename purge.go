package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/smm-h/saferm/internal/db"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerPurgeCmd(app *strictcli.App) {
	app.Command("purge", "Permanently remove items from archive", handlePurge,
		strictcli.WithFlags(
			strictcli.StringFlag("older-than", "Purge items older than duration (e.g., 30d, 24h, 1w)", strictcli.Default("")),
			strictcli.BoolFlag("force", "Skip confirmation prompt", strictcli.Short("f")),
			strictcli.BoolFlag("all", "Purge everything"),
		),
		strictcli.WithArgs(
			strictcli.NewArg("ids", "Specific IDs to purge",
				strictcli.Variadic(), strictcli.ArgRequired(false)),
		),
	)
}

func handlePurge(kwargs map[string]interface{}) int {
	olderThan := kwargs["older_than"].(string)
	force := kwargs["force"].(bool)
	purgeAll := kwargs["all"].(bool)
	idsRaw := kwargs["ids"].([]interface{})
	verbose, _ := kwargs["verbose"].(bool)

	hasIDs := len(idsRaw) > 0
	hasOlderThan := olderThan != ""

	// Must specify at least one selection method
	if !hasIDs && !hasOlderThan && !purgeAll {
		fmt.Fprintln(os.Stderr, "error: specify IDs, --older-than, or --all")
		return ExitUsage
	}

	archiveDir := kwargs["archive_dir"].(string)
	dbPath := kwargs["db_path"].(string)

	if err := ensureDirectories(baseDir(), archiveDir, dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: creating directories: %s\n", err)
		return ExitGeneral
	}

	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening database: %s\n", err)
		return ExitDatabase
	}
	defer database.Close()

	var records []*db.DeletionRecord

	if hasIDs {
		for _, raw := range idsRaw {
			idStr := raw.(string)
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %q is not a valid ID\n", idStr)
				return ExitUsage
			}
			rec, err := database.QueryByID(id)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: no record with ID %d\n", id)
				return ExitFileNotFound
			}
			records = append(records, rec)
		}
	} else if hasOlderThan {
		dur, err := parseDuration(olderThan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			return ExitUsage
		}
		before := time.Now().Add(-dur)
		records, err = database.QueryOlderThan(before)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
			return ExitDatabase
		}
	} else {
		// --all
		records, err = database.QueryAll(true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
			return ExitDatabase
		}
	}

	if len(records) == 0 {
		fmt.Println("Nothing to purge.")
		return ExitSuccess
	}

	if !force {
		fmt.Fprintf(os.Stderr, "Will permanently delete %d item(s):\n", len(records))
		for _, rec := range records {
			fmt.Fprintf(os.Stderr, "  [%d] %s (%s, %s)\n",
				rec.ID, rec.OriginalPath, humanSize(rec.Size), humanAge(rec.DeletedAt))
		}
		fmt.Fprintf(os.Stderr, "Permanently delete %d items? [y/N] ", len(records))
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(scanner.Text())), "y") {
			fmt.Println("Aborted.")
			return ExitSuccess
		}
	}

	purged := 0
	for _, rec := range records {
		// Remove the archive file
		archivePath := filepath.Join(archiveDir, rec.UUID)
		if rec.SymlinkTarget != nil {
			archivePath += ".symlink"
		} else if rec.IsDirectory {
			archivePath += ".tar.zst"
		}
		if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: removing archive file %s: %s\n", archivePath, err)
		}

		// Delete the DB record
		if err := database.Delete(rec.ID); err != nil {
			fmt.Fprintf(os.Stderr, "error: deleting record %d: %s\n", rec.ID, err)
			continue
		}

		if verbose {
			fmt.Printf("purged: [%d] %s\n", rec.ID, rec.OriginalPath)
		}
		purged++
	}

	if !verbose {
		fmt.Printf("%d item(s) purged\n", purged)
	}

	return ExitSuccess
}
