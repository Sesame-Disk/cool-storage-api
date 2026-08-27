package db

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
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

		if err := m.preflightDestructive(mf); err != nil {
			return err
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

// --- Destructive-migration preflight -----------------------------------------
//
// A migration that DROPs a table is not an upgrade: it discards whatever the
// table held. Migration 018 is exactly that — the exact-P GC identity model
// cannot be reached with ALTER, because Cassandra will not change a PRIMARY KEY
// — and it is correct ONLY under the contract it states: a clean keyspace, put
// into service before any node starts producing GC work.
//
// A comment cannot enforce that contract. This preflight can: before applying
// any migration that drops tables, every table it is about to drop must be
// empty. Running the release against a keyspace that already accumulated GC
// work then fails loudly at startup with the table named, instead of silently
// erasing queue, pending and DLQ rows — including the non-block item types that
// have nothing to do with this change.
//
// The check is deliberately generic rather than keyed to version 018, so the
// next destructive migration inherits the barrier instead of having to remember
// it.

var dropTablePattern = regexp.MustCompile(`(?is)\bDROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([A-Za-z0-9_."]+)`)

// droppedTables returns the tables a migration drops, in statement order.
//
// A keyspace-qualified name is returned AS WRITTEN, not stripped down to the bare
// table. The probe runs against the migrator's session, which is bound to one
// keyspace: silently discarding the qualifier would make it check
// `<session keyspace>.foo` while the DROP removes `otherks.foo`, so the barrier
// would report a different table empty than the one about to be destroyed. Passing
// the qualifier through keeps the probe and the drop pointed at the same table.
func (mf MigrationFile) droppedTables() []string {
	var tables []string
	for _, stmt := range mf.Statements {
		for _, match := range dropTablePattern.FindAllStringSubmatch(stmt, -1) {
			name := strings.Trim(strings.TrimSpace(match[1]), `";`)
			if name != "" {
				tables = append(tables, name)
			}
		}
	}
	return tables
}

// missingTableError reports whether err says the table is not there. A table
// this migration was going to drop anyway is not something to protect, so a
// re-run after a partially-applied destructive migration still converges.
func missingTableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"unconfigured table",
		"unconfigured columnfamily",
		"does not exist",
		"non-existent table",
		"cannot be found",
		"table not found",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// preflightDestructive refuses a table-dropping migration whose targets still
// hold rows.
func (m *Migrator) preflightDestructive(mf MigrationFile) error {
	tables := mf.droppedTables()
	if len(tables) == 0 {
		return nil
	}
	for _, table := range tables {
		// LIMIT 1 over the whole table: this only has to answer "is there anything
		// here", and it runs once, at startup, before the drop.
		//
		// NumRows, not Scan: the probe deliberately binds no columns, because it
		// must work for any schema, and Scan with no destinations is a driver error
		// rather than an empty result.
		//
		// EACH_QUORUM, NEVER THE SESSION DEFAULT. The session normally runs
		// LOCAL_QUORUM, and a local quorum is exactly the wrong instrument here: it
		// can answer "empty" while another datacenter holds rows that simply have
		// not been read locally, and the migration would then drop live work on the
		// strength of a local view. EACH_QUORUM obtains a quorum in EVERY
		// datacenter, so it intersects the quorum that acknowledged a write in
		// whichever DC accepted it — the same per-DC intersection argument the
		// destructive GC path rests on (see ValidateDestructiveGCTopology).
		//
		// It is deliberately NOT conditional on the replication class. Under a
		// strategy that gives EACH_QUORUM no per-DC meaning the engine either
		// resolves it to an ordinary quorum — still strictly stronger than
		// LOCAL_QUORUM, and sound where there is no second DC to miss — or rejects
		// it, and a rejection lands in the refusal below. Both outcomes are
		// fail-closed, so the probe does not have to depend on which one an engine
		// picks.
		iter := m.session.Query(fmt.Sprintf(`SELECT * FROM %s LIMIT 1`, table)).
			Consistency(gocql.EachQuorum).
			Iter()
		occupied := iter.NumRows() > 0
		err := iter.Close()
		if err != nil {
			if missingTableError(err) {
				continue
			}
			// A scan that cannot complete is NOT evidence of emptiness, so it
			// refuses like any other unverified table. Name the tombstone case
			// explicitly: these are high-churn tables, a drained one can still hold
			// hundreds of thousands of tombstones, and an operator who reads only
			// "could not be established" has no way to tell a dead datacenter from
			// a table that is empty but heavily deleted. TRUNCATE resolves both.
			hint := ""
			if tombstoneScanError(err) {
				hint = " The scan aborted on tombstones, which means this table was used and drained rather " +
					"than being unreachable: it may well be logically empty. That is still not proof, so it is " +
					"refused. TRUNCATE the GC tables to clear both rows and tombstones before starting the server."
			}
			return fmt.Errorf(
				"migrator: %s drops %s, and whether that table is empty could not be established: %w — "+
					"refusing to run a destructive migration on an unverified table%s",
				mf.label(), table, err, hint)
		}
		if occupied {
			return fmt.Errorf(
				"migrator: %s drops %s, but that table still holds rows.\n"+
					"This migration is NOT an upgrade — it discards the tables it drops — and is only "+
					"correct against a clean keyspace deployed before any node produces GC work.\n"+
					"Refusing to erase live state. Either deploy this release onto an empty keyspace, or "+
					"drain and truncate the GC tables deliberately before starting the server.",
				mf.label(), table)
		}
	}
	return nil
}

// PreflightDestructiveForTest runs the destructive preflight over a migration's
// CQL against an arbitrary session.
//
// It exists so the barrier can be proven against a REAL cluster: the whole check
// is a live read of a live table, so a unit test cannot show it closing. The
// integration test seeds a scratch table and asserts the refusal, which is the
// only way to demonstrate that deploying this release onto a keyspace with
// existing GC work fails loudly instead of erasing it.
//
// It is a test seam, not an API: production always reaches this through Run.
func PreflightDestructiveForTest(session *gocql.Session, migrationCQL string) error {
	m := &Migrator{session: session}
	return m.preflightDestructive(MigrationFile{
		Version:    0,
		Name:       "preflight_probe",
		Content:    migrationCQL,
		Statements: parseCQLStatements(migrationCQL),
	})
}

// tombstoneScanError reports whether a read aborted because it walked too many
// tombstones. The driver surfaces this as a plain read failure, so the wording is
// what distinguishes it.
func tombstoneScanError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "tombstone")
}
