//go:build integration

package gc

import "context"

// BlockCandidatesScanCursorKey is the gc_stats key for orphaned-block discovery.
// Integration tests save and restore it around a scoped ScanOrphanedBlocksOnce.
// This alias is integration-tagged so the production package API stays unchanged.
const BlockCandidatesScanCursorKey = gcBlockCandidatesCursorKey

// ScanOrphanedBlocksOnce runs only the orphaned-block rediscovery phase.
// Integration tests use it so a full ScanOnce does not advance unrelated
// scanner cursors. Callers must save and restore BlockCandidatesScanCursorKey.
func (s *Scanner) ScanOrphanedBlocksOnce(ctx context.Context) (int, error) {
	return s.scanOrphanedBlocks(ctx)
}
