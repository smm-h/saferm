package main

import (
	"fmt"
	"os"

	"github.com/smm-h/saferm/internal/db"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerListCmd(app *strictcli.App) {
	app.Command("list", "Show all items currently held in the saferm archive", handleList,
		strictcli.WithEffect(strictcli.EffectReadOnly),
		strictcli.WithFlags(
			strictcli.StringFlag("path", "Filter results to original paths matching the given glob pattern (* spans directory separators, so /home/m/* reaches any depth)", strictcli.Default("")),
			strictcli.BoolFlag("all", "Include items that have already been restored or purged", strictcli.Default(false)),
		),
	)
}

func handleList(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
	pathGlob := kwargs["path"].(string)
	includeAll := kwargs["all"].(bool)

	dbPath := kwargs["db_path"].(string)

	database, err := openArchiveDBIfPresent(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening database: %s\n", err)
		return strictcli.Exit(ExitDatabase)
	}
	// No database file means nothing has ever been deleted on this machine,
	// which is a list of length zero, not a failure.
	if database == nil {
		fmt.Println("No archived items found.")
		return strictcli.Exit(ExitSuccess)
	}
	defer database.Close()

	records, err := database.QueryAll(includeAll)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
		return strictcli.Exit(ExitDatabase)
	}

	// Filter by glob if specified
	if pathGlob != "" {
		var filtered []*db.DeletionRecord
		for _, rec := range records {
			matched, err := matchArchivePath(pathGlob, rec.OriginalPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: invalid glob pattern %q: %s\n", pathGlob, err)
				return strictcli.Exit(ExitUsage)
			}
			if matched {
				filtered = append(filtered, rec)
			}
		}
		records = filtered
	}

	if len(records) == 0 {
		fmt.Println("No archived items found.")
		return strictcli.Exit(ExitSuccess)
	}

	fmt.Printf("%-6s %-40s %-10s %-16s %s\n", "ID", "Path", "Size", "Age", "Status")
	fmt.Printf("%-6s %-40s %-10s %-16s %s\n", "------", "----------------------------------------", "----------", "----------------", "--------")

	for _, rec := range records {
		status := "archived"
		if rec.PurgedAt != nil {
			status = "purged"
		} else if rec.RestoredAt != nil {
			status = "restored"
		}

		path := rec.OriginalPath

		// Append type indicator for non-regular files.
		typeIndicator := ""
		if rec.SymlinkTarget != nil {
			typeIndicator = " [sym]"
		} else if rec.IsDirectory {
			typeIndicator = " [dir]"
		}

		if len(path)+len(typeIndicator) > 40 {
			maxPath := 40 - len(typeIndicator)
			path = "..." + path[len(path)-(maxPath-3):]
		}
		path += typeIndicator

		fmt.Printf("%-6d %-40s %-10s %-16s %s\n",
			rec.ID,
			path,
			humanSize(rec.Size),
			humanAge(rec.DeletedAt),
			status,
		)
	}

	return strictcli.Exit(ExitSuccess)
}
