package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

// runGCMetadataRetentionReport prints a read-only measurement of fs_object
// retention in live libraries (docs/GC-LIVE-LIBRARY-RECLAMATION.md): one JSON
// line per library, then a summary line. Diagnostics go to stderr; logging.Setup
// is deliberately not called because it would write logs to stdout. Exit status
// 0 when every library was measured completely, 2 otherwise, 1 on usage or
// connection errors.
func runGCMetadataRetentionReport() {
	flags := flag.NewFlagSet("gc-metadata-retention-report", flag.ExitOnError)
	libraryFlag := flags.String("library", "", "measure only this library ID")
	orgFlag := flags.String("org", "", "measure only libraries of this org ID")
	maxLibraries := flags.Int("max-libraries", 0, "stop after measuring this many libraries (0 = no limit)")
	_ = flags.Parse(os.Args[2:])

	var scope gc.MetadataRetentionScope
	for _, f := range []struct {
		value string
		name  string
		dst   *uuid.UUID
	}{{*libraryFlag, "library", &scope.LibraryID}, {*orgFlag, "org", &scope.OrgID}} {
		if f.value == "" {
			continue
		}
		id, err := uuid.Parse(f.value)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid -%s %q: %v\n", f.name, f.value, err)
			os.Exit(1)
		}
		*f.dst = id
	}
	if *maxLibraries < 0 {
		fmt.Fprintln(os.Stderr, "-max-libraries must be >= 0")
		os.Exit(1)
	}
	scope.MaxLibraries = *maxLibraries

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load configuration: %v\n", err)
		os.Exit(1)
	}
	database, err := db.New(cfg.Database)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect to database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	out := json.NewEncoder(os.Stdout)
	summary, err := gc.CollectMetadataRetention(gc.NewCassandraStore(database), scope, func(r gc.LibraryMetadataRetention) {
		if encErr := out.Encode(r); encErr != nil {
			fmt.Fprintf(os.Stderr, "write report line: %v\n", encErr)
		}
	})
	if encErr := out.Encode(summary); encErr != nil {
		fmt.Fprintf(os.Stderr, "write summary: %v\n", encErr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "retention report: %v\n", err)
		database.Close()
		os.Exit(2)
	}
	if summary.LibrariesIncomplete > 0 {
		database.Close()
		os.Exit(2)
	}
}
