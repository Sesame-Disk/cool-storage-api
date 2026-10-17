//go:build integration

package gc

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// Filter only the phase's library enumeration. Selection, queue persistence,
// filesystem reads and all worker mutations use the real Cassandra store.
type phase5OwnedLibraryStore struct {
	GCStore
	org, library uuid.UUID
}

func (s phase5OwnedLibraryStore) ListLibrariesWithVersionTTL() ([]LibraryTTLInfo, error) {
	rows, err := s.GCStore.ListLibrariesWithVersionTTL()
	if err != nil {
		return nil, err
	}
	var owned []LibraryTTLInfo
	for _, row := range rows {
		if row.OrgID == s.org && row.LibraryID == s.library {
			owned = append(owned, row)
		}
	}
	return owned, nil
}

// Explicit destructive-test opt-in; ordinary APIs/replicas remain GC-OFF.
// Run through the real Phase 5 selector and public organization-scoped worker.
func TestPhase5SharedTreesRealStack(t *testing.T) {
	if os.Getenv("SESAMEFS_PHASE5_REAL_STACK") != "1" {
		t.Skip("requires owned Cassandra/S3 fixtures: SESAMEFS_PHASE5_REAL_STACK=1")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GC.Enabled {
		t.Fatal("ordinary GC must remain disabled")
	}
	database, err := db.New(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	class := cfg.Storage.DefaultClass
	sc, ok := cfg.Storage.Classes[class]
	if !ok {
		t.Fatalf("default storage class %q not configured", class)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s3, err := storage.NewS3Store(ctx, storage.S3Config{Endpoint: sc.Endpoint, Bucket: sc.Bucket, Region: sc.Region, AccessKeyID: sc.AccessKey, SecretAccessKey: sc.SecretKey, UsePathStyle: sc.UsePathStyle})
	if err != nil {
		t.Fatal(err)
	}
	if err := s3.HeadBucket(ctx); err != nil {
		t.Fatal(err)
	}

	for _, sameRoot := range []bool{false, true} {
		t.Run(map[bool]string{false: "shared_subtrees", true: "identical_root"}[sameRoot], func(t *testing.T) {
			org, library := uuid.New(), uuid.New()
			store := NewCassandraStore(database)
			session := database.Session()
			scoped := phase5OwnedLibraryStore{GCStore: store, org: org, library: library}
			queue := NewQueue(scoped)
			manager := storage.NewManager()
			manager.RegisterBackend(class, s3, "")
			worker := NewWorker(scoped, NewStorageManagerAdapter(manager), queue, 100, 0, false, &Stats{})
			bs, err := storage.NewOrgBlockStore(s3, "blocks/", org.String())
			if err != nil {
				t.Fatal(err)
			}
			id := func(s string) string { h := sha1.Sum([]byte(library.String() + s)); return hex.EncodeToString(h[:]) }
			head, ancestor, young, expired := id("head"), id("ancestor"), id("young"), id("expired")
			rootH, rootR, rootY, rootX := id("root-head"), id("root-retained"), id("root-young"), id("root-expired")
			dirH, dirY, fileH, fileY := id("dir-head"), id("dir-young"), id("file-head"), id("file-young")
			if sameRoot {
				rootX = rootH
			}
			payload := []byte("phase5-owned-" + uuid.NewString())
			sum := sha256.Sum256(payload)
			block := hex.EncodeToString(sum[:])
			key, err := bs.MintStorageKey(block)
			if err != nil {
				t.Fatal(err)
			}
			// Register cleanup before the first durable write; every key is fixture-owned.
			t.Cleanup(func() {
				cctx, ccancel := context.WithTimeout(context.Background(), time.Minute)
				defer ccancel()
				items, err := store.DequeueBatch(org, 100, time.Now().Add(time.Hour))
				if err != nil {
					t.Error(err)
				} else {
					for _, item := range items {
						if err := queue.Complete(item); err != nil {
							t.Error(err)
						}
					}
				}
				for _, q := range []string{"DELETE FROM commits WHERE library_id = ?", "DELETE FROM fs_objects WHERE library_id = ?"} {
					if err := session.Query(q, library.String()).Exec(); err != nil {
						t.Error(err)
					}
				}
				batch := session.Batch(gocql.LoggedBatch)
				db.AddDeleteLibraryPolicyQuery(batch, db.GCLibraryPolicyVersionTTL, org.String(), library.String())
				batch.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, org.String(), library.String())
				batch.Query(`DELETE FROM block_references WHERE org_id = ? AND block_id = ?`, org.String(), block)
				batch.Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, org.String(), block)
				batch.Query(`DELETE FROM gc_active_orgs WHERE bucket = ? AND org_id = ?`, gcOrgBucket(org), org.String())
				batch.Query(`DELETE FROM gc_dirty_orgs WHERE bucket = ? AND org_id = ?`, gcOrgBucket(org), org.String())
				batch.Query(`DELETE FROM gc_org_stats WHERE org_id = ?`, org.String())
				if err := batch.Exec(); err != nil {
					t.Error(err)
				}
				for kind, ids := range map[db.IdentityKind][]string{db.IdentityKindCommit: {head, ancestor, young, expired}, db.IdentityKindFSObject: {rootH, rootR, rootY, rootX, dirH, dirY, fileH, fileY}} {
					for _, identity := range ids {
						if err := session.Query(`DELETE FROM identity_authority_claims WHERE library_id = ? AND identity_kind = ? AND identity_id = ?`, library.String(), string(kind), identity).Exec(); err != nil {
							t.Error(err)
						}
					}
				}
				// Verify physical and metadata cleanup rather than relying on teardown
				// returning no error. All probes use this fixture's exact partitions.
				for _, table := range []string{"commits", "fs_objects"} {
					var count int
					if err := session.Query("SELECT count(*) FROM "+table+" WHERE library_id = ?", library.String()).Scan(&count); err != nil || count != 0 {
						t.Errorf("owned %s cleanup: count=%d err=%v", table, count, err)
					}
				}
				if err := bs.DeleteBlockByStorageKey(cctx, key); err != nil {
					t.Error(err)
				}
				if exists, err := bs.ObjectExists(cctx, key); err != nil || exists {
					t.Errorf("owned object cleanup: exists=%v err=%v", exists, err)
				}
				if items, err := store.DequeueBatch(org, 100, time.Now().Add(time.Hour)); err != nil || len(items) != 0 {
					t.Errorf("owned queue cleanup: items=%d err=%v", len(items), err)
				}
			})
			creator := uuid.NewString()
			now := time.Now().UTC().Truncate(time.Millisecond)
			if _, err := bs.PutObjectAutoDirect(ctx, key, payload); err != nil {
				t.Fatal(err)
			}
			physical := db.BlockPhysicalLocation{StorageClass: class, StorageKey: key}
			if result := database.InstallBlockMetadata(ctx, org.String(), db.PlainBlockRepresentationID, block, "", len(payload), physical); result.Outcome != db.InstallBlockMetadataApplied {
				t.Fatalf("INSTALL: %+v", result)
			}
			if err := session.Query(`INSERT INTO libraries (org_id,library_id,head_commit_id,version_ttl_days,storage_class,block_representation_id,encrypted) VALUES (?,?,?,?,?,?,?)`, org.String(), library.String(), head, 30, class, db.PlainBlockRepresentationID, false).Exec(); err != nil {
				t.Fatal(err)
			}
			batch := session.Batch(gocql.LoggedBatch)
			db.AddUpsertLibraryPolicyQuery(batch, db.GCLibraryPolicyVersionTTL, org.String(), library.String(), 30, head, now)
			if err := batch.Exec(); err != nil {
				t.Fatal(err)
			}
			// Old HEAD ancestors and recent off-chain commits must both remain retained.
			for _, c := range []struct {
				id, root, parent string
				at               time.Time
			}{{head, rootH, ancestor, now}, {ancestor, rootR, "", now.Add(-120 * 24 * time.Hour)}, {young, rootY, "", now}, {expired, rootX, "", now.Add(-90 * 24 * time.Hour)}} {
				authorized, err := db.AuthorizeCommitProjection(ctx, session, db.CommitProjection{LibraryID: library.String(), CommitID: c.id, RootFSID: c.root, ParentID: c.parent, CreatorID: creator, Description: "owned Phase 5 fixture", CreatedAt: c.at})
				if err != nil {
					t.Fatal(err)
				}
				if err := db.MaterializeAuthorizedCommit(session, authorized); err != nil {
					t.Fatal(err)
				}
			}
			dirs := map[string][]string{rootH: {dirH}, rootR: {dirH}, rootY: {dirY}, dirH: {fileH}, dirY: {fileY}}
			if !sameRoot {
				dirs[rootX] = []string{dirH, dirY}
			}
			for fs, children := range dirs {
				entries := make([]map[string]string, 0, len(children))
				for _, child := range children {
					entries = append(entries, map[string]string{"id": child})
				}
				raw, err := json.Marshal(entries)
				if err != nil {
					t.Fatal(err)
				}
				authorized, err := db.AuthorizeFSObjectProjection(ctx, session, db.FSObjectProjection{LibraryID: library.String(), FSID: fs, ObjectType: "dir", DirectoryEntries: string(raw)})
				if err != nil {
					t.Fatal(err)
				}
				if err := db.MaterializeAuthorizedFSObject(session, authorized); err != nil {
					t.Fatal(err)
				}
			}
			for _, fs := range []string{fileH, fileY} {
				authorized, err := db.AuthorizeFSObjectProjection(ctx, session, db.FSObjectProjection{LibraryID: library.String(), FSID: fs, ObjectType: "file", SizeBytes: int64(len(payload)), FileLayout: db.FileStoragePairedCanonical, LogicalSHA1IDs: []string{id("logical-block")}, CanonicalSHA256IDs: []string{block}})
				if err != nil {
					t.Fatal(err)
				}
				if err := db.MaterializeAuthorizedFSObject(session, authorized); err != nil {
					t.Fatal(err)
				}
				if err := database.AddBlockReference(org.String(), block, db.BlockReferrerForFSObject(library.String(), fs), library.String(), 0); err != nil {
					t.Fatal(err)
				}
			}
			scanner := NewScanner(scoped, queue, &Stats{}, cfg.GC)
			if n, err := scanner.scanExpiredVersions(ctx); err != nil || n != 1 {
				t.Fatalf("Phase 5 selection: n=%d err=%v, want exactly expired commit", n, err)
			}
			selected, err := store.DequeueBatch(org, 100, time.Now().Add(time.Second))
			if err != nil || len(selected) != 1 || selected[0].ItemID != expired || selected[0].LibraryGuardMode != LibraryGuardNone {
				t.Fatalf("unexpected Phase 5 work: %+v err=%v", selected, err)
			}
			// Three passes expose root/directory loss on the old runtime without executing
			// the queued file/reference/block destruction from that unsafe cascade.
			for i := 0; i < 3; i++ {
				n, err := worker.ProcessOrgOnce(ctx, org)
				if err != nil {
					t.Fatal(err)
				}
				if i == 0 && n != 1 {
					t.Fatalf("expired commit did not successfully execute: n=%d", n)
				}
			}
			for _, c := range []string{head, ancestor, young} {
				if _, err := store.GetCommit(library, c); err != nil {
					t.Errorf("retained commit %s: %v", c, err)
				}
			}
			if _, err := store.GetCommit(library, expired); err != gocql.ErrNotFound {
				t.Errorf("expired commit not removed: %v", err)
			}
			var observedHead string
			if err := session.Query(`SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`, org.String(), library.String()).Consistency(gocql.Serial).Scan(&observedHead); err != nil || observedHead != head {
				t.Errorf("HEAD changed: %s %v", observedHead, err)
			}
			for fs := range dirs {
				if _, err := store.GetFSObject(library, fs); err != nil {
					t.Errorf("shared/retained directory %s deleted: %v", fs, err)
				}
			}
			for _, fs := range []string{fileH, fileY} {
				if _, err := store.GetFSObject(library, fs); err != nil {
					t.Error(err)
				}
			}
			for _, fs := range []string{fileH, fileY} {
				var count int
				if err := session.Query(`SELECT count(*) FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?`, org.String(), block, db.BlockReferrerForFSObject(library.String(), fs)).Scan(&count); err != nil || count != 1 {
					t.Errorf("permanent ref %s lost: %d %v", fs, count, err)
				}
			}
			if info, err := store.GetBlockInfo(org, block); err != nil || info.StorageKey != key {
				t.Errorf("canonical P changed: %+v %v", info, err)
			}
			if data, err := bs.GetBlockByStorageKey(ctx, key); err != nil || !bytes.Equal(data, payload) {
				t.Errorf("S3 bytes changed: %v", err)
			}
			if items, err := store.DequeueBatch(org, 100, time.Now().Add(time.Hour)); err != nil || len(items) != 0 {
				t.Errorf("unexpected descendant work: %d %v", len(items), err)
			}
			if t.Failed() {
				return
			}
			t.Logf("owned Phase 5 sameRoot=%t: expired commit retired; HEAD/ancestor/recent trees, 2 permanent refs and actual S3 bytes checked", sameRoot)
		})
	}
}
