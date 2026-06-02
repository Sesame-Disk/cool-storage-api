package httputil

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type countingEOFReader struct {
	reads int
}

func (r *countingEOFReader) Read(_ []byte) (int, error) {
	r.reads++
	return 0, io.EOF
}

func TestReadAllWithLimitRejectsDeclaredSizeBeforeReading(t *testing.T) {
	reader := &countingEOFReader{}

	data, err := ReadAllWithLimit(reader, 6, 5)
	if !errors.Is(err, ErrReadLimitExceeded) {
		t.Fatalf("ReadAllWithLimit() error = %v, want ErrReadLimitExceeded", err)
	}
	if data != nil {
		t.Fatalf("data = %q, want nil", data)
	}
	if reader.reads != 0 {
		t.Fatalf("reader.reads = %d, want 0", reader.reads)
	}
}

func TestReadAllWithLimitRejectsBodyOverLimit(t *testing.T) {
	data, err := ReadAllWithLimit(bytes.NewReader([]byte("abcdef")), 0, 5)
	if !errors.Is(err, ErrReadLimitExceeded) {
		t.Fatalf("ReadAllWithLimit() error = %v, want ErrReadLimitExceeded", err)
	}
	if data != nil {
		t.Fatalf("data = %q, want nil", data)
	}
}

func TestReadAllWithLimitAllowsBodyWithinLimit(t *testing.T) {
	data, err := ReadAllWithLimit(bytes.NewReader([]byte("abcde")), 5, 5)
	if err != nil {
		t.Fatalf("ReadAllWithLimit() error = %v, want nil", err)
	}
	if string(data) != "abcde" {
		t.Fatalf("data = %q, want abcde", data)
	}
}
