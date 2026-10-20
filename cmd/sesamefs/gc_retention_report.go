package main

import (
	"encoding/json"
	"errors"
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
// is deliberately not called because it would write logs to stdout.
//
// Exit status: 0 every library in scope was measured completely; 1 usage,
// configuration, connection or output error (the report is unusable); 2 the
// enumeration failed, at least one library is incomplete, or an explicit
// -library/-org matched no library.
func runGCMetadataRetentionReport() {
	os.Exit(gcMetadataRetentionReport(os.Args[2:]))
}

// parseGCMetadataRetentionArgs turns the command line into a scope. ok is false
// when the command must exit now with exit (0 for -h, 1 for a usage error); it
// touches no configuration or database.
func parseGCMetadataRetentionArgs(args []string) (scope gc.MetadataRetentionScope, exit int, ok bool) {
	flags := flag.NewFlagSet("gc-metadata-retention-report", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	libraryFlag := flags.String("library", "", "measure only this library ID")
	orgFlag := flags.String("org", "", "measure only libraries of this org ID (the all-zero UUID is the platform org)")
	maxLibraries := flags.Int("max-libraries", 0, "stop after this many libraries are measured or incomplete (0 = no limit)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return scope, 0, false
		}
		return scope, 1, false
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments %q: use -library or -org\n", flags.Args())
		return scope, 1, false
	}
	if *libraryFlag != "" {
		id, err := uuid.Parse(*libraryFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid -library %q: %v\n", *libraryFlag, err)
			return scope, 1, false
		}
		// The scope reads uuid.Nil as "any library", and no library has that id:
		// accepting it would silently turn a one-library query into a full scan.
		if id == uuid.Nil {
			fmt.Fprintln(os.Stderr, "invalid -library: the all-zero UUID is not a library id")
			return scope, 1, false
		}
		scope.LibraryID = id
	}
	if *orgFlag != "" {
		id, err := uuid.Parse(*orgFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid -org %q: %v\n", *orgFlag, err)
			return scope, 1, false
		}
		// The all-zero UUID is the platform org, a real org: keep it as a filter.
		scope.OrgID, scope.OrgSet = id, true
	}
	if *maxLibraries < 0 {
		fmt.Fprintln(os.Stderr, "-max-libraries must be >= 0")
		return scope, 1, false
	}
	scope.MaxLibraries = *maxLibraries
	return scope, 0, true
}

func gcMetadataRetentionReport(args []string) int {
	scope, exit, ok := parseGCMetadataRetentionArgs(args)
	if !ok {
		return exit
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load configuration: %v\n", err)
		return 1
	}
	database, err := db.New(cfg.Database)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect to database: %v\n", err)
		return 1
	}
	defer database.Close()

	out := json.NewEncoder(os.Stdout)
	var writeErr error
	// Stop measuring at the first failed write: the rest would be discarded.
	summary, err := gc.CollectMetadataRetention(gc.NewCassandraStore(database), scope, func(r gc.LibraryMetadataRetention) error {
		if encErr := out.Encode(r); encErr != nil {
			writeErr = encErr
			return encErr
		}
		return nil
	})
	if writeErr == nil {
		writeErr = out.Encode(summary)
	}
	switch {
	case writeErr != nil:
		fmt.Fprintf(os.Stderr, "write report: %v\n", writeErr)
		return 1
	case err != nil:
		fmt.Fprintf(os.Stderr, "retention report: %v\n", err)
		return 2
	case summary.LibrariesIncomplete > 0:
		return 2
	case (scope.LibraryID != uuid.Nil || scope.OrgSet) && summary.LibrariesListed == 0:
		// A mistyped ID must not read as a complete measurement of zero retention.
		fmt.Fprintln(os.Stderr, "no library matched -library/-org")
		return 2
	}
	return 0
}
