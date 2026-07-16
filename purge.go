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
			strictcli.StringFlag("larger-than", "Only purge items larger than this size (e.g. 100MB, 1GB)", strictcli.Default("")),
			strictcli.BoolFlag("skip-confirmation", "Skip confirmation prompt", strictcli.Short("f"), strictcli.Default(false)),
			strictcli.BoolFlag("all", "Purge everything", strictcli.Default(false)),
			strictcli.BoolFlag("dry-run", "Show what would be purged without deleting", strictcli.Default(false)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("ids", "Specific IDs to purge",
				strictcli.Variadic(), strictcli.ArgRequired(false)),
		),
	)
}

func handlePurge(kwargs map[string]interface{}) int {
	olderThan := kwargs["older_than"].(string)
	largerThan := kwargs["larger_than"].(string)
	skipConfirmation := kwargs["skip_confirmation"].(bool)
	purgeAll := kwargs["all"].(bool)
	dryRun := kwargs["dry_run"].(bool)
	idsRaw := kwargs["ids"].([]interface{})
	verbose, _ := kwargs["verbose"].(bool)

	hasIDs := len(idsRaw) > 0
	hasOlderThan := olderThan != ""
	hasLargerThan := largerThan != ""

	// Must specify at least one selection method.
	// --larger-than alone is valid (acts like --all --larger-than).
	if !hasIDs && !hasOlderThan && !purgeAll && !hasLargerThan {
		fmt.Fprintln(os.Stderr, "error: specify IDs, --older-than, --larger-than, or --all")
		return ExitUsage
	}

	archiveDir := kwargs["archive_dir"].(string)
	dbPath := kwargs["db_path"].(string)

	if err := ensureDirectories(filepath.Dir(archiveDir), archiveDir, dbPath); err != nil {
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
		// --all or --larger-than alone (which implies all records)
		records, err = database.QueryAll(true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
			return ExitDatabase
		}
	}

	// Apply --larger-than filter if set.
	if hasLargerThan {
		threshold, err := parseSize(largerThan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			return ExitUsage
		}
		var filtered []*db.DeletionRecord
		for _, rec := range records {
			if rec.Size >= threshold {
				filtered = append(filtered, rec)
			}
		}
		records = filtered
	}

	if len(records) == 0 {
		fmt.Println("Nothing to purge.")
		return ExitSuccess
	}

	if dryRun {
		// Display what would be purged in tabular format.
		fmt.Printf("%-6s %-40s %-10s %-16s\n", "ID", "Path", "Size", "Age")
		fmt.Printf("%-6s %-40s %-10s %-16s\n", "------", "----------------------------------------", "----------", "----------------")
		var totalSize int64
		for _, rec := range records {
			path := rec.OriginalPath
			if len(path) > 40 {
				path = "..." + path[len(path)-37:]
			}
			fmt.Printf("%-6d %-40s %-10s %-16s\n",
				rec.ID, path, humanSize(rec.Size), humanAge(rec.DeletedAt))
			totalSize += rec.Size
		}
		fmt.Printf("\nWould purge %d item(s), freeing ~%s\n", len(records), humanSize(totalSize))
		return ExitSuccess
	}

	if !skipConfirmation {
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
		// Skip already-purged items.
		if rec.PurgedAt != nil {
			continue
		}

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

		// Mark the record as purged (preserves metadata).
		if err := database.MarkPurged(rec.ID); err != nil {
			fmt.Fprintf(os.Stderr, "error: marking record %d as purged: %s\n", rec.ID, err)
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
