package db

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

//go:embed migrations/*.cql
var migrationsFS embed.FS

// Migrator applies versioned CQL schema migrations to a Cassandra keyspace.
//
// Migration files live in internal/db/migrations/NNN_description.cql and are
// embedded into the binary at compile time via go:embed. Each applied migration
// is recorded in the schema_migrations table with a SHA-256 checksum. If a
// migration file is modified after it has been applied, the checksum mismatch
// is detected at startup and the server refuses to boot — preventing silent
// schema drift.
//
// # File naming
//
// Files must follow the NNN_description.cql convention where NNN is a
// zero-padded integer (e.g. 001, 042). The version number determines
// application order; gaps are allowed.
//
// # Idempotency
//
// All statements in 001_initial_schema.cql use CREATE TABLE IF NOT EXISTS and
// CREATE INDEX IF NOT EXISTS, making them safe to re-run on any database state.
// Any later incremental migrations may use ALTER TABLE or other non-idempotent
// statements and are only executed once — tracked via schema_migrations.
type Migrator struct {
	session *gocql.Session
}

// MigrationFile is a parsed migration file ready to apply.
type MigrationFile struct {
	Version    int
	Name       string   // human-readable portion, e.g. "initial_schema"
	Filename   string   // original filename, e.g. "001_initial_schema.cql"
	Content    string   // raw file content
	Checksum   string   // SHA-256 hex digest of Content
	Statements []string // individual CQL statements (comments stripped, split on ;)
}

// AppliedMigration is a row read from the schema_migrations table.
type AppliedMigration struct {
	Version   int
	Name      string
	Checksum  string
	AppliedAt time.Time
}

// MigrationStatus combines a migration file with its applied state.
type MigrationStatus struct {
	MigrationFile
	Applied   bool
	AppliedAt time.Time
}

// NewMigrator creates a Migrator backed by the given Cassandra session.
func NewMigrator(session *gocql.Session) *Migrator {
	return &Migrator{session: session}
}

// Run applies all pending migrations in version order.
//
// Checksum validation runs first: if any previously-applied migration file has
// been modified, Run returns an error before touching the database.
func (m *Migrator) Run() error {
	if err := m.ensureTable(); err != nil {
		return err
	}
	files, err := m.loadFiles()
	if err != nil {
		return err
	}
	applied, err := m.appliedMigrations()
	if err != nil {
		return err
	}

	if err := validateAppliedMigrationChecksums(files, applied); err != nil {
		return err
	}

	for _, mf := range files {
		if _, alreadyApplied := applied[mf.Version]; alreadyApplied {
			continue
		}

		if err := m.apply(mf); err != nil {
			return fmt.Errorf("migrator: applying %03d_%s: %w", mf.Version, mf.Name, err)
		}
		slog.Info("migrator: applied", "migration", mf.label())
	}

	return nil
}

// Status returns the applied state of every known migration file.
func (m *Migrator) Status() ([]MigrationStatus, error) {
	if err := m.ensureTable(); err != nil {
		return nil, err
	}
	files, err := m.loadFiles()
	if err != nil {
		return nil, err
	}
	applied, err := m.appliedMigrations()
	if err != nil {
		return nil, err
	}
	statuses := make([]MigrationStatus, len(files))
	for i, mf := range files {
		rec, ok := applied[mf.Version]
		statuses[i] = MigrationStatus{
			MigrationFile: mf,
			Applied:       ok,
			AppliedAt:     rec.AppliedAt,
		}
	}
	return statuses, nil
}

// DryRun returns the list of migrations that would be applied by Run,
// without executing or stamping anything.
func (m *Migrator) DryRun() ([]MigrationFile, error) {
	if err := m.ensureTable(); err != nil {
		return nil, err
	}
	files, err := m.loadFiles()
	if err != nil {
		return nil, err
	}
	applied, err := m.appliedMigrations()
	if err != nil {
		return nil, err
	}
	var pending []MigrationFile
	for _, mf := range files {
		if _, ok := applied[mf.Version]; !ok {
			pending = append(pending, mf)
		}
	}
	return pending, nil
}

// Check returns a non-nil error if any migrations are pending OR if any
// previously-applied migration file has been modified since application.
// Intended for CI pipelines: exit non-zero whenever the binary and database
// are out of sync in either direction.
func (m *Migrator) Check() error {
	if err := m.ensureTable(); err != nil {
		return err
	}
	files, err := m.loadFiles()
	if err != nil {
		return err
	}
	applied, err := m.appliedMigrations()
	if err != nil {
		return err
	}

	issues := migrationCheckIssues(files, applied)
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("schema check failed (%d issue(s)):\n  %s",
		len(issues), strings.Join(issues, "\n  "))
}

// ── internal helpers ─────────────────────────────────────────────────────────

// ensureTable creates the schema_migrations tracking table if it does not exist.
func (m *Migrator) ensureTable() error {
	return m.session.Query(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INT PRIMARY KEY,
			name       TEXT,
			applied_at TIMESTAMP,
			checksum   TEXT
		)`).Exec()
}

// loadFiles reads all embedded *.cql files, parses them and returns them sorted
// by version number. Duplicate version numbers cause an immediate error.
func (m *Migrator) loadFiles() ([]MigrationFile, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("reading embedded migrations directory: %w", err)
	}

	var files []MigrationFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".cql") {
			continue
		}
		version, name, err := parseMigrationFilename(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("invalid migration filename %q: %w", entry.Name(), err)
		}
		raw, err := fs.ReadFile(migrationsFS, "migrations/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("reading migration %s: %w", entry.Name(), err)
		}
		content := string(raw)
		files = append(files, MigrationFile{
			Version:    version,
			Name:       name,
			Filename:   entry.Name(),
			Content:    content,
			Checksum:   fmt.Sprintf("%x", sha256.Sum256(raw)),
			Statements: parseCQLStatements(content),
		})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Version < files[j].Version })

	seen := make(map[int]string, len(files))
	for _, mf := range files {
		if prev, ok := seen[mf.Version]; ok {
			return nil, fmt.Errorf(
				"duplicate migration version %d: %q and %q — each file must have a unique version prefix",
				mf.Version, prev, mf.Filename,
			)
		}
		seen[mf.Version] = mf.Filename
	}

	return files, nil
}

// appliedMigrations reads schema_migrations and returns all records keyed by
// version number.
func (m *Migrator) appliedMigrations() (map[int]AppliedMigration, error) {
	iter := m.session.Query(
		`SELECT version, name, applied_at, checksum FROM schema_migrations`,
	).Iter()
	applied := make(map[int]AppliedMigration)
	var rec AppliedMigration
	for iter.Scan(&rec.Version, &rec.Name, &rec.AppliedAt, &rec.Checksum) {
		applied[rec.Version] = rec
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("reading schema_migrations: %w", err)
	}
	return applied, nil
}

// stamp records a migration in schema_migrations without executing its CQL.
func (m *Migrator) stamp(mf MigrationFile) error {
	return m.session.Query(
		`INSERT INTO schema_migrations (version, name, applied_at, checksum) VALUES (?, ?, ?, ?)`,
		mf.Version, mf.Name, time.Now().UTC(), mf.Checksum,
	).Exec()
}

// apply executes the migration's CQL statements one by one, then stamps it.
// If a statement fails the migration is not stamped so it will be retried on
// the next startup.
func (m *Migrator) apply(mf MigrationFile) error {
	for i, stmt := range mf.Statements {
		if err := m.session.Query(stmt).Exec(); err != nil {
			return fmt.Errorf("statement %d/%d failed: %w\nCQL: %.300s",
				i+1, len(mf.Statements), err, stmt)
		}
	}
	return m.stamp(mf)
}

// label returns the human-readable label used in log messages.
func (mf MigrationFile) label() string {
	return fmt.Sprintf("%03d_%s", mf.Version, mf.Name)
}

func migrationCheckIssues(files []MigrationFile, applied map[int]AppliedMigration) []string {
	issues := make([]string, 0, len(files))
	for _, mf := range files {
		rec, ok := applied[mf.Version]
		if !ok {
			issues = append(issues, fmt.Sprintf("pending:  %s", mf.label()))
			continue
		}
		if rec.Checksum != mf.Checksum {
			issues = append(issues, fmt.Sprintf("modified: %s (recorded=%s… file=%s…)",
				mf.label(), checksumPrefix(rec.Checksum), checksumPrefix(mf.Checksum)))
		}
	}
	return issues
}

func validateAppliedMigrationChecksums(files []MigrationFile, applied map[int]AppliedMigration) error {
	for _, mf := range files {
		rec, alreadyApplied := applied[mf.Version]
		if !alreadyApplied {
			continue
		}
		if rec.Checksum != mf.Checksum {
			return fmt.Errorf(
				"migration %03d (%s): checksum mismatch — the file was modified after it was applied "+
					"(recorded=%s file=%s). "+
					"Do not edit migration files after application; create a new numbered migration instead.",
				mf.Version, mf.Name, rec.Checksum, mf.Checksum,
			)
		}
	}
	return nil
}

func checksumPrefix(checksum string) string {
	if len(checksum) <= 8 {
		return checksum
	}
	return checksum[:8]
}

// parseMigrationFilename parses "042_add_foo_column.cql" into (42, "add_foo_column").
func parseMigrationFilename(filename string) (version int, name string, err error) {
	base := strings.TrimSuffix(filename, ".cql")
	idx := strings.IndexByte(base, '_')
	if idx < 1 {
		return 0, "", fmt.Errorf("expected NNN_name.cql format, got %q", filename)
	}
	v, err := strconv.Atoi(base[:idx])
	if err != nil {
		return 0, "", fmt.Errorf("version prefix %q is not an integer: %w", base[:idx], err)
	}
	n := base[idx+1:]
	if n == "" {
		return 0, "", fmt.Errorf("name part is empty in %q", filename)
	}
	return v, n, nil
}

// parseCQLStatements splits CQL file content into individual executable
// statements. Line comments (-- …) are stripped before splitting on semicolons.
// Empty and whitespace-only pieces are discarded.
func parseCQLStatements(content string) []string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	cleaned := strings.Join(lines, "\n")

	var stmts []string
	for _, raw := range strings.Split(cleaned, ";") {
		if s := strings.TrimSpace(raw); s != "" {
			stmts = append(stmts, s)
		}
	}
	return stmts
}
