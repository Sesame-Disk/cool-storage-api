//go:build !integration

package v2

// Production barriers are true no-ops: no mutex, no callback table, no test setter.
// Integration builds replace this file with file_from_blocks_publication_barriers_integration.go.
func fileFromBlocksAfterVerifiedBarrier(string) {}

func fileFromBlocksAfterStagedBarrier(string) {}

func fileFromBlocksBeforeHeadBarrier(string) error { return nil }
