//go:build integration

package integration

import (
	"testing"
)

func TestGC_R11bCanonicalOrphanSchemaOmitsRepresentationID(t *testing.T) {
	requireCassandra(t)

	keyspace := envOrDefault("CASSANDRA_KEYSPACE", "sesamefs")
	session := shareProjectionDBForTest(t).Session()
	iter := session.Query(`
		SELECT column_name
		FROM system_schema.columns
		WHERE keyspace_name = ? AND table_name = ?
	`, keyspace, "gc_s3_orphans").Iter()

	columns := map[string]bool{}
	var columnName string
	for iter.Scan(&columnName) {
		columns[columnName] = true
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("read effective gc_s3_orphans columns: %v", err)
	}
	if len(columns) == 0 {
		t.Fatal("gc_s3_orphans has no columns in system_schema; the gate would pass vacuously")
	}
	if columns["representation_id"] {
		t.Fatal("gc_s3_orphans still has representation_id after migration 015")
	}
	for _, required := range []string{"storage_class", "external_sha1", "recovery_phase", "first_seen_at"} {
		if !columns[required] {
			t.Fatalf("gc_s3_orphans is missing required surviving column %q", required)
		}
	}
}
