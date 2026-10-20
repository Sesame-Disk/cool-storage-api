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

func gcMetadataRetentionReport(args []string) int {
	flags := flag.NewFlagSet("gc-metadata-retention-report", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	libraryFlag := flags.String("library", "", "measure only this library ID")
	orgFlag := flags.String("org", "", "measure only libraries of this org ID")
	maxLibraries := flags.Int("max-libraries", 0, "stop after this many libraries are measured or incomplete (0 = no limit)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments %q: use -library or -org\n", flags.Args())
		return 1
	}

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
			return 1
		}
		*f.dst = id
	}
	if *maxLibraries < 0 {
		fmt.Fprintln(os.Stderr, "-max-libraries must be >= 0")
		return 1
	}
	scope.MaxLibraries = *maxLibraries

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
	case (scope.LibraryID != uuid.Nil || scope.OrgID != uuid.Nil) && summary.LibrariesListed == 0:
		// A mistyped ID must not read as a complete measurement of zero retention.
		fmt.Fprintln(os.Stderr, "no library matched -library/-org")
		return 2
	}
	return 0
}
