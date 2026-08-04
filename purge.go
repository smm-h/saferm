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
	app.Command("purge", "Permanently destroy archived items and free disk space", handlePurge,
		strictcli.WithEffect(strictcli.EffectMutating),
		strictcli.WithGrants(strictcli.Grant{
			Name:   "purge",
			Reason: "purging destroys the archived content permanently; undelete cannot bring it back",
			Kind:   strictcli.FileWrite,
		}),
		strictcli.WithFlags(
			strictcli.StringFlag("older-than", "Purge items older than duration (e.g., 30d, 24h, 1w)", strictcli.Default("")),
			strictcli.StringFlag("larger-than", "Only purge items larger than this size (e.g. 100MB, 1GB)", strictcli.Default("")),
			strictcli.BoolFlag("skip-confirmation", "Skip the interactive confirmation prompt before purging", strictcli.Short("f"), strictcli.Default(false)),
			strictcli.BoolFlag("all", "Select all archived items for permanent destruction", strictcli.Default(false)),
		),
		strictcli.WithArgs(
			strictcli.NewArg("ids", "Numeric database IDs of specific items to permanently destroy",
				strictcli.Variadic(), strictcli.ArgRequired(false)),
		),
	)
}

func handlePurge(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
	olderThan := kwargs["older_than"].(string)
	largerThan := kwargs["larger_than"].(string)
	skipConfirmation := kwargs["skip_confirmation"].(bool)
	purgeAll := kwargs["all"].(bool)
	dryRun := ctx.DryRun()
	idsRaw := kwargs["ids"].([]interface{})
	verbose := ctx.Verbose()
	fx := ctx.Effects()

	hasIDs := len(idsRaw) > 0
	hasOlderThan := olderThan != ""
	hasLargerThan := largerThan != ""

	// Must specify at least one selection method.
	// --larger-than alone is valid (acts like --all --larger-than).
	if !hasIDs && !hasOlderThan && !purgeAll && !hasLargerThan {
		fmt.Fprintln(os.Stderr, "error: specify IDs, --older-than, --larger-than, or --all")
		return strictcli.Exit(ExitUsage)
	}

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

	var records []*db.DeletionRecord

	if hasIDs {
		for _, raw := range idsRaw {
			idStr := raw.(string)
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %q is not a valid ID\n", idStr)
				return strictcli.Exit(ExitUsage)
			}
			rec, err := database.QueryByID(id)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: no record with ID %d\n", id)
				return strictcli.Exit(ExitFileNotFound)
			}
			records = append(records, rec)
		}
	} else if hasOlderThan {
		dur, err := parseDuration(olderThan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			return strictcli.Exit(ExitUsage)
		}
		before := time.Now().Add(-dur)
		records, err = database.QueryOlderThan(before)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
			return strictcli.Exit(ExitDatabase)
		}
	} else {
		// --all or --larger-than alone (which implies all records)
		records, err = database.QueryAll(true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
			return strictcli.Exit(ExitDatabase)
		}
	}

	// Apply --larger-than filter if set.
	if hasLargerThan {
		threshold, err := parseSize(largerThan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			return strictcli.Exit(ExitUsage)
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
		return strictcli.Exit(ExitSuccess)
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
		// Fall through: the loop below mints each archive-file removal on the
		// effects handle, which records it under --dry-run instead of doing it,
		// so the would-do log names every file that would be destroyed.
	}

	if !dryRun && !skipConfirmation {
		fmt.Fprintf(os.Stderr, "Will permanently delete %d item(s):\n", len(records))
		for _, rec := range records {
			fmt.Fprintf(os.Stderr, "  [%d] %s (%s, %s)\n",
				rec.ID, rec.OriginalPath, humanSize(rec.Size), humanAge(rec.DeletedAt))
		}
		fmt.Fprintf(os.Stderr, "Permanently delete %d items? [y/N] ", len(records))
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(scanner.Text())), "y") {
			fmt.Println("Aborted.")
			return strictcli.Exit(ExitSuccess)
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
		// Minted on the handle: purging is the irreversible half of saferm, so
		// the destruction is declared (and, under --dry-run, only declared).
		if _, err := fx.Remove(archivePath,
			strictcli.UseGrant("purge"),
			strictcli.Resource("saferm-entry:"+rec.UUID),
		); err != nil {
			fmt.Fprintf(os.Stderr, "warning: removing archive file %s: %s\n", archivePath, err)
		}

		// Mark the record as purged (preserves metadata). The database is
		// SQLite and no member of the effects handle's closed method set can
		// describe a row update, so this mutation stays outside the handle and
		// is skipped in dry mode.
		if !dryRun {
			if err := database.MarkPurged(rec.ID); err != nil {
				fmt.Fprintf(os.Stderr, "error: marking record %d as purged: %s\n", rec.ID, err)
				continue
			}
		}

		if verbose && !dryRun {
			fmt.Printf("purged: [%d] %s\n", rec.ID, rec.OriginalPath)
		}
		purged++
	}

	if !verbose && !dryRun {
		fmt.Printf("%d item(s) purged\n", purged)
	}

	return strictcli.Exit(ExitSuccess)
}
