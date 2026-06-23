package db

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// BlockUploadSessionTTLSeconds bounds how long a server-issued web block-upload
// session survives. Aligned with the provisional block reference TTL so a
// session and the blocks it pins expire together: the last /blocks/upload under
// the session refreshes the provisional ref TTL, and the session row carries the
// same window. A successful commit re-writes the row (resetting the TTL) so the
// idempotency record outlives client retries.
const BlockUploadSessionTTLSeconds = ProvisionalBlockReferenceTTLSeconds

// BlockUploadSession is a server-issued handle for the web content-addressed
// upload flow. It scopes a batch of /blocks/upload calls and the final
// file-from-blocks commit to one (org, user, repo). SessionID doubles as the
// provisional block reference owner ("up:<session_id>").
type BlockUploadSession struct {
	SessionID      string
	OrgID          string
	UserID         string
	RepoID         string
	ParentDir      string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	Committed      bool
	ManifestDigest string
	ResultPath     string
	ResultFilename string
	ResultCommitID string
}

// CreateBlockUploadSession mints a new server-issued session bound to the
// caller's (org, user, repo). The random 256-bit SessionID is unguessable so it
// cannot be reused or collided across users. The row carries a TTL so an
// abandoned session self-expires.
func (db *DB) CreateBlockUploadSession(orgID, userID, repoID, parentDir string) (BlockUploadSession, error) {
	idBytes := make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return BlockUploadSession{}, fmt.Errorf("generate block upload session id: %w", err)
	}
	now := time.Now().UTC()
	s := BlockUploadSession{
		SessionID: hex.EncodeToString(idBytes),
		OrgID:     orgID,
		UserID:    userID,
		RepoID:    repoID,
		ParentDir: parentDir,
		CreatedAt: now,
		ExpiresAt: now.Add(BlockUploadSessionTTLSeconds * time.Second),
	}
	if err := db.Session().Query(`
		INSERT INTO block_upload_sessions
			(session_id, org_id, user_id, repo_id, parent_dir, created_at, expires_at, committed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) USING TTL ?
	`, s.SessionID, s.OrgID, s.UserID, s.RepoID, s.ParentDir, s.CreatedAt, s.ExpiresAt, false, BlockUploadSessionTTLSeconds).Exec(); err != nil {
		return BlockUploadSession{}, fmt.Errorf("insert block upload session: %w", err)
	}
	return s, nil
}

// GetBlockUploadSession reads a session by id. ok=false when the row is missing
// (expired via TTL or never existed).
func (db *DB) GetBlockUploadSession(sessionID string) (BlockUploadSession, bool, error) {
	var s BlockUploadSession
	err := db.Session().Query(`
		SELECT session_id, org_id, user_id, repo_id, parent_dir, created_at, expires_at,
		       committed, manifest_digest, result_path, result_filename, result_commit_id
		FROM block_upload_sessions WHERE session_id = ?
	`, sessionID).Scan(&s.SessionID, &s.OrgID, &s.UserID, &s.RepoID, &s.ParentDir,
		&s.CreatedAt, &s.ExpiresAt, &s.Committed, &s.ManifestDigest,
		&s.ResultPath, &s.ResultFilename, &s.ResultCommitID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return BlockUploadSession{}, false, nil
		}
		return BlockUploadSession{}, false, err
	}
	return s, true, nil
}

// MarkBlockUploadSessionCommitted records the committed result so a retried
// commit with the same manifest is idempotent (returns the same file instead of
// auto-renaming a duplicate). The whole row is re-written with a fresh TTL so
// the idempotency record stays consistent and survives client retries.
func (db *DB) MarkBlockUploadSessionCommitted(s BlockUploadSession, manifestDigest, resultPath, resultFilename, resultCommitID string) error {
	expiresAt := time.Now().UTC().Add(BlockUploadSessionTTLSeconds * time.Second)
	if err := db.Session().Query(`
		INSERT INTO block_upload_sessions
			(session_id, org_id, user_id, repo_id, parent_dir, created_at, expires_at,
			 committed, manifest_digest, result_path, result_filename, result_commit_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TTL ?
	`, s.SessionID, s.OrgID, s.UserID, s.RepoID, s.ParentDir, s.CreatedAt, expiresAt,
		true, manifestDigest, resultPath, resultFilename, resultCommitID,
		BlockUploadSessionTTLSeconds).Exec(); err != nil {
		return fmt.Errorf("mark block upload session committed: %w", err)
	}
	return nil
}

// ClaimBlockUploadSessionForCommit atomically claims the session for committing
// the given manifest digest, via a Cassandra LWT. Exactly one concurrent caller
// gets applied=true (the winner, which then runs finalize); the rest get
// applied=false and must wait for / return the winner's idempotent result. The
// claim refreshes the row TTL so a crash after claiming cannot leave immortal
// session columns behind; a successful commit later re-writes the whole row with
// a fresh TTL, and a failed finalize releases the claim.
func (db *DB) ClaimBlockUploadSessionForCommit(sessionID, manifestDigest string) (bool, error) {
	var currentCommitted bool
	applied, err := db.Session().Query(`
		UPDATE block_upload_sessions USING TTL ? SET committed = true, manifest_digest = ?
		WHERE session_id = ? IF committed = false
	`, BlockUploadSessionTTLSeconds, manifestDigest, sessionID).ScanCAS(&currentCommitted)
	if err != nil {
		return false, fmt.Errorf("claim block upload session for commit: %w", err)
	}
	return applied, nil
}

// ReleaseBlockUploadSessionCommit reverts a commit claim so a retry can proceed
// after a failed finalize. Idempotent.
func (db *DB) ReleaseBlockUploadSessionCommit(sessionID string) error {
	if err := db.Session().Query(`
		UPDATE block_upload_sessions USING TTL ? SET committed = false WHERE session_id = ?
	`, BlockUploadSessionTTLSeconds, sessionID).Exec(); err != nil {
		return fmt.Errorf("release block upload session commit claim: %w", err)
	}
	return nil
}

// BlockHasReferrer reports whether a specific (block, referrer) reference row
// exists. Used to verify that a block is owned by a given upload session's
// provisional reference ("up:<session_id>") before a commit publishes it.
func (db *DB) BlockHasReferrer(orgID, blockID, referrer string) (bool, error) {
	var got string
	err := db.Session().Query(`
		SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgID, blockID, referrer).Scan(&got)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return got != "", nil
}

// ListBlockReferrers returns all referrer strings currently keeping a block
// alive. The (org, block) partition holds only this block's reference rows, so
// the scan is a single-partition read. Used to distinguish permanent ("fs:"/
// "pub:") liveness from provisional ("up:") upload references.
func (db *DB) ListBlockReferrers(orgID, blockID string) ([]string, error) {
	iter := db.Session().Query(`
		SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Iter()
	var referrers []string
	var referrer string
	for iter.Scan(&referrer) {
		referrers = append(referrers, referrer)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return referrers, nil
}
