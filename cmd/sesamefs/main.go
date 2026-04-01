package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/Sesame-Disk/sesamefs/internal/api"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/logging"
	"github.com/joho/godotenv"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		// Will be logged properly after logging.Setup, but that requires config first.
		// Use fmt here since slog isn't configured yet.
		fmt.Println("No .env file found, using environment variables")
	}

	// Parse command
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "serve")
	}

	command := os.Args[1]

	switch command {
	case "serve":
		runServer()
	case "health":
		runHealthCheck()
	case "migrate":
		runMigrations()
	case "version":
		printVersion()
	case "backfill-search-index":
		runBackfillSearchIndex()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		fmt.Println("Available commands: serve, health, migrate, version, backfill-search-index")
		os.Exit(1)
	}
}

func runServer() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Set up structured logging (must happen early)
	logging.Setup(cfg.Auth.DevMode)

	// Initialize database connection
	database, err := db.New(cfg.Database)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Run database migrations (idempotent)
	slog.Info("Running database migrations...")
	if err := database.Migrate(); err != nil {
		slog.Error("Migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Migrations completed successfully")

	// Seed database with default data (idempotent)
	slog.Info("Checking database seed status...")
	if err := database.SeedDatabase(cfg, cfg.Auth.DevMode, cfg.Auth.FirstSuperAdminEmail); err != nil {
		slog.Error("Failed to seed database", "error", err)
		os.Exit(1)
	}

	// Create and start the API server
	server := api.NewServer(cfg, database, Version)

	slog.Info("SesameFS starting", "version", Version, "port", cfg.Server.Port)
	if err := server.Run(); err != nil {
		slog.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

func runHealthCheck() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("UNHEALTHY: Failed to load config")
		os.Exit(1)
	}

	// TODO: Check database connection
	// TODO: Check storage connection

	fmt.Printf("HEALTHY: SesameFS on port %s\n", cfg.Server.Port)
	os.Exit(0)
}

func runMigrations() {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Print pending migrations without applying them")
	check := fs.Bool("check", false, "Exit non-zero if any migrations are pending (for CI)")
	status := fs.Bool("status", false, "Print the applied/pending status of all migrations")
	fs.Parse(os.Args[2:]) //nolint:errcheck // ExitOnError handles errors

	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	logging.Setup(cfg.Auth.DevMode)

	database, err := db.New(cfg.Database)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	migrator := db.NewMigrator(database.Session())

	switch {
	case *status:
		statuses, err := migrator.Status()
		if err != nil {
			slog.Error("Failed to read migration status", "error", err)
			os.Exit(1)
		}
		fmt.Printf("%-6s %-40s %-26s %s\n", "VER", "NAME", "APPLIED AT", "CHECKSUM")
		fmt.Println("──────────────────────────────────────────────────────────────────────────────────────")
		for _, s := range statuses {
			applied := "pending"
			appliedAt := ""
			if s.Applied {
				applied = "applied"
				appliedAt = s.AppliedAt.Format("2006-01-02 15:04:05 UTC")
			}
			fmt.Printf("%-6d %-40s %-26s %s  [%s]\n",
				s.Version, s.Name, appliedAt, s.Checksum[:12]+"…", applied)
		}

	case *dryRun:
		pending, err := migrator.DryRun()
		if err != nil {
			slog.Error("Failed to compute pending migrations", "error", err)
			os.Exit(1)
		}
		if len(pending) == 0 {
			fmt.Println("No pending migrations.")
			return
		}
		fmt.Printf("%d pending migration(s):\n", len(pending))
		for _, mf := range pending {
			fmt.Printf("  %03d_%s\n", mf.Version, mf.Name)
		}

	case *check:
		if err := migrator.Check(); err != nil {
			fmt.Fprintln(os.Stderr, "schema check failed:", err)
			os.Exit(1)
		}
		fmt.Println("Schema is up to date.")

	default:
		slog.Info("Running database migrations...")
		if err := database.Migrate(); err != nil {
			slog.Error("Migration failed", "error", err)
			os.Exit(1)
		}
		slog.Info("Migrations completed successfully")

		slog.Info("Seeding database with default data...")
		if err := database.SeedDatabase(cfg, cfg.Auth.DevMode, cfg.Auth.FirstSuperAdminEmail); err != nil {
			slog.Error("Failed to seed database", "error", err)
			os.Exit(1)
		}
	}
}

func printVersion() {
	fmt.Printf("SesameFS %s\n", Version)
	fmt.Printf("  Build Time: %s\n", BuildTime)
	fmt.Printf("  Git Commit: %s\n", GitCommit)
}

// runBackfillSearchIndex populates obj_name and full_path fields for all fs_objects
// by traversing the directory tree from root for each library.
func runBackfillSearchIndex() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	logging.Setup(cfg.Auth.DevMode)

	database, err := db.New(cfg.Database)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	slog.Info("Starting search index backfill (with full paths)...")

	// FSEntry represents a directory entry
	type FSEntry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Mode int    `json:"mode"`
	}

	// Get all libraries with their head_commit_id
	type LibInfo struct {
		ID           string
		HeadCommitID string
	}
	libIter := database.Session().Query("SELECT library_id, head_commit_id FROM libraries").Iter()
	var libraries []LibInfo
	var libID, headCommit string
	for libIter.Scan(&libID, &headCommit) {
		if headCommit != "" {
			libraries = append(libraries, LibInfo{ID: libID, HeadCommitID: headCommit})
		}
	}
	libIter.Close()

	slog.Info("Found libraries to process", "count", len(libraries))

	var totalUpdated int
	var totalLibraries int

	for _, lib := range libraries {
		libraryID := lib.ID

		// Get root_fs_id from the HEAD commit
		var rootFsID string
		err := database.Session().Query(`
			SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
		`, libraryID, lib.HeadCommitID).Scan(&rootFsID)
		if err != nil || rootFsID == "" {
			continue
		}

		totalLibraries++

		// Recursive function to traverse directory tree
		var traverseDir func(fsID, parentPath string) int
		traverseDir = func(fsID, parentPath string) int {
			updated := 0

			// Get directory entries
			var dirEntries string
			err := database.Session().Query(`
				SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
			`, libraryID, fsID).Scan(&dirEntries)
			if err != nil || dirEntries == "" || dirEntries == "[]" {
				return 0
			}

			// Parse entries
			var content struct {
				Dirents []FSEntry `json:"dirents"`
			}
			if err := json.Unmarshal([]byte(dirEntries), &content); err != nil {
				var entries []FSEntry
				if err := json.Unmarshal([]byte(dirEntries), &entries); err != nil {
					return 0
				}
				content.Dirents = entries
			}

			// Update each child
			for _, entry := range content.Dirents {
				if entry.Name == "" || entry.ID == "" {
					continue
				}

				// Compute full path
				var fullPath string
				if parentPath == "/" {
					fullPath = "/" + entry.Name
				} else {
					fullPath = parentPath + "/" + entry.Name
				}

				// Update obj_name and full_path
				err := database.Session().Query(`
					UPDATE fs_objects SET obj_name = ?, full_path = ? WHERE library_id = ? AND fs_id = ?
				`, entry.Name, fullPath, libraryID, entry.ID).Exec()
				if err == nil {
					updated++
				}

				// If this is a directory (mode 16384 = directory), recurse
				if entry.Mode == 16384 {
					updated += traverseDir(entry.ID, fullPath)
				}
			}

			return updated
		}

		// Start traversal from root
		updated := traverseDir(rootFsID, "/")
		totalUpdated += updated

		if totalLibraries%100 == 0 {
			slog.Info("Progress", "libraries_processed", totalLibraries, "paths_updated", totalUpdated)
		}
	}

	slog.Info("Search index backfill complete",
		"libraries_processed", totalLibraries,
		"paths_updated", totalUpdated)
}
