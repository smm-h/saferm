package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/smm-h/saferm/internal/config"
	"github.com/smm-h/saferm/internal/db"
	"github.com/smm-h/strictcli/go/strictcli"
)

func registerListCmd(app *strictcli.App) {
	app.Command("list", "List archived items", handleList,
		strictcli.WithFlags(
			strictcli.StringFlag("path", "Filter by path glob pattern", strictcli.Default("")),
			strictcli.BoolFlag("all", "Include already-restored items"),
		),
	)
}

func handleList(kwargs map[string]interface{}) int {
	pathGlob := kwargs["path"].(string)
	includeAll := kwargs["all"].(bool)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading config: %s\n", err)
		return ExitGeneral
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening database: %s\n", err)
		return ExitDatabase
	}
	defer database.Close()

	records, err := database.QueryAll(includeAll)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: querying database: %s\n", err)
		return ExitDatabase
	}

	// Filter by glob if specified
	if pathGlob != "" {
		var filtered []*db.DeletionRecord
		for _, rec := range records {
			matched, err := filepath.Match(pathGlob, rec.OriginalPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: invalid glob pattern %q: %s\n", pathGlob, err)
				return ExitUsage
			}
			if matched {
				filtered = append(filtered, rec)
			}
		}
		records = filtered
	}

	if len(records) == 0 {
		fmt.Println("No archived items found.")
		return ExitSuccess
	}

	fmt.Printf("%-6s %-40s %-10s %-16s %s\n", "ID", "Path", "Size", "Age", "Status")
	fmt.Printf("%-6s %-40s %-10s %-16s %s\n", "------", "----------------------------------------", "----------", "----------------", "--------")

	for _, rec := range records {
		status := "archived"
		if rec.RestoredAt != nil {
			status = "restored"
		}

		path := rec.OriginalPath
		if len(path) > 40 {
			path = "..." + path[len(path)-37:]
		}

		fmt.Printf("%-6d %-40s %-10s %-16s %s\n",
			rec.ID,
			path,
			humanSize(rec.Size),
			humanAge(rec.DeletedAt),
			status,
		)
	}

	return ExitSuccess
}
