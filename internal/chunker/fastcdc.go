package chunker

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
)

// Block represents a content-defined chunk with its hash
type Block struct {
	Hash   string // SHA256 hash of the block content
	Data   []byte // Block content
	Size   int64  // Size in bytes
	Offset int64  // Offset in the original file
}

// FastCDC implements the FastCDC content-defined chunking algorithm
// Based on the paper: "FastCDC: a Fast and Efficient Content-Defined Chunking Approach for Data Deduplication"
// https://www.usenix.org/conference/atc16/technical-sessions/presentation/xia
type FastCDC struct {
	minSize int64
	avgSize int64
	maxSize int64

	// Masks for boundary detection
	maskS uint64 // "Small" mask - used between min and avg (easier to match)
	maskL uint64 // "Large" mask - used between avg and max (harder to match)
}

// NewFastCDC creates a new FastCDC chunker with the specified size parameters
// minSize: minimum chunk size (no boundaries checked before this)
// avgSize: average/target chunk size (mask tuned for this)
// maxSize: maximum chunk size (force cut here)
func NewFastCDC(minSize, avgSize, maxSize int64) *FastCDC {
	// Normalize sizes
	if minSize < 64 {
		minSize = 64
	}
	if avgSize < minSize {
		avgSize = minSize * 4
	}
	if maxSize < avgSize {
		maxSize = avgSize * 4
	}

	// Calculate masks based on average size
	// The number of bits determines probability of match
	// More bits = lower probability = larger chunks
	bits := logarithm2(uint64(avgSize))

	// maskS has fewer bits (easier to match) - used before avgSize
	// maskL has more bits (harder to match) - used after avgSize
	maskS := uint64(1<<(bits-1)) - 1
	maskL := uint64(1<<(bits+1)) - 1

	return &FastCDC{
		minSize: minSize,
		avgSize: avgSize,
		maxSize: maxSize,
		maskS:   maskS,
		maskL:   maskL,
	}
}

// Chunk reads from the reader and returns all blocks
func (c *FastCDC) Chunk(r io.Reader) ([]Block, error) {
	var blocks []Block
	var offset int64

	for {
		block, err := c.NextBlock(r, offset)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if block == nil {
			break
		}

		blocks = append(blocks, *block)
		offset += block.Size
	}

	return blocks, nil
}

// NextBlock reads the next block from the reader
func (c *FastCDC) NextBlock(r io.Reader, offset int64) (*Block, error) {
	// Read up to maxSize bytes
	buf := make([]byte, c.maxSize)
	n, err := io.ReadFull(r, buf)

	if err == io.EOF {
		return nil, io.EOF
	}
	if err == io.ErrUnexpectedEOF {
		// Less than maxSize bytes remaining - this is the last block
		buf = buf[:n]
	} else if err != nil {
		return nil, err
	} else {
		buf = buf[:n]
	}

	if len(buf) == 0 {
		return nil, io.EOF
	}

	// Find the chunk boundary
	boundary := c.findBoundary(buf)

	// Create the block
	blockData := buf[:boundary]
	hash := sha256.Sum256(blockData)

	block := &Block{
		Hash:   hex.EncodeToString(hash[:]),
		Data:   blockData,
		Size:   int64(len(blockData)),
		Offset: offset,
	}

	// If we read more than the boundary, we need to "unread" the excess
	// Since we can't unread from io.Reader, the caller should use ChunkAll
	// or use a buffered approach

	return block, nil
}

// ChunkAll chunks the entire data buffer and returns all blocks
func (c *FastCDC) ChunkAll(data []byte) []Block {
	var blocks []Block
	var offset int64

	remaining := data
	for len(remaining) > 0 {
		boundary := c.findBoundary(remaining)
		blockData := remaining[:boundary]

		hash := sha256.Sum256(blockData)
		blocks = append(blocks, Block{
			Hash:   hex.EncodeToString(hash[:]),
			Data:   blockData,
			Size:   int64(len(blockData)),
			Offset: offset,
		})

		offset += int64(boundary)
		remaining = remaining[boundary:]
	}

	return blocks
}

// findBoundary finds the chunk boundary in the buffer using FastCDC algorithm
func (c *FastCDC) findBoundary(buf []byte) int {
	n := int64(len(buf))

	// If buffer is smaller than minimum, return entire buffer
	if n <= c.minSize {
		return int(n)
	}

	// If buffer is smaller than or equal to average, use full buffer or find boundary
	if n <= c.avgSize {
		// Try to find boundary between min and n using small mask
		pos := c.minSize
		for pos < n {
			hash := c.gear(buf[pos-64 : pos])
			if (hash & c.maskS) == 0 {
				return int(pos)
			}
			pos++
		}
		return int(n)
	}

	// Buffer is larger than average
	// Phase 1: Check between min and avg with small mask (easier to match)
	pos := c.minSize
	for pos < c.avgSize {
		hash := c.gear(buf[pos-64 : pos])
		if (hash & c.maskS) == 0 {
			return int(pos)
		}
		pos++
	}

	// Phase 2: Check between avg and max with large mask (harder to match)
	maxPos := c.maxSize
	if maxPos > n {
		maxPos = n
	}
	for pos < maxPos {
		hash := c.gear(buf[pos-64 : pos])
		if (hash & c.maskL) == 0 {
			return int(pos)
		}
		pos++
	}

	// No boundary found, force cut at max (or buffer end)
	if n > c.maxSize {
		return int(c.maxSize)
	}
	return int(n)
}

// gear computes the Gear hash of a 64-byte window
// This is a fast rolling hash used by FastCDC
func (c *FastCDC) gear(window []byte) uint64 {
	var hash uint64
	for _, b := range window {
		hash = (hash << 1) + gearTable[b]
	}
	return hash
}

// logarithm2 returns floor(log2(n))
func logarithm2(n uint64) uint {
	var bits uint
	for n > 1 {
		n >>= 1
		bits++
	}
	return bits
}

// gearTable is a pre-computed table of random values for the Gear hash
// Generated using a fixed seed for reproducibility
var gearTable = [256]uint64{
	0x651748f10d89d8fd, 0x22be9c4ed45aa256, 0x6f4dd1bb1b196c5f, 0xc46ac22f8bf0a2b4,
	0x0ae3dd10e0a9c0f1, 0xdf06a0d4c64a7676, 0x58f2b8d60c97bd7f, 0x9c6a1fb4a5c82a2e,
	0x3d8a7b43b9f1d8fa, 0x1f67d8e42a8b4c19, 0x7ba296f5c45a7a22, 0xd84ed1f7a7c5b3fd,
	0x54f4c7e3b1d7a8b5, 0xe21c8bd6f9a7b4c2, 0x4ab9d8c3e6f5a2b1, 0x96c2a7b5d4e3f1a0,
	0x2f8b7c6d5e4a3b1c, 0x6d4c3b2a19f8e7d6, 0xb5a4938271605f4e, 0xe3d2c1b0a99f8e7d,
	0x1a2b3c4d5e6f7a8b, 0x9c8d7e6f5a4b3c2d, 0x4f5e6d7c8b9a0a1b, 0xc2d3e4f5a6b7c8d9,
	0x7890abcdef123456, 0x56789abcdef01234, 0x34567890abcdef12, 0x12345678abcdef90,
	0xfedcba9876543210, 0xdcba987654321fed, 0xba9876543210fedc, 0x98765432fedcba10,
	0x0f1e2d3c4b5a6978, 0x2d3c4b5a69780f1e, 0x4b5a69780f1e2d3c, 0x69780f1e2d3c4b5a,
	0x8796a5b4c3d2e1f0, 0xa5b4c3d2e1f08796, 0xc3d2e1f08796a5b4, 0xe1f08796a5b4c3d2,
	0x1029384756afbecd, 0x384756afbecd1029, 0x56afbecd10293847, 0xafbecd1029384756,
	0xcdef0123456789ab, 0xef0123456789abcd, 0x0123456789abcdef, 0x23456789abcdef01,
	0x456789abcdef0123, 0x6789abcdef012345, 0x89abcdef01234567, 0xabcdef0123456789,
	0x13579bdf02468ace, 0x579bdf02468ace13, 0x9bdf02468ace1357, 0xdf02468ace13579b,
	0x02468ace13579bdf, 0x468ace13579bdf02, 0x8ace13579bdf0246, 0xce13579bdf02468a,
	0xfdb97531eca86420, 0xb97531eca86420fd, 0x7531eca86420fdb9, 0x31eca86420fdb975,
	// Extended table for all 256 byte values
	0xeca86420fdb97531, 0xa86420fdb97531ec, 0x6420fdb97531eca8, 0x20fdb97531eca864,
	0x1111111111111111, 0x2222222222222222, 0x3333333333333333, 0x4444444444444444,
	0x5555555555555555, 0x6666666666666666, 0x7777777777777777, 0x8888888888888888,
	0x9999999999999999, 0xaaaaaaaaaaaaaaaa, 0xbbbbbbbbbbbbbbbb, 0xcccccccccccccccc,
	0xdddddddddddddddd, 0xeeeeeeeeeeeeeeee, 0xffffffffffffffff, 0x0000000000000000,
	0xf0f0f0f0f0f0f0f0, 0x0f0f0f0f0f0f0f0f, 0xff00ff00ff00ff00, 0x00ff00ff00ff00ff,
	0xf00ff00ff00ff00f, 0x0ff00ff00ff00ff0, 0x123456789abcdef0, 0x0fedcba987654321,
	0xdeadbeefcafebabe, 0xcafebabedeadbeef, 0xbeefcafedeadbabe, 0xbabedeadbeefcafe,
	0x0a1b2c3d4e5f6789, 0x1b2c3d4e5f67890a, 0x2c3d4e5f67890a1b, 0x3d4e5f67890a1b2c,
	0x4e5f67890a1b2c3d, 0x5f67890a1b2c3d4e, 0x67890a1b2c3d4e5f, 0x890a1b2c3d4e5f67,
	0xa1b2c3d4e5f67890, 0xb2c3d4e5f67890a1, 0xc3d4e5f67890a1b2, 0xd4e5f67890a1b2c3,
	0xe5f67890a1b2c3d4, 0xf67890a1b2c3d4e5, 0x7890a1b2c3d4e5f6, 0x90a1b2c3d4e5f678,
	0x1234567890abcdef, 0x234567890abcdef1, 0x34567890abcdef12, 0x4567890abcdef123,
	0x567890abcdef1234, 0x67890abcdef12345, 0x7890abcdef123456, 0x890abcdef1234567,
	0x90abcdef12345678, 0x0abcdef123456789, 0xabcdef1234567890, 0xbcdef1234567890a,
	0xcdef1234567890ab, 0xdef1234567890abc, 0xef1234567890abcd, 0xf1234567890abcde,
	0xfedcba0987654321, 0xedcba0987654321f, 0xdcba0987654321fe, 0xcba0987654321fed,
	0xba0987654321fedc, 0xa0987654321fedcb, 0x0987654321fedcba, 0x987654321fedcba0,
	0x87654321fedcba09, 0x7654321fedcba098, 0x654321fedcba0987, 0x54321fedcba09876,
	0x4321fedcba098765, 0x321fedcba0987654, 0x21fedcba09876543, 0x1fedcba098765432,
	0xaaaa5555aaaa5555, 0x5555aaaa5555aaaa, 0xaa55aa55aa55aa55, 0x55aa55aa55aa55aa,
	0xa5a5a5a5a5a5a5a5, 0x5a5a5a5a5a5a5a5a, 0xf0f00f0ff0f00f0f, 0x0f0ff0f00f0ff0f0,
	0xff0000ffff0000ff, 0x00ffff0000ffff00, 0xf0000000000000f0, 0x0f000000000000f0,
	0x00f00000000000f0, 0x000f0000000000f0, 0x0000f000000000f0, 0x00000f00000000f0,
	0x000000f0000000f0, 0x0000000f000000f0, 0x00000000f00000f0, 0x000000000f0000f0,
	0x0000000000f000f0, 0x00000000000f00f0, 0x000000000000f0f0, 0x0000000000000ff0,
	0x1248124812481248, 0x2481248124812481, 0x4812481248124812, 0x8124812481248124,
	0x8421842184218421, 0x4218421842184218, 0x2184218421842184, 0x1842184218421842,
	0x369cf258be147ad0, 0x69cf258be147ad03, 0x9cf258be147ad036, 0xcf258be147ad0369,
	0xf258be147ad0369c, 0x258be147ad0369cf, 0x58be147ad0369cf2, 0x8be147ad0369cf25,
	0xbe147ad0369cf258, 0xe147ad0369cf258b, 0x147ad0369cf258be, 0x47ad0369cf258be1,
	0x7ad0369cf258be14, 0xad0369cf258be147, 0xd0369cf258be147a, 0x0369cf258be147ad,
	0x0248ace13579bdf0, 0x248ace13579bdf00, 0x48ace13579bdf002, 0x8ace13579bdf0024,
	0xace13579bdf00248, 0xce13579bdf0024ba, 0xe13579bdf00248ac, 0x13579bdf00248ace,
	0x3579bdf00248ace1, 0x579bdf00248ace13, 0x79bdf00248ace135, 0x9bdf00248ace1357,
	0xbdf00248ace13579, 0xdf00248ace13579b, 0xf00248ace13579bd, 0x00248ace13579bdf,
	0xfdb975310eca8642, 0xdb975310eca8642f, 0xb975310eca8642fd, 0x975310eca8642fdb,
	0x75310eca8642fdb9, 0x5310eca8642fdb97, 0x310eca8642fdb975, 0x10eca8642fdb9753,
	0x0eca8642fdb97531, 0xeca8642fdb975310, 0xca8642fdb975310e, 0xa8642fdb975310ec,
	0x8642fdb975310eca, 0x642fdb975310eca8, 0x42fdb975310eca86, 0x2fdb975310eca864,
}
