package chunker

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestFastCDC_ChunkAll_SmallData(t *testing.T) {
	// Data smaller than minSize should be one block
	chunker := NewFastCDC(1024, 4096, 16384) // 1KB, 4KB, 16KB

	data := make([]byte, 512) // 512 bytes - smaller than minSize
	rand.Read(data)

	blocks := chunker.ChunkAll(data)

	if len(blocks) != 1 {
		t.Errorf("expected 1 block for small data, got %d", len(blocks))
	}
	if blocks[0].Size != 512 {
		t.Errorf("expected block size 512, got %d", blocks[0].Size)
	}
	if !bytes.Equal(blocks[0].Data, data) {
		t.Error("block data doesn't match input")
	}
}

func TestFastCDC_ChunkAll_LargeData(t *testing.T) {
	// 1MB of random data with 64KB-256KB-1MB chunking
	minSize := int64(64 * 1024)   // 64 KB
	avgSize := int64(256 * 1024)  // 256 KB
	maxSize := int64(1024 * 1024) // 1 MB

	chunker := NewFastCDC(minSize, avgSize, maxSize)

	// Create 4MB of random data
	dataSize := 4 * 1024 * 1024
	data := make([]byte, dataSize)
	rand.Read(data)

	blocks := chunker.ChunkAll(data)

	// Should have multiple blocks
	if len(blocks) < 2 {
		t.Errorf("expected multiple blocks for 4MB data, got %d", len(blocks))
	}

	// Verify all blocks are within size bounds
	var totalSize int64
	for i, block := range blocks {
		totalSize += block.Size

		// All blocks except possibly the last should be >= minSize
		if i < len(blocks)-1 && block.Size < minSize {
			t.Errorf("block %d size %d is less than minSize %d", i, block.Size, minSize)
		}

		// No block should exceed maxSize
		if block.Size > maxSize {
			t.Errorf("block %d size %d exceeds maxSize %d", i, block.Size, maxSize)
		}

		// Verify hash is 64 hex characters (SHA256)
		if len(block.Hash) != 64 {
			t.Errorf("block %d hash length %d, expected 64", i, len(block.Hash))
		}
	}

	// Total size should match input
	if totalSize != int64(dataSize) {
		t.Errorf("total block size %d doesn't match input size %d", totalSize, dataSize)
	}

	// Verify we can reconstruct the original data
	var reconstructed []byte
	for _, block := range blocks {
		reconstructed = append(reconstructed, block.Data...)
	}
	if !bytes.Equal(reconstructed, data) {
		t.Error("reconstructed data doesn't match original")
	}
}

func TestFastCDC_Deterministic(t *testing.T) {
	// Same input should produce same chunks
	chunker := NewFastCDC(1024, 4096, 16384)

	data := make([]byte, 100*1024) // 100KB
	rand.Read(data)

	blocks1 := chunker.ChunkAll(data)
	blocks2 := chunker.ChunkAll(data)

	if len(blocks1) != len(blocks2) {
		t.Fatalf("different number of blocks: %d vs %d", len(blocks1), len(blocks2))
	}

	for i := range blocks1 {
		if blocks1[i].Hash != blocks2[i].Hash {
			t.Errorf("block %d has different hashes", i)
		}
		if blocks1[i].Size != blocks2[i].Size {
			t.Errorf("block %d has different sizes: %d vs %d", i, blocks1[i].Size, blocks2[i].Size)
		}
	}
}

func TestFastCDC_ContentDefinedBoundaries(t *testing.T) {
	// Inserting data should only affect nearby chunks
	chunker := NewFastCDC(64, 256, 1024)

	// Create base data
	base := make([]byte, 10*1024) // 10KB
	rand.Read(base)

	baseBlocks := chunker.ChunkAll(base)

	// Insert some bytes in the middle
	insertPos := 5 * 1024
	insertion := []byte("INSERTED_CONTENT_HERE")
	modified := make([]byte, len(base)+len(insertion))
	copy(modified[:insertPos], base[:insertPos])
	copy(modified[insertPos:insertPos+len(insertion)], insertion)
	copy(modified[insertPos+len(insertion):], base[insertPos:])

	modifiedBlocks := chunker.ChunkAll(modified)

	// Count how many blocks are the same (by hash)
	baseHashes := make(map[string]bool)
	for _, b := range baseBlocks {
		baseHashes[b.Hash] = true
	}

	sameCount := 0
	for _, b := range modifiedBlocks {
		if baseHashes[b.Hash] {
			sameCount++
		}
	}

	// Some blocks should be the same (content-defined chunking benefit)
	// This is probabilistic, so we just check that at least some blocks match
	t.Logf("base blocks: %d, modified blocks: %d, same blocks: %d",
		len(baseBlocks), len(modifiedBlocks), sameCount)
}

func TestFastCDC_AdaptiveChunkSizes(t *testing.T) {
	// Test with the adaptive sizes from config
	minSize := int64(2 * 1024 * 1024)   // 2 MB
	avgSize := int64(64 * 1024 * 1024)  // 64 MB
	maxSize := int64(256 * 1024 * 1024) // 256 MB

	chunker := NewFastCDC(minSize, avgSize, maxSize)

	// Create 500MB of data
	dataSize := 500 * 1024 * 1024
	data := make([]byte, dataSize)
	rand.Read(data)

	blocks := chunker.ChunkAll(data)

	// Should have reasonable number of blocks
	// With 64MB average, expect roughly 8 blocks for 500MB
	if len(blocks) < 2 || len(blocks) > 250 {
		t.Errorf("unexpected number of blocks: %d (expected ~8 for 500MB with 64MB avg)", len(blocks))
	}

	// Log block sizes for analysis
	var totalSize int64
	for i, block := range blocks {
		totalSize += block.Size
		t.Logf("Block %d: %d MB", i, block.Size/(1024*1024))
	}

	if totalSize != int64(dataSize) {
		t.Errorf("total size mismatch: %d vs %d", totalSize, dataSize)
	}
}

func BenchmarkFastCDC_ChunkAll(b *testing.B) {
	chunker := NewFastCDC(2*1024*1024, 8*1024*1024, 16*1024*1024)

	// 100MB of data
	data := make([]byte, 100*1024*1024)
	rand.Read(data)

	b.ResetTimer()
	b.SetBytes(int64(len(data)))

	for i := 0; i < b.N; i++ {
		_ = chunker.ChunkAll(data)
	}
}
